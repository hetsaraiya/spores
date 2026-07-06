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

// Max chars of memory rendered into a prompt. Files render in priority order;
// once the budget is hit the rest stay on disk but drop out of the prompt.
const promptInjectionBudget = 6000

var scopedDirs = []string{"SKILLS", "REPOS"}

// Fixed top-level files, in prompt priority order — earliest survives budget truncation.
var rootFiles = []string{"USER.md", "STACK.md"}

// Allowed names: the root files, or SKILLS/<topic>.md / REPOS/<repo>.md. Blocks traversal and stray paths.
var validName = regexp.MustCompile(`^(USER\.md|STACK\.md|(SKILLS|REPOS)/[A-Za-z0-9][A-Za-z0-9._ -]*\.md)$`)

var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// meaningful is the content minus HTML-comment guidance and surrounding space — what actually reaches the agent.
func meaningful(content string) string {
	return strings.TrimSpace(htmlComment.ReplaceAllString(content, ""))
}

type Store struct {
	dir string
	mu  sync.Mutex
}

func New(dir string) (*Store, error) {
	for _, sub := range scopedDirs {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("create memory dir: %w", err)
		}
	}
	return &Store{dir: dir}, nil
}

func (s *Store) files() ([]string, error) {
	var out []string
	for _, name := range rootFiles {
		if _, err := os.Stat(filepath.Join(s.dir, name)); err == nil {
			out = append(out, name)
		}
	}
	// scopedDirs order (SKILLS before REPOS) puts per-repo memory last, so it drops first under budget.
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

// Write replaces one memory file; empty content deletes it. It reports whether
// the file meaningfully changed — whitespace/comment-only diffs are skipped, so
// re-emitting stored facts is a no-op that never touches disk.
func (s *Store) Write(name, content string) (bool, error) {
	if !validName.MatchString(name) {
		return false, fmt.Errorf("invalid memory file name %q (allowed: USER.md, STACK.md, SKILLS/<topic>.md, REPOS/<repo>.md)", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, name)
	existing, err := os.ReadFile(path)
	if err != nil {
		// Missing file: only meaningful content is a change worth writing.
		if meaningful(content) == "" {
			return false, nil
		}
	} else if meaningful(string(existing)) == meaningful(content) {
		return false, nil
	}
	if strings.TrimSpace(content) == "" {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			err = nil
		}
		return true, err
	}
	return true, os.WriteFile(path, []byte(content), 0o644)
}

// IsEmpty reports whether no meaningful memory exists — whitespace/comment-only files count as empty.
func (s *Store) IsEmpty() bool {
	return s.FullBlock() == ""
}

// PromptBlock renders memory for a prompt, bounded: whole files in priority order
// until promptInjectionBudget, then a note. Use FullBlock when nothing may be dropped.
func (s *Store) PromptBlock() string {
	return s.render(promptInjectionBudget)
}

// FullBlock renders all memory uncapped — for the maintenance agent, which must see everything.
func (s *Store) FullBlock() string {
	return s.render(0)
}

// render joins files as "## name\n<content>"; budget > 0 stops before the first overflowing file (budget <= 0 = uncapped).
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
