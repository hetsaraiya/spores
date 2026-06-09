// Package memorystore persists the agent's long-term memory as markdown
// files on local disk (direct storage; an S3-backed store can replace this
// later). Layout inside the root directory:
//
//	COMPANY.md       what the company is
//	PRODUCT.md       what the product is
//	STACK.md         what stack is used and preferred
//	SKILLS/<topic>.md   one file per learned skill/preference
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

// rootFiles are the fixed top-level memory files, in prompt order.
var rootFiles = []string{"COMPANY.md", "PRODUCT.md", "STACK.md"}

// validName matches the allowed memory file names: the fixed root files or
// SKILLS/<topic>.md. Anything else (absolute paths, traversal, other dirs)
// is rejected.
var validName = regexp.MustCompile(`^(COMPANY\.md|PRODUCT\.md|STACK\.md|SKILLS/[A-Za-z0-9][A-Za-z0-9._ -]*\.md)$`)

type Store struct {
	dir string
	mu  sync.Mutex
}

// New creates the memory directory (including SKILLS/) if needed.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "SKILLS"), 0o755); err != nil {
		return nil, fmt.Errorf("create memory dir: %w", err)
	}
	return &Store{dir: dir}, nil
}

// Dir returns the root directory of the store.
func (s *Store) Dir() string { return s.dir }

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
	entries, err := os.ReadDir(filepath.Join(s.dir, "SKILLS"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	var skills []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			skills = append(skills, "SKILLS/"+e.Name())
		}
	}
	sort.Strings(skills)
	return append(out, skills...), nil
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
		return fmt.Errorf("invalid memory file name %q (allowed: COMPANY.md, PRODUCT.md, STACK.md, SKILLS/<topic>.md)", name)
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

// IsEmpty reports whether no memory has been stored yet.
func (s *Store) IsEmpty() bool {
	files, err := s.Files()
	return err == nil && len(files) == 0
}

// PromptBlock renders all memory files as one markdown block for injection
// into a system prompt. Returns "" when the store is empty.
func (s *Store) PromptBlock() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := s.files()
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, name := range files {
		content, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil || strings.TrimSpace(string(content)) == "" {
			continue
		}
		fmt.Fprintf(&b, "## %s\n%s\n\n", name, strings.TrimSpace(string(content)))
	}
	return strings.TrimSpace(b.String())
}
