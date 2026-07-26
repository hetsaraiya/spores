package memory

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return store
}

func mustWrite(t *testing.T, store *Store, name, content string) bool {
	t.Helper()
	changed, err := store.Write(name, content)
	if err != nil {
		t.Fatalf("Write(%q): %v", name, err)
	}
	return changed
}

func TestWriteReadRoundTrip(t *testing.T) {
	store := newStore(t)
	if !mustWrite(t, store, "STACK.md", "- Go for backend services.\n") {
		t.Fatal("first write reported no change")
	}
	content, err := store.Read("STACK.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if content != "- Go for backend services.\n" {
		t.Fatalf("round trip mismatch: %q", content)
	}
	if store.FullBlock() == "" {
		t.Fatal("stored content missing from FullBlock")
	}
}

func TestWriteIsNoOpWhenOnlyFormattingChanges(t *testing.T) {
	store := newStore(t)
	mustWrite(t, store, "STACK.md", "- Go for backend services.")
	if mustWrite(t, store, "STACK.md", "\n- Go for backend services.\n\n<!-- a note -->") {
		t.Fatal("whitespace- and comment-only diff reported as a change")
	}
}

func TestScaffoldedFilesAreEmpty(t *testing.T) {
	store := newStore(t)
	for _, name := range rootFiles {
		if _, err := os.Stat(store.path(name)); err != nil {
			t.Fatalf("%s was not scaffolded: %v", name, err)
		}
	}
	if !store.IsEmpty() {
		t.Fatalf("scaffolded store reports content: %q", store.FullBlock())
	}
	if store.PromptSection() != "" {
		t.Fatal("scaffolded store injected a prompt section")
	}
}

func TestWriteRejectsInvalidNames(t *testing.T) {
	store := newStore(t)
	for _, name := range []string{
		"COMPANY.md", "SKILLS/../../etc/passwd", "../USER.md", "SKILLS/nested/deep.md",
		"USER.md.bak", "SKILLS/.hidden.md", "", "SKILLS/", "/etc/passwd",
	} {
		if _, err := store.Write(name, "content"); err == nil {
			t.Errorf("Write(%q) was accepted", name)
		}
		if _, err := store.Read(name); err == nil {
			t.Errorf("Read(%q) was accepted", name)
		}
	}
}

func TestWriteRejectsInvalidUTF8(t *testing.T) {
	store := newStore(t)
	if _, err := store.Write("STACK.md", "prefix \xff\xfe suffix"); err == nil {
		t.Fatal("invalid UTF-8 was accepted")
	}
}

func TestWriteRejectsOversizedContent(t *testing.T) {
	store := newStore(t)
	if _, err := store.Write("STACK.md", strings.Repeat("a", maxFileBytes+1)); err == nil {
		t.Fatal("oversized content was accepted")
	}
}

func TestWriteRejectsSecrets(t *testing.T) {
	store := newStore(t)
	secrets := map[string]string{
		"github token":  "Use ghp_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6 for the API.",
		"github pat":    "token github_pat_11ABCDEFG0abcdefghijklmnopqrstuvwxyz012345",
		"openai key":    "OPENAI_API_KEY=sk-proj-abc123def456ghi789jkl",
		"slack token":   "xoxb-123456789012-abcdefghijkl",
		"aws key":       "AKIAIOSFODNN7EXAMPLE",
		"private key":   "-----BEGIN RSA PRIVATE KEY-----\nMIIEow==\n-----END RSA PRIVATE KEY-----",
		"assigned pass": "password: hunter2Correct99",
		"bearer header": "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6",
	}
	for label, content := range secrets {
		if _, err := store.Write("STACK.md", content); err == nil {
			t.Errorf("%s was stored", label)
		}
	}
}

func TestWriteAllowsProseMentioningCredentials(t *testing.T) {
	store := newStore(t)
	clean := []string{
		"- A GitHub token is required for private repos; keep it in the environment.\n",
		"- Auth is token-based: prefer short-lived credentials.\n",
		"- The deploy secret lives in the CI settings, never in the repo.\n",
		"- Set MEMORY_UPDATE_MODE=always during development.\n",
	}
	for _, content := range clean {
		if _, err := store.Write("STACK.md", content); err != nil {
			t.Errorf("rejected clean prose %q: %v", content, err)
		}
	}
}

func TestDeleteRemovesFile(t *testing.T) {
	store := newStore(t)
	mustWrite(t, store, "SKILLS/deploy.md", "- Redeploy via the deploy hook.\n")
	changed, err := store.Delete("SKILLS/deploy.md")
	if err != nil || !changed {
		t.Fatalf("Delete: changed=%t err=%v", changed, err)
	}
	if _, err := os.Stat(store.path("SKILLS/deploy.md")); !os.IsNotExist(err) {
		t.Fatal("file survived delete")
	}
	if changed, _ := store.Delete("SKILLS/deploy.md"); changed {
		t.Fatal("deleting a missing file reported a change")
	}
}

func TestPromptBudgetDropsLowestPriorityFile(t *testing.T) {
	store := newStore(t)
	mustWrite(t, store, "USER.md", strings.Repeat("u", 5000))
	mustWrite(t, store, "SKILLS/testing.md", strings.Repeat("s", 2000))

	prompt := store.PromptBlock()
	if !strings.Contains(prompt, "USER.md") {
		t.Fatal("highest-priority file dropped from the prompt")
	}
	if strings.Contains(prompt, "SKILLS/testing.md") {
		t.Fatal("budget did not drop the lowest-priority file")
	}
	if full := store.FullBlock(); !strings.Contains(full, "SKILLS/testing.md") {
		t.Fatal("FullBlock must be uncapped for the curator")
	}
}

func TestNamesListsRootFilesAndSkills(t *testing.T) {
	store := newStore(t)
	mustWrite(t, store, "SKILLS/testing.md", "- Run go test ./... before pushing.\n")

	infos, err := store.Names()
	if err != nil {
		t.Fatalf("Names: %v", err)
	}
	found := make(map[string]FileInfo, len(infos))
	for _, info := range infos {
		found[info.Name] = info
	}
	for _, name := range append(rootFiles, "SKILLS/testing.md") {
		if _, ok := found[name]; !ok {
			t.Errorf("%s missing from Names()", name)
		}
	}
	if !found["SKILLS/testing.md"].Present || !found["SKILLS/testing.md"].InPrompt {
		t.Errorf("skill reported as absent or out of prompt: %+v", found["SKILLS/testing.md"])
	}
	// Scaffolded root files exist on disk but carry no meaningful content.
	if found["USER.md"].InPrompt {
		t.Error("scaffolded USER.md counted as in-prompt")
	}
}

func TestWriteIsAtomicUnderConcurrency(t *testing.T) {
	store := newStore(t)
	var group sync.WaitGroup
	for i := range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			content := strings.Repeat("line\n", 200+i)
			if _, err := store.Write("STACK.md", content); err != nil {
				t.Errorf("concurrent write: %v", err)
			}
		}()
	}
	group.Wait()

	content, err := os.ReadFile(store.path("STACK.md"))
	if err != nil {
		t.Fatalf("read after concurrent writes: %v", err)
	}
	// Every writer wrote whole "line\n" units, so a torn write shows up as a partial tail.
	if len(content)%5 != 0 || !strings.HasSuffix(string(content), "line\n") {
		t.Fatalf("torn write: %d bytes ending %q", len(content), tail(string(content)))
	}
	if entries, _ := filepath.Glob(filepath.Join(store.dir, ".tmp-*")); len(entries) != 0 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func tail(content string) string {
	if len(content) < 12 {
		return content
	}
	return content[len(content)-12:]
}
