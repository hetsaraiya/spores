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

func TestWriteReadLoad(t *testing.T) {
	s := newTestStore(t)
	if !s.IsEmpty() {
		t.Fatal("fresh store should be empty")
	}
	if err := s.Write("COMPANY.md", "We build widgets."); err != nil {
		t.Fatal(err)
	}
	if err := s.Write("SKILLS/golang.md", "Prefers table-driven tests."); err != nil {
		t.Fatal(err)
	}
	if s.IsEmpty() {
		t.Error("store with files reported empty")
	}
	got, err := s.Read("COMPANY.md")
	if err != nil || got != "We build widgets." {
		t.Errorf("Read = (%q, %v)", got, err)
	}
	files, err := s.Files()
	if err != nil || len(files) != 2 || files[0] != "COMPANY.md" || files[1] != "SKILLS/golang.md" {
		t.Errorf("Files = (%v, %v)", files, err)
	}
}

func TestWriteRejectsInvalidNames(t *testing.T) {
	s := newTestStore(t)
	bad := []string{
		"../escape.md",
		"/etc/passwd",
		"COMPANY.txt",
		"SKILLS/../../escape.md",
		"SKILLS/nested/deep.md",
		"OTHER.md",
		"SKILLS/.hidden.md",
	}
	for _, name := range bad {
		if err := s.Write(name, "x"); err == nil {
			t.Errorf("Write(%q) accepted an invalid name", name)
		}
	}
}

func TestWriteEmptyDeletes(t *testing.T) {
	s := newTestStore(t)
	if err := s.Write("STACK.md", "Go + E2B"); err != nil {
		t.Fatal(err)
	}
	if err := s.Write("STACK.md", "  "); err != nil {
		t.Fatal(err)
	}
	if !s.IsEmpty() {
		t.Error("file was not deleted by empty write")
	}
	if err := s.Write("PRODUCT.md", ""); err != nil {
		t.Errorf("deleting a missing file should not error: %v", err)
	}
}

func TestChanged(t *testing.T) {
	s := newTestStore(t)
	// A missing file "changes" only when there is real content to add.
	if !s.Changed("STACK.md", "Go + E2B") {
		t.Error("new file with content should count as changed")
	}
	if s.Changed("STACK.md", "   ") {
		t.Error("empty content for a missing file should not count as changed")
	}
	if s.Changed("COMPANY.md", "<!-- guidance only -->") {
		t.Error("comment-only content for a missing file should not count as changed")
	}

	if err := s.Write("STACK.md", "Go + E2B"); err != nil {
		t.Fatal(err)
	}
	// Whitespace- or comment-only differences are not real changes.
	if s.Changed("STACK.md", "  Go + E2B  ") {
		t.Error("whitespace-only difference should not count as changed")
	}
	if s.Changed("STACK.md", "<!-- note -->Go + E2B") {
		t.Error("comment-only difference should not count as changed")
	}
	// Substantive changes and deletions do count.
	if !s.Changed("STACK.md", "Go + E2B + Postgres") {
		t.Error("substantive difference should count as changed")
	}
	if !s.Changed("STACK.md", "") {
		t.Error("deleting existing content should count as changed")
	}
}

func TestPromptBlock(t *testing.T) {
	s := newTestStore(t)
	if got := s.PromptBlock(); got != "" {
		t.Errorf("empty store PromptBlock = %q", got)
	}
	_ = s.Write("PRODUCT.md", "A Slack coding bot.")
	_ = s.Write("SKILLS/cloud.md", "Prefers AWS.")
	block := s.PromptBlock()
	if !strings.Contains(block, "## PRODUCT.md\nA Slack coding bot.") ||
		!strings.Contains(block, "## SKILLS/cloud.md\nPrefers AWS.") {
		t.Errorf("PromptBlock missing content:\n%s", block)
	}
}

func TestUserAndRepoScopes(t *testing.T) {
	s := newTestStore(t)
	for _, name := range []string{"USER.md", "STACK.md", "REPOS/acme-web.md"} {
		if err := s.Write(name, "x"); err != nil {
			t.Errorf("Write(%q) rejected a valid name: %v", name, err)
		}
	}
	_ = s.Write("SKILLS/go.md", "y")
	// Order: root files (rootFiles order), then SKILLS, then REPOS.
	files, _ := s.Files()
	want := []string{"USER.md", "STACK.md", "SKILLS/go.md", "REPOS/acme-web.md"}
	if strings.Join(files, ",") != strings.Join(want, ",") {
		t.Errorf("Files order = %v, want %v", files, want)
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
