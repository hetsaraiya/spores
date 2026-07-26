package memory

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// history keeps revisions of the memory directory in git. A dedicated revision
// format, prune policy, and restore API would all be reimplementations of what
// git already does for a directory this small — and `git -C <dir> log -p` is a
// better review tool than any UI worth building here.
type history struct {
	dir     string
	enabled bool
}

func newHistory(dir string) *history {
	h := &history{dir: dir}
	if _, err := exec.LookPath("git"); err != nil {
		log.Printf("memory: git not found, revision history disabled")
		return h
	}
	if err := h.init(); err != nil {
		log.Printf("memory: revision history disabled: %v", err)
		return h
	}
	h.enabled = true
	return h
}

func (h *history) init() error {
	if _, err := os.Stat(filepath.Join(h.dir, ".git")); err == nil {
		return nil
	}
	if _, err := h.git("init", "--quiet"); err != nil {
		return err
	}
	if _, err := h.git("config", "user.name", "spores-memory"); err != nil {
		return err
	}
	_, err := h.git("config", "user.email", "memory@spores.local")
	return err
}

// commit records the current state. Failures are logged and swallowed: losing a
// revision is not a reason to fail the write that already landed on disk.
func (h *history) commit(message string) {
	if !h.enabled {
		return
	}
	if _, err := h.git("add", "--all"); err != nil {
		log.Printf("memory: stage revision: %v", err)
		return
	}
	// Nothing staged means nothing changed on disk; not an error worth logging.
	if _, err := h.git("diff", "--cached", "--quiet"); err == nil {
		return
	}
	if out, err := h.git("commit", "--quiet", "--message", message); err != nil {
		log.Printf("memory: commit revision: %v %s", err, out)
	}
}

func (h *history) git(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", append([]string{"-C", h.dir}, args...)...)
	// Keep the ambient environment out of it: a stray GIT_DIR or commit hook
	// config on the host must not reach the memory repo.
	command.Env = []string{"HOME=" + h.dir, "PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1"}
	out, err := command.CombinedOutput()
	return string(out), err
}
