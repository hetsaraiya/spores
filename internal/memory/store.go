// Package memory stores the agent's long-term personal memory as markdown on disk.
//
// The schema is deliberately small: USER.md for identity and standing
// preferences, STACK.md for tooling choices, and SKILLS/<topic>.md for durable
// lessons. Repository-specific facts are intentionally absent — those are
// cheaper to rediscover from the repository than to keep correct here.
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// promptBudget caps how much memory is rendered into a prompt. Files render
	// in priority order; once the budget is hit the rest stay on disk but drop
	// out of the prompt.
	promptBudget = 6000

	maxFileBytes  = 16 << 10  // per file
	maxTotalBytes = 256 << 10 // across all files

	dirPerm  = 0o700
	filePerm = 0o600
)

// rootFiles are the fixed files, in prompt priority order — the earliest
// survives budget truncation longest. SKILLS/* render after these and so drop first.
var rootFiles = []string{"USER.md", "STACK.md"}

const skillsDir = "SKILLS"

// validName allows only the schema above. It blocks traversal and stray paths.
var validName = regexp.MustCompile(`^(USER\.md|STACK\.md|SKILLS/[A-Za-z0-9][A-Za-z0-9._ -]*\.md)$`)

var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// meaningful is the content minus HTML-comment guidance and surrounding space —
// what actually reaches the agent. Scaffolded-but-unfilled files are empty by
// this measure, so they never consume prompt budget.
func meaningful(content string) string {
	return strings.TrimSpace(htmlComment.ReplaceAllString(content, ""))
}

// FileInfo describes one memory file for the portal.
type FileInfo struct {
	Name     string    `json:"name"`
	Present  bool      `json:"present"`
	Size     int       `json:"size"`
	Modified time.Time `json:"modified,omitzero"`
	InPrompt bool      `json:"inPrompt"`
}

type Store struct {
	dir string
	mu  sync.Mutex

	history *history
}

// New prepares the memory directory, scaffolds the root files when absent, and
// enables git-backed revision history when git is available.
func New(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("memory directory is required")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve memory dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(absolute, skillsDir), dirPerm); err != nil {
		return nil, fmt.Errorf("create memory dir: %w", err)
	}
	store := &Store{dir: absolute, history: newHistory(absolute)}
	if err := store.scaffold(); err != nil {
		return nil, err
	}
	return store, nil
}

// Dir is the resolved memory directory, for startup logging.
func (s *Store) Dir() string { return s.dir }

// HistoryEnabled reports whether writes are being committed to git.
func (s *Store) HistoryEnabled() bool { return s.history.enabled }

// Check verifies the directory is writable, without touching stored content.
func (s *Store) Check() error {
	probe := filepath.Join(s.dir, ".writable")
	if err := os.WriteFile(probe, []byte("ok"), filePerm); err != nil {
		return fmt.Errorf("memory dir not writable: %w", err)
	}
	return os.Remove(probe)
}

func (s *Store) path(name string) string {
	return filepath.Join(s.dir, filepath.FromSlash(name))
}

// Read returns one memory file's raw content, including scaffold guidance.
func (s *Store) Read(name string) (string, error) {
	if !validName.MatchString(name) {
		return "", invalidName(name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	content, err := os.ReadFile(s.path(name))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// Write replaces one memory file; empty content deletes it. It reports whether
// the file meaningfully changed — whitespace- and comment-only diffs are
// skipped, so re-emitting stored facts is a no-op that never touches disk.
func (s *Store) Write(name, content string) (bool, error) {
	if !validName.MatchString(name) {
		return false, invalidName(name)
	}
	if !utf8.ValidString(content) {
		return false, fmt.Errorf("memory content must be valid UTF-8")
	}
	if len(content) > maxFileBytes {
		return false, fmt.Errorf("memory content is %d bytes, over the %d-byte limit for one file; consolidate it", len(content), maxFileBytes)
	}
	if reason := containsSecret(content); reason != "" {
		return false, fmt.Errorf("refusing to store %s: memory must never contain credentials", reason)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.path(name)
	existing, readErr := os.ReadFile(path)
	if readErr != nil {
		// Missing file: only meaningful content is a change worth writing.
		if meaningful(content) == "" {
			return false, nil
		}
	} else if meaningful(string(existing)) == meaningful(content) {
		return false, nil
	}
	if err := s.checkTotal(name, len(content)); err != nil {
		return false, err
	}

	if strings.TrimSpace(content) == "" {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		s.history.commit("delete " + name)
		return true, nil
	}
	if err := writeAtomic(path, content); err != nil {
		return false, err
	}
	s.history.commit("update " + name)
	return true, nil
}

// Delete removes a memory file.
func (s *Store) Delete(name string) (bool, error) { return s.Write(name, "") }

// checkTotal bounds the whole store, counting the pending write in place of the
// file's current size. Callers hold the lock.
func (s *Store) checkTotal(name string, pending int) error {
	total := pending
	names, err := s.stored()
	if err != nil {
		return err
	}
	for _, other := range names {
		if other == name {
			continue
		}
		info, err := os.Stat(s.path(other))
		if err != nil {
			continue
		}
		total += int(info.Size())
	}
	if total > maxTotalBytes {
		return fmt.Errorf("memory would total %d bytes, over the %d-byte limit; consolidate or delete a file first", total, maxTotalBytes)
	}
	return nil
}

// stored lists memory files present on disk, in prompt priority order. Callers hold the lock.
func (s *Store) stored() ([]string, error) {
	var names []string
	for _, name := range rootFiles {
		if _, err := os.Stat(s.path(name)); err == nil {
			names = append(names, name)
		}
	}
	entries, err := os.ReadDir(filepath.Join(s.dir, skillsDir))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	var skills []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			skills = append(skills, skillsDir+"/"+entry.Name())
		}
	}
	sort.Strings(skills)
	return append(names, skills...), nil
}

// Names lists the memory files for the portal. The root files always appear,
// present or not, so they can be created; skills appear only once they exist,
// since their names are unbounded.
func (s *Store) Names() ([]FileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored, err := s.stored()
	if err != nil {
		return nil, err
	}
	inPrompt := make(map[string]bool)
	for _, name := range s.selected(promptBudget) {
		inPrompt[name] = true
	}

	seen := make(map[string]bool, len(stored))
	infos := make([]FileInfo, 0, len(stored)+len(rootFiles))
	add := func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		info := FileInfo{Name: name, InPrompt: inPrompt[name]}
		if stat, err := os.Stat(s.path(name)); err == nil {
			info.Present, info.Size, info.Modified = true, int(stat.Size()), stat.ModTime()
		}
		infos = append(infos, info)
	}
	for _, name := range rootFiles {
		add(name)
	}
	for _, name := range stored {
		add(name)
	}
	return infos, nil
}

// IsEmpty reports whether no meaningful memory exists — scaffolded but unfilled files count as empty.
func (s *Store) IsEmpty() bool { return s.FullBlock() == "" }

// PromptBlock renders memory bounded by the prompt budget: whole files in
// priority order until the budget is reached.
func (s *Store) PromptBlock() string { return s.render(promptBudget) }

// FullBlock renders all memory uncapped — for the curator, which must see
// everything to consolidate across files.
func (s *Store) FullBlock() string { return s.render(0) }

func (s *Store) render(budget int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out strings.Builder
	for _, name := range s.selected(budget) {
		content, err := os.ReadFile(s.path(name))
		if err != nil {
			continue
		}
		fmt.Fprintf(&out, "## %s\n%s\n\n", name, meaningful(string(content)))
	}
	return strings.TrimSpace(out.String())
}

// selected returns the files that fit the budget, in priority order
// (budget <= 0 is uncapped). Callers hold the lock.
func (s *Store) selected(budget int) []string {
	names, err := s.stored()
	if err != nil {
		return nil
	}
	var chosen []string
	size := 0
	for _, name := range names {
		content, err := os.ReadFile(s.path(name))
		if err != nil {
			continue
		}
		text := meaningful(string(content))
		if text == "" {
			continue
		}
		section := len(name) + len(text) + 8
		if budget > 0 && size > 0 && size+section > budget {
			break
		}
		size += section
		chosen = append(chosen, name)
	}
	return chosen
}

// writeAtomic writes through a temporary file in the same directory and renames
// it into place, so an interrupted process can never leave a truncated file.
func writeAtomic(path, content string) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())

	if err := temp.Chmod(filePerm); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.WriteString(content); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

func invalidName(name string) error {
	return fmt.Errorf("invalid memory file %q (allowed: USER.md, STACK.md, SKILLS/<topic>.md)", name)
}
