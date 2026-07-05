package memorystore

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// promptInjectionBudget caps how many characters of memory are rendered into a
// prompt. Files render in priority order (see files); once the budget is hit,
// remaining (lower-priority, e.g. per-repo) files are omitted from the prompt
// but stay on disk. This is the hard guarantee that memory never bloats context.
const promptInjectionBudget = 6000

// scopedDirs are the subdirectories that hold one-file-per-topic memory.
var scopedDirs = []string{"SKILLS", "REPOS"}

// rootFiles are the fixed top-level memory files, in prompt order (most broadly
// useful first, so if the injection budget truncates, the important ones stay).
var rootFiles = []string{"USER.md", "STACK.md", "COMPANY.md", "PRODUCT.md"}

// validName matches the allowed memory file names: the fixed root files, or
// SKILLS/<topic>.md and REPOS/<repo>.md. Anything else (absolute paths,
// traversal, other dirs) is rejected.
var validName = regexp.MustCompile(`^(USER\.md|STACK\.md|COMPANY\.md|PRODUCT\.md|(SKILLS|REPOS)/[A-Za-z0-9][A-Za-z0-9._ -]*\.md)$`)

// htmlComment matches HTML comments, so any guidance a user hand-writes into
// a memory file is hidden from prompt rendering.
var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// meaningful strips HTML comments and whitespace, returning the content that
// should actually reach the agent.
func meaningful(content string) string {
	return strings.TrimSpace(htmlComment.ReplaceAllString(content, ""))
}

type Store struct {
	dir string
	mu  sync.Mutex
}

// New creates the memory directory (including the scoped subdirs) if needed.
func New(dir string) (*Store, error) {
	for _, sub := range scopedDirs {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("create memory dir: %w", err)
		}
	}
	return &Store{dir: dir}, nil
}

// Dir returns the root directory of the store.
func (s *Store) Dir() string { return s.dir }

// Changed reports whether writing content to name would meaningfully change
// the stored file. Differences that are only whitespace or HTML-comment
// guidance don't count, so the memory agent re-emitting facts it already
// recorded is a no-op rather than a rewrite. A missing file "changes" only if
// there is real content to add; supplying empty content to delete an existing
// file counts as a change.
func (s *Store) Changed(name, content string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(filepath.Join(s.dir, name))
	if err != nil {
		return meaningful(content) != ""
	}
	return meaningful(string(b)) != meaningful(content)
}

// Files returns the names of all existing memory files, root files first,
// then skills sorted by name.
func (s *Store) Files() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.files()
}

func (s *Store) files() ([]string, error) {
	var out []string
	for _, name := range rootFiles {
		if _, err := os.Stat(filepath.Join(s.dir, name)); err == nil {
			out = append(out, name)
		}
	}
	// Scoped dirs follow the root files, each sorted by name, in scopedDirs
	// order (SKILLS before REPOS) so per-repo memory sorts last and is the
	// first to drop if the injection budget truncates.
	for _, sub := range scopedDirs {
		entries, err := os.ReadDir(filepath.Join(s.dir, sub))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		var scoped []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				scoped = append(scoped, sub+"/"+e.Name())
			}
		}
		sort.Strings(scoped)
		out = append(out, scoped...)
	}
	return out, nil
}

// Read returns the content of one memory file.
func (s *Store) Read(name string) (string, error) {
	if !validName.MatchString(name) {
		return "", fmt.Errorf("invalid memory file name %q", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(filepath.Join(s.dir, name))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Write replaces the content of one memory file. Writing empty content
// deletes the file.
func (s *Store) Write(name, content string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid memory file name %q (allowed: USER.md, STACK.md, COMPANY.md, PRODUCT.md, SKILLS/<topic>.md, REPOS/<repo>.md)", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, name)
	if strings.TrimSpace(content) == "" {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// IsEmpty reports whether no meaningful memory has been stored yet. Files that
// hold only whitespace or HTML-comment guidance count as empty.
func (s *Store) IsEmpty() bool {
	return s.FullBlock() == ""
}

// PromptBlock renders memory for injection into a prompt. It is BOUNDED: whole
// files are added in priority order until promptInjectionBudget is reached, then
// the rest are omitted (with a note) so memory can never bloat the context. Use
// FullBlock when the whole picture is needed (e.g. the memory-maintenance agent).
func (s *Store) PromptBlock() string {
	return s.render(promptInjectionBudget)
}

// FullBlock renders all memory with no budget cap. Used by the memory
// maintenance agent, which must see everything to consolidate and de-conflict.
func (s *Store) FullBlock() string {
	return s.render(0)
}

// render builds the memory block. When budget > 0, whole files are included in
// priority order only while the running length stays under budget; once a file
// would overflow, it stops and appends an omission note. budget <= 0 means no cap.
func (s *Store) render(budget int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := s.files()
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, name := range files {
		content, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			continue
		}
		text := meaningful(string(content))
		if text == "" {
			continue
		}
		section := fmt.Sprintf("## %s\n%s\n\n", name, text)
		if budget > 0 && b.Len() > 0 && b.Len()+len(section) > budget {
			b.WriteString("_(some lower-priority memory omitted to stay within the prompt budget)_\n")
			break
		}
		b.WriteString(section)
	}
	return strings.TrimSpace(b.String())
}
