package memory

import (
	"os/exec"
	"strings"
	"testing"
)

func TestHistoryCommitsEveryRevision(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	store := newStore(t)
	if !store.HistoryEnabled() {
		t.Fatal("history was not enabled despite git being present")
	}

	mustWrite(t, store, "STACK.md", "- Prefer pnpm over npm.\n")
	mustWrite(t, store, "STACK.md", "- Prefer pnpm over npm.\n- Deploys are containers.\n")
	mustWrite(t, store, "SKILLS/deploy.md", "- Redeploy via the deploy hook.\n")
	if _, err := store.Delete("SKILLS/deploy.md"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	log, err := store.history.git("log", "--format=%s")
	if err != nil {
		t.Fatalf("git log: %v %s", err, log)
	}
	if count := len(strings.Split(strings.TrimSpace(log), "\n")); count != 4 {
		t.Fatalf("expected one revision per write, got %d: %q", count, log)
	}
	if !strings.Contains(log, "delete SKILLS/deploy.md") {
		t.Errorf("delete was not recorded: %q", log)
	}

	// The point of history: a superseded revision is still recoverable.
	revisions, err := store.history.git("log", "--format=%H", "--", "STACK.md")
	if err != nil {
		t.Fatalf("git log STACK.md: %v %s", err, revisions)
	}
	commits := strings.Fields(revisions)
	if len(commits) != 2 {
		t.Fatalf("expected 2 revisions of STACK.md, got %d", len(commits))
	}
	original, err := store.history.git("show", commits[len(commits)-1]+":STACK.md")
	if err != nil {
		t.Fatalf("git show: %v %s", err, original)
	}
	if !strings.Contains(original, "pnpm") || strings.Contains(original, "containers") {
		t.Fatalf("oldest revision is not the pre-update content: %q", original)
	}
}

func TestHistoryIgnoresNoOpWrites(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	store := newStore(t)
	mustWrite(t, store, "STACK.md", "- Prefer pnpm over npm.\n")
	before, _ := store.history.git("rev-list", "--count", "HEAD")

	mustWrite(t, store, "STACK.md", "\n- Prefer pnpm over npm.\n<!-- note -->")
	after, _ := store.history.git("rev-list", "--count", "HEAD")

	if before != after {
		t.Fatalf("a no-op write created a revision: %s -> %s", strings.TrimSpace(before), strings.TrimSpace(after))
	}
}
