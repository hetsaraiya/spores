package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"spore/startup/config"
	"spore/githubclient"
	"spore/sandbox"
)

// Git identity used for the commits the coding agent makes inside the sandbox.
const (
	gitUserName  = "Slack Agent"
	gitUserEmail = "bot@agent.dev"
)

type Agent struct {
	github *githubclient.Client
	cfg    *config.Config
}

func New(gh *githubclient.Client, cfg *config.Config) *Agent {
	return &Agent{github: gh, cfg: cfg}
}

// Run stands up an authenticated sandbox and hands the whole job to one Codex
// session (clone → implement → commit → push → optional PR/issue → report).
// The Go side is just the harness: prepare auth, relay the agent's report.
func (a *Agent) Run(ctx context.Context, message string) (string, error) {
	log.Print("1/3 Starting E2B sandbox...")
	sb, err := sandbox.New(ctx, a.cfg.E2BAPIKey, a.cfg.E2BTemplateID, os.Stdout)
	if err != nil {
		return "", fmt.Errorf("coding agent: %w", err)
	}
	defer func() { _ = sb.Close() }()
	if err = sb.SetupCodexAuth(a.cfg.CodexAuthJSON, a.cfg.OpenAIAPIKey); err != nil {
		return "", fmt.Errorf("coding agent: %w", err)
	}
	if err = sb.SetupGitAuth(a.github.CredentialsLine()); err != nil {
		return "", fmt.Errorf("coding agent: %w", err)
	}
	if err = sb.SetupGitHub(a.github.Token(), gitUserName, gitUserEmail); err != nil {
		return "", fmt.Errorf("coding agent: %w", err)
	}

	log.Print("2/3 Coding agent is running the task...")
	out, err := sb.RunCodex("/home/user", a.cfg.CodexModel, message)
	out = strings.TrimSpace(out)
	if err != nil {
		// Return partial output so the reporter keeps context instead of a bare error.
		return out, fmt.Errorf("coding agent: %w", err)
	}
	log.Print("3/3 Coding agent finished.")
	return out, nil
}
