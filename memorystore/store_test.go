package memorystore

import (
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestWriteAndLoad(t *testing.T) {
	s := newTestStore(t)
	if !s.IsEmpty() {
		t.Fatal("fresh store should be empty")
	}
	if _, err := s.Write("STACK.md", "Go + E2B."); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write("SKILLS/golang.md", "Prefers table-driven tests."); err != nil {
		t.Fatal(err)
	}
	if s.IsEmpty() {
		t.Error("store with files reported empty")
	}
	block := s.PromptBlock()
	if !strings.Contains(block, "Go + E2B.") || !strings.Contains(block, "SKILLS/golang.md") {
		t.Errorf("PromptBlock missing written content:\n%s", block)
	}
}

func TestWriteRejectsInvalidNames(t *testing.T) {
	s := newTestStore(t)
	bad := []string{
		"../escape.md",
		"/etc/passwd",
		"USER.txt",
		"COMPANY.md",
		"SKILLS/../../escape.md",
		"SKILLS/nested/deep.md",
		"OTHER.md",
		"SKILLS/.hidden.md",
	}
	for _, name := range bad {
		if _, err := s.Write(name, "x"); err == nil {
			t.Errorf("Write(%q) accepted an invalid name", name)
		}
	}
}

func TestWriteEmptyDeletes(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Write("STACK.md", "Go + E2B"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write("STACK.md", "  "); err != nil {
		t.Fatal(err)
	}
	if !s.IsEmpty() {
		t.Error("file was not deleted by empty write")
	}
	if _, err := s.Write("USER.md", ""); err != nil {
		t.Errorf("deleting a missing file should not error: %v", err)
	}
}

func TestWriteReportsChanged(t *testing.T) {
	s := newTestStore(t)
	changed := func(name, content string) bool {
		t.Helper()
		ch, err := s.Write(name, content)
		if err != nil {
			t.Fatalf("Write(%q) failed: %v", name, err)
		}
		return ch
	}
	// A missing file "changes" only when there is real content to add.
	if changed("STACK.md", "   ") {
		t.Error("empty content for a missing file should not count as changed")
	}
	if changed("USER.md", "<!-- guidance only -->") {
		t.Error("comment-only content for a missing file should not count as changed")
	}
	if !changed("STACK.md", "Go + E2B") {
		t.Error("new file with content should count as changed")
	}
	// Whitespace- or comment-only differences are not real changes.
	if changed("STACK.md", "  Go + E2B  ") {
		t.Error("whitespace-only difference should not count as changed")
	}
	if changed("STACK.md", "<!-- note -->Go + E2B") {
		t.Error("comment-only difference should not count as changed")
	}
	// Substantive changes and deletions do count.
	if !changed("STACK.md", "Go + E2B + Postgres") {
		t.Error("substantive difference should count as changed")
	}
	if !changed("STACK.md", "") {
		t.Error("deleting existing content should count as changed")
	}
}

func TestPromptBlock(t *testing.T) {
	s := newTestStore(t)
	if got := s.PromptBlock(); got != "" {
		t.Errorf("empty store PromptBlock = %q", got)
	}
	s.Write("USER.md", "Prefers concise replies.")
	s.Write("SKILLS/cloud.md", "Prefers AWS.")
	block := s.PromptBlock()
	if !strings.Contains(block, "## USER.md\nPrefers concise replies.") ||
		!strings.Contains(block, "## SKILLS/cloud.md\nPrefers AWS.") {
		t.Errorf("PromptBlock missing content:\n%s", block)
	}
}

func TestUserAndRepoScopes(t *testing.T) {
	s := newTestStore(t)
	for _, name := range []string{"USER.md", "STACK.md", "REPOS/acme-web.md"} {
		if _, err := s.Write(name, "x"); err != nil {
			t.Errorf("Write(%q) rejected a valid name: %v", name, err)
		}
	}
	s.Write("SKILLS/go.md", "y")
	block := s.FullBlock()
	for _, want := range []string{"USER.md", "STACK.md", "SKILLS/go.md", "REPOS/acme-web.md"} {
		if !strings.Contains(block, want) {
			t.Errorf("FullBlock missing %q:\n%s", want, block)
		}
	}
	idxUser := strings.Index(block, "USER.md")
	idxStack := strings.Index(block, "STACK.md")
	idxSkills := strings.Index(block, "SKILLS/go.md")
	idxRepos := strings.Index(block, "REPOS/acme-web.md")
	if !(idxUser < idxStack && idxStack < idxSkills && idxSkills < idxRepos) {
		t.Errorf("FullBlock file order wrong:\n%s", block)
	}
}

func TestPromptBudgetTruncates(t *testing.T) {
	s := newTestStore(t)
	s.Write("USER.md", "Prefers concise replies.")          // small, high priority
	s.Write("REPOS/big-repo.md", strings.Repeat("x", 8000)) // large, low priority

	full := s.FullBlock()
	if !strings.Contains(full, "USER.md") || !strings.Contains(full, "REPOS/big-repo.md") {
		t.Error("FullBlock must contain all files uncapped")
	}

	prompt := s.PromptBlock()
	if !strings.Contains(prompt, "USER.md") {
		t.Error("PromptBlock dropped a high-priority file")
	}
	if strings.Contains(prompt, strings.Repeat("x", 8000)) {
		t.Error("PromptBlock did not cap the oversized low-priority file")
	}
	if !strings.Contains(prompt, "omitted to stay within") {
		t.Error("PromptBlock missing the truncation note")
	}
	if len(prompt) > promptInjectionBudget {
		t.Errorf("PromptBlock length %d exceeds budget %d", len(prompt), promptInjectionBudget)
	}
}
