package agent

import (
	"context"
	"os"
	"strings"

	"spore/config"
	"spore/githubclient"
	"spore/sandbox"
)

// Git identity used for the commits the coding agent makes inside the sandbox.
const (
	gitUserName  = "Slack Agent"
	gitUserEmail = "bot@agent.dev"
)

type Agent struct {
	github      *githubclient.Client
	e2bKey      string
	e2bTemplate string
	codexModel  string
	codexAuth   string
	openAIKey   string
}

type StatusFunc func(string)

type statusKey struct{}

func New(gh *githubclient.Client, cfg *config.Config) *Agent {
	return &Agent{
		github:      gh,
		e2bKey:      cfg.E2BAPIKey,
		e2bTemplate: cfg.E2BTemplateID,
		codexModel:  cfg.CodexModel,
		codexAuth:   cfg.CodexAuthJSON,
		openAIKey:   cfg.OpenAIAPIKey,
	}
}

func WithStatus(ctx context.Context, fn StatusFunc) context.Context {
	return context.WithValue(ctx, statusKey{}, fn)
}

// Run stands up an authenticated sandbox and hands the entire job to a single
// Codex session. That one session reads the request, opens (or reuses) the
// GitHub issue, clones, implements, commits, pushes, and opens the pull request
// itself, then returns a Slack-ready report. The Go side is only the harness:
// it prepares auth and relays whatever the agent says back to the caller.
func (a *Agent) Run(ctx context.Context, message string) (string, error) {
	emit(ctx, "1/3 Starting E2B sandbox...")
	sb, err := a.spinSandbox(ctx)
	if err != nil {
		return "", fail(1, err)
	}
	defer func() { _ = sb.Close() }()
	if err = sb.ProbeIO(); err != nil {
		return "", fail(1, err)
	}
	if err = sb.SetupCodexAuth(a.codexAuth, a.openAIKey); err != nil {
		return "", fail(1, err)
	}
	if err = sb.SetupGitAuth(a.github.CredentialsLine()); err != nil {
		return "", fail(1, err)
	}
	if err = sb.SetupGitHub(a.github.Token(), gitUserName, gitUserEmail); err != nil {
		return "", fail(1, err)
	}

	emit(ctx, "2/3 Coding agent is running the task...")
	out, err := sb.RunCodex("/home/user", a.codexModel, message)
	out = strings.TrimSpace(out)
	if err != nil {
		// Return whatever the agent managed to say so the reporter still has
		// context (which issue/PR, if any, it created) rather than a bare error.
		return out, fail(2, err)
	}
	emit(ctx, "3/3 Coding agent finished.")
	return out, nil
}

func (a *Agent) spinSandbox(ctx context.Context) (*sandbox.Sandbox, error) {
	return sandbox.New(ctx, a.e2bKey, a.e2bTemplate, os.Stdout)
}

func emit(ctx context.Context, msg string) {
	if fn, ok := ctx.Value(statusKey{}).(StatusFunc); ok {
		fn(msg)
	}
}

// Emit reports a progress message through the status func set with WithStatus.
// Exported so the router can share the same status channel.
func Emit(ctx context.Context, msg string) {
	emit(ctx, msg)
}
