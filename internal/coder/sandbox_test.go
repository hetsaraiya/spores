package coder

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// quote is the shell-injection boundary: every value interpolated into a sandbox
// command goes through it. Each case is run through a real shell so the
// assertion is what bash actually does, not what the quoting looks like.
func TestQuoteSurvivesShellInterpretation(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	values := []string{
		"plain",
		"with space",
		"it's quoted",
		"'; rm -rf / #",
		"$(whoami)",
		"`whoami`",
		"${HOME}",
		`back\slash`,
		"new\nline",
		"semi;colon && chained || other",
		"pipe | redirect > file < in",
		"* ? [glob]",
		`"double"`,
		"emoji 🌱",
	}
	for _, value := range values {
		out, err := exec.Command("bash", "-lc", "printf %s "+quote(value)).Output()
		if err != nil {
			t.Errorf("quote(%q) produced an unrunnable command: %v", value, err)
			continue
		}
		if string(out) != value {
			t.Errorf("quote(%q) round-tripped as %q", value, string(out))
		}
	}
}

// Asserted by side effect: the payload's own text would appear in stdout either
// way, so only an actual file write proves the injected command ran.
func TestQuoteContainsInjectionAttempts(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	marker := filepath.Join(t.TempDir(), "pwned")
	payloads := []string{
		"x'; touch " + marker + "; #",
		"x$(touch " + marker + ")",
		"x`touch " + marker + "`",
	}
	for _, payload := range payloads {
		if _, err := exec.Command("bash", "-lc", "printf %s "+quote(payload)).Output(); err != nil {
			t.Fatalf("command failed: %v", err)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("injection escaped quoting: %q ran", payload)
		}
	}
}

func TestShortenCommandBoundsLogOutput(t *testing.T) {
	got := shortenCommand(strings.Repeat("a", maxLoggedCommand*2))
	if len(got) > maxLoggedCommand+len("... [truncated]") {
		t.Fatalf("logged command is %d bytes, over the bound", len(got))
	}

	short := "git status"
	if shortenCommand(short) != short {
		t.Fatal("a short command was altered")
	}
}

func TestCodexSpecPinsWhenVersionIsSet(t *testing.T) {
	cases := map[string]string{
		"":         codexPackage,
		"   ":      codexPackage,
		"1.2.3":    codexPackage + "@1.2.3",
		"  1.2.3 ": codexPackage + "@1.2.3",
		"latest":   codexPackage + "@latest",
	}
	for version, want := range cases {
		if got := codexSpec(version); got != want {
			t.Errorf("codexSpec(%q) = %q, want %q", version, got, want)
		}
	}
}

func TestCodexCommandSupportsRepositoryFreeResearch(t *testing.T) {
	command := codexCommand("")
	if !strings.Contains(command, " --search") {
		t.Fatalf("command omitted live search: %s", command)
	}
	if !strings.Contains(command, "--skip-git-repo-check") {
		t.Fatalf("repository-free execution was disabled: %s", command)
	}
}

// A refresh failure must not discard work the sandbox already did: the
// repository may have changed, so reporting nothing would be a lie.
func TestReportSeparatesRefreshFailureFromTheRun(t *testing.T) {
	failure := result{report: "  Opened PR #12.  ", refreshFailure: errors.New("read auth.json: no such file")}
	got := report(failure)
	if !strings.HasPrefix(got, "Opened PR #12.") {
		t.Fatalf("the report was discarded: %q", got)
	}
	if !strings.Contains(got, "warning") {
		t.Fatalf("the refresh failure was not disclosed: %q", got)
	}

	if got := report(result{report: " Done. "}); got != "Done." {
		t.Fatalf("a clean run was altered: %q", got)
	}
}
