package memory

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
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
		"notes.txt", "SKILLS/../../etc/passwd", "../USER.md", "SKILLS/nested/deep.md",
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

func TestWriteAllowsAdditionalMarkdownFiles(t *testing.T) {
	store := newStore(t)
	for _, name := range []string{
		"PROJECTS.md",
		"profile/01-identity-and-contact.md",
		"SKILLS/deploy.md",
	} {
		if !mustWrite(t, store, name, "- Durable memory.\n") {
			t.Errorf("%s was not written", name)
		}
		content, err := store.Read(name)
		if err != nil || !strings.Contains(content, "Durable memory") {
			t.Errorf("Read(%q): content=%q err=%v", name, content, err)
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

// A scaffolded file is "empty" to meaningful(), which used to make it
// undeletable through Write's no-op check.
func TestDeleteRemovesFiles(t *testing.T) {
	cases := map[string]struct {
		name    string
		seed    string
		remove  func(*Store, string) (bool, error)
		changed bool
	}{
		"written file":  {"SKILLS/deploy.md", "- Redeploy via the deploy hook.\n", (*Store).Delete, true},
		"scaffolded":    {userFile, "", (*Store).Delete, true},
		"missing file":  {"SKILLS/absent.md", "", (*Store).Delete, false},
		"blank content": {"SKILLS/deploy.md", "- Redeploy.\n", func(s *Store, name string) (bool, error) { return s.Write(name, "   \n") }, true},
	}
	for label, test := range cases {
		t.Run(label, func(t *testing.T) {
			store := newStore(t)
			if test.seed != "" {
				mustWrite(t, store, test.name, test.seed)
			}
			changed, err := test.remove(store, test.name)
			if err != nil {
				t.Fatalf("remove: %v", err)
			}
			if changed != test.changed {
				t.Fatalf("changed=%t, want %t", changed, test.changed)
			}
			if _, err := os.Stat(store.path(test.name)); !os.IsNotExist(err) {
				t.Fatal("file survived removal")
			}
		})
	}
}

// The budget is a hard cap. Lower-priority files are whole or absent; only a
// first file that alone exceeds the budget is truncated, since dropping it
// would silently discard USER.md.
func TestPromptBudgetIsAHardCap(t *testing.T) {
	cases := map[string]struct {
		user, skill   string
		wantSkill     bool
		wantTruncated bool
	}{
		"both fit":            {strings.Repeat("u", 3000), strings.Repeat("s", 2000), true, false},
		"skill over budget":   {strings.Repeat("u", 5000), strings.Repeat("s", 2000), false, false},
		"first file oversize": {strings.Repeat("u", maxFileBytes-1), "", false, true},
	}
	for label, test := range cases {
		t.Run(label, func(t *testing.T) {
			store := newStore(t)
			mustWrite(t, store, userFile, test.user)
			if test.skill != "" {
				mustWrite(t, store, "SKILLS/testing.md", test.skill)
			}

			prompt := store.PromptBlock()
			if len(prompt) > promptBudget {
				t.Fatalf("prompt is %d bytes, over the %d-byte budget", len(prompt), promptBudget)
			}
			if !strings.Contains(prompt, userFile) {
				t.Fatal("highest-priority file dropped from the prompt")
			}
			if got := strings.Contains(prompt, "SKILLS/testing.md"); got != test.wantSkill {
				t.Fatalf("skill in prompt = %t, want %t", got, test.wantSkill)
			}
			if got := strings.Contains(prompt, truncationMarker); got != test.wantTruncated {
				t.Fatalf("truncated = %t, want %t", got, test.wantTruncated)
			}
			if test.skill != "" && !strings.Contains(store.FullBlock(), "SKILLS/testing.md") {
				t.Fatal("FullBlock must stay uncapped for the curator")
			}
		})
	}
}

func TestNamesListsRootAndAdditionalFiles(t *testing.T) {
	store := newStore(t)
	mustWrite(t, store, "SKILLS/testing.md", "- Run go test ./... before pushing.\n")
	mustWrite(t, store, "profile/identity.md", "- Personal profile.\n")

	infos, err := store.Names()
	if err != nil {
		t.Fatalf("Names: %v", err)
	}
	found := make(map[string]FileInfo, len(infos))
	for _, info := range infos {
		found[info.Name] = info
	}
	for _, name := range append(rootFiles, "SKILLS/testing.md", "profile/identity.md") {
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

// Reads run *while* writes are in flight. Reading only after the writers finish
// cannot observe a torn file, so a non-atomic write would pass unnoticed.
func TestWriteIsAtomicUnderConcurrency(t *testing.T) {
	const target = "SKILLS/notes.md" // unscaffolded, so any partial read is torn
	store := newStore(t)
	done := make(chan struct{})

	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			content, err := os.ReadFile(store.path(target))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				t.Errorf("concurrent read: %v", err)
				return
			}
			// Every writer writes whole "line\n" units, so any reader that sees a
			// partial unit has caught a torn write.
			if len(content)%5 != 0 || !strings.HasSuffix(string(content), "line\n") {
				t.Errorf("torn read: %d bytes ending %q", len(content), tail(string(content)))
				return
			}
		}
	}()

	var writers sync.WaitGroup
	for i := range 20 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			if _, err := store.Write(target, strings.Repeat("line\n", 200+i)); err != nil {
				t.Errorf("concurrent write: %v", err)
			}
		}()
	}
	writers.Wait()
	close(done)
	readers.Wait()

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

func TestTruncateBytesNeverSplitsARune(t *testing.T) {
	const text = "héllo wörld"
	for limit := range len(text) + 2 {
		got := truncateBytes(text, limit)
		if !utf8.ValidString(got) {
			t.Fatalf("limit %d produced invalid UTF-8: %q", limit, got)
		}
		if len(got) > limit {
			t.Fatalf("limit %d produced %d bytes", limit, len(got))
		}
	}
}
