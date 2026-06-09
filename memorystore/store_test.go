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

func TestScaffold(t *testing.T) {
	s := newTestStore(t)
	if err := s.Scaffold(); err != nil {
		t.Fatal(err)
	}
	files, err := s.Files()
	if err != nil || len(files) != 4 {
		t.Fatalf("Files after scaffold = (%v, %v), want 4 files", files, err)
	}
	if !s.IsEmpty() {
		t.Error("template-only files must count as empty memory")
	}
	if got := s.PromptBlock(); got != "" {
		t.Errorf("template-only PromptBlock = %q, want empty", got)
	}
	if err := s.Write("COMPANY.md", "We build widgets."); err != nil {
		t.Fatal(err)
	}
	if err := s.Scaffold(); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Read("COMPANY.md")
	if got != "We build widgets." {
		t.Error("Scaffold overwrote an existing file")
	}
	if s.IsEmpty() {
		t.Error("store with real content reported empty")
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
