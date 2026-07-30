// Package memory stores the agent's long-term personal memory as markdown on disk.
//
// USER.md and STACK.md have the highest prompt priority. Additional Markdown
// files may live at the root or in one category directory, such as
// PROFILE/identity.md or SKILLS/deploy.md.
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// promptBudget is a hard cap: files render in priority order, and one larger
	// than the budget is truncated rather than admitted whole.
	promptBudget = 6000

	maxFileBytes  = 16 << 10  // per file
	maxTotalBytes = 256 << 10 // across all files

	dirPerm  = 0o700
	filePerm = 0o600

	sectionFormat   = "## %s\n%s\n\n"
	sectionOverhead = len("## ") + len("\n") + len("\n\n")

	truncationMarker = "\n… [truncated to fit the prompt budget]"
	probeName        = ".writable"

	updateMessage = "update "
	deleteMessage = "delete "
)

const (
	userFile  = "USER.md"
	stackFile = "STACK.md"
	skillsDir = "SKILLS"
)

// rootFiles are the fixed files, in prompt priority order — the earliest
// survives budget truncation longest. SKILLS/* render after these and so drop first.
var rootFiles = []string{userFile, stackFile}

// validName permits Markdown files at the root or one directory deep. Requiring
// simple path components blocks absolute paths, traversal, and hidden files.
var validName = regexp.MustCompile(`^(?:[A-Za-z0-9][A-Za-z0-9._ -]*/)?[A-Za-z0-9][A-Za-z0-9._ -]*\.md$`)

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

// section is one file's prompt contribution, already read and budget-trimmed.
type section struct {
	name      string
	text      string
	truncated bool
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
	probe := filepath.Join(s.dir, probeName)
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

// Write replaces one memory file; blank content deletes it. It reports whether
// the file meaningfully changed — whitespace- and comment-only diffs are
// skipped, so re-emitting stored facts is a no-op that never touches disk.
func (s *Store) Write(name, content string) (bool, error) {
	if !validName.MatchString(name) {
		return false, invalidName(name)
	}
	// A comment-only file is "empty" to meaningful(), so deletes must bypass the
	// no-op check or such a file can never be removed.
	if strings.TrimSpace(content) == "" {
		return s.Delete(name)
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

	changed, err := s.store(name, content)
	if err != nil || !changed {
		return false, err
	}
	// Outside the lock: git is a subprocess and would stall prompt rendering.
	s.history.commit(updateMessage + name)
	return true, nil
}

// store performs the guarded portion of a write and reports whether disk changed.
func (s *Store) store(name, content string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.path(name)
	existing, readErr := os.ReadFile(path)
	if readErr == nil && meaningful(string(existing)) == meaningful(content) {
		return false, nil
	}
	if err := s.checkTotal(name, len(content)); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return false, fmt.Errorf("create memory category: %w", err)
	}
	if err := writeAtomic(path, content); err != nil {
		return false, err
	}
	return true, nil
}

// Delete removes a memory file, including one holding only scaffold guidance.
func (s *Store) Delete(name string) (bool, error) {
	if !validName.MatchString(name) {
		return false, invalidName(name)
	}
	s.mu.Lock()
	err := os.Remove(s.path(name))
	s.mu.Unlock()

	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	s.history.commit(deleteMessage + name)
	return true, nil
}

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
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var additional []string
	for _, entry := range entries {
		if entry.IsDir() {
			children, err := os.ReadDir(filepath.Join(s.dir, entry.Name()))
			if err != nil {
				continue
			}
			for _, child := range children {
				name := entry.Name() + "/" + child.Name()
				if !child.IsDir() && validName.MatchString(name) {
					additional = append(additional, name)
				}
			}
			continue
		}
		if validName.MatchString(entry.Name()) && !slices.Contains(rootFiles, entry.Name()) {
			additional = append(additional, entry.Name())
		}
	}
	sort.Strings(additional)
	return append(names, additional...), nil
}

// Names lists the memory files for the portal. USER.md and STACK.md always
// appear so they can be created; additional files appear once they exist.
func (s *Store) Names() ([]FileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored, err := s.stored()
	if err != nil {
		return nil, err
	}
	inPrompt := make(map[string]bool)
	for _, chosen := range s.sections(promptBudget) {
		inPrompt[chosen.name] = true
	}

	// stored() excludes root files from its additional list, so no dedupe needed.
	ordered := make([]string, 0, len(rootFiles)+len(stored))
	ordered = append(ordered, rootFiles...)
	for _, name := range stored {
		if !slices.Contains(rootFiles, name) {
			ordered = append(ordered, name)
		}
	}

	infos := make([]FileInfo, 0, len(ordered))
	for _, name := range ordered {
		info := FileInfo{Name: name, InPrompt: inPrompt[name]}
		if stat, err := os.Stat(s.path(name)); err == nil {
			info.Present, info.Size, info.Modified = true, int(stat.Size()), stat.ModTime()
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// IsEmpty reports whether no meaningful memory exists — scaffolded but unfilled
// files count as empty.
func (s *Store) IsEmpty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	names, err := s.stored()
	if err != nil {
		return true
	}
	for _, name := range names {
		content, err := os.ReadFile(s.path(name))
		if err != nil {
			continue
		}
		if meaningful(string(content)) != "" {
			return false
		}
	}
	return true
}

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
	for _, chosen := range s.sections(budget) {
		text := chosen.text
		if chosen.truncated {
			text += truncationMarker
		}
		fmt.Fprintf(&out, sectionFormat, chosen.name, text)
	}
	return strings.TrimSpace(out.String())
}

// sections returns the files fitting the budget with the text to render for
// each, so rendering never re-reads a file (budget <= 0 is uncapped). Files are
// whole or absent, except a first file that alone exceeds the budget: dropping
// it would silently discard USER.md, so it is truncated. Callers hold the lock.
func (s *Store) sections(budget int) []section {
	names, err := s.stored()
	if err != nil {
		return nil
	}
	var chosen []section
	used := 0
	for _, name := range names {
		content, err := os.ReadFile(s.path(name))
		if err != nil {
			continue
		}
		text := meaningful(string(content))
		if text == "" {
			continue
		}
		if budget <= 0 {
			chosen = append(chosen, section{name: name, text: text})
			continue
		}

		remaining := budget - used - sectionOverhead - len(name)
		if remaining <= 0 {
			break
		}
		truncated := false
		if len(text) > remaining {
			if len(chosen) > 0 {
				break
			}
			// The marker is rendered too, so it comes out of the same budget.
			text = truncateBytes(text, remaining-len(truncationMarker))
			if text == "" {
				break
			}
			truncated = true
		}
		chosen = append(chosen, section{name: name, text: text, truncated: truncated})
		used += sectionOverhead + len(name) + len(text)
		if truncated {
			break
		}
	}
	return chosen
}

// truncateBytes cuts text to at most limit bytes without splitting a rune.
func truncateBytes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
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
	return fmt.Errorf("invalid memory file %q (use a .md file at the root or one category deep, such as PROFILE/identity.md)", name)
}
