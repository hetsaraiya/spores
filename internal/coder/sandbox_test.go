package coder

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	e2b "github.com/matiasinsaurralde/go-e2b"
)

type recordingCommands struct {
	ctx         context.Context
	optionCount int
}

type recordingE2BClient struct {
	ctx    context.Context
	config e2b.SandboxConfig
}

func (r *recordingE2BClient) NewSandbox(ctx context.Context, configs ...e2b.SandboxConfig) (*e2b.Sandbox, error) {
	r.ctx = ctx
	if len(configs) > 0 {
		r.config = configs[0]
	}
	return &e2b.Sandbox{}, nil
}

func TestNewSandboxDoesNotSetLifetime(t *testing.T) {
	client := &recordingE2BClient{}
	original := makeE2BClient
	makeE2BClient = func(e2b.ClientConfig) (e2bClient, error) { return client, nil }
	t.Cleanup(func() { makeE2BClient = original })

	ctx := context.Background()
	if _, err := newSandbox(ctx, "key", "template", nil); err != nil {
		t.Fatalf("newSandbox: %v", err)
	}
	if client.ctx != ctx {
		t.Fatal("sandbox creation did not receive the caller context")
	}
	if client.config.Timeout != 0 {
		t.Fatalf("sandbox lifetime = %d, want unset", client.config.Timeout)
	}
}

func (r *recordingCommands) Run(ctx context.Context, _ string, options ...e2b.RunOption) (*e2b.CommandResult, error) {
	r.ctx = ctx
	r.optionCount = len(options)
	return &e2b.CommandResult{}, nil
}

func TestRunUsesCallerContextWithoutCommandTimeoutOptions(t *testing.T) {
	type contextKey string
	const key contextKey = "command"
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key, "caller"))
	cancel()
	commands := &recordingCommands{}
	box := &sandbox{ctx: ctx, commands: commands}

	if _, _, err := box.run("sleep 1"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if commands.ctx.Value(key) != "caller" {
		t.Fatal("command did not receive the caller context")
	}
	if _, ok := commands.ctx.Deadline(); ok {
		t.Fatal("command context has an application deadline")
	}
	if !errors.Is(commands.ctx.Err(), context.Canceled) {
		t.Fatalf("command context error = %v, want caller cancellation", commands.ctx.Err())
	}
	if commands.optionCount != 0 {
		t.Fatalf("command received %d E2B options, want none", commands.optionCount)
	}
}

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
