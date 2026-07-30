package coder

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/hetsaraiya/spores/internal/codexauth"
)

// Appended when credentials could not be read back: the coding work already
// happened, so reporting a failure would wrongly imply nothing changed.
const refreshFailureNote = "\n\n[warning: the coding run completed, but refreshed Codex credentials could not be read back; the next delegation may need re-authentication]"

type Config struct {
	E2BAPIKey, E2BTemplateID, CodexModel, CodexVersion, OpenAIAPIKey, GitHubToken string
	CodexCredentials                                                              *codexauth.Credentials
}

// Memory supplies standing preferences to append to every brief.
type Memory interface{ BriefSection() string }

type Delegate struct {
	config Config
	logW   io.Writer
	memory Memory
}

func New(config Config, logW io.Writer, store Memory) *Delegate {
	return &Delegate{config: config, logW: logW, memory: store}
}

// result keeps the refresh outcome apart from the run outcome, so one cannot be
// mistaken for the other.
type result struct {
	report         string
	refreshed      string
	refreshFailure error
}

func (d *Delegate) Run(ctx context.Context, task string) (string, error) {
	if strings.TrimSpace(task) == "" {
		return "", fmt.Errorf("delegation task is required")
	}
	if strings.TrimSpace(d.config.E2BAPIKey) == "" {
		return "", fmt.Errorf("E2B_API_KEY is required for delegate_to_coder")
	}
	if !d.config.CodexCredentials.Configured() && strings.TrimSpace(d.config.OpenAIAPIKey) == "" {
		return "", fmt.Errorf("CODEX_AUTH_JSON or OPENAI_API_KEY is required for delegate_to_coder")
	}

	if !d.config.CodexCredentials.Configured() {
		outcome, err := d.run(ctx, task, "")
		return report(outcome), err
	}

	var outcome result
	err := d.config.CodexCredentials.Use(func(authJSON string) (string, error) {
		var runErr error
		outcome, runErr = d.run(ctx, task, authJSON)
		return outcome.refreshed, runErr
	})
	return report(outcome), err
}

// report notes a refresh failure inline rather than discarding the work.
func report(outcome result) string {
	text := strings.TrimSpace(outcome.report)
	if outcome.refreshFailure == nil {
		return text
	}
	log.Printf("coder: %v", outcome.refreshFailure)
	if text == "" {
		return text
	}
	return text + refreshFailureNote
}

func (d *Delegate) run(ctx context.Context, task, authJSON string) (result, error) {
	box, err := newSandbox(ctx, d.config.E2BAPIKey, d.config.E2BTemplateID, d.logW)
	if err != nil {
		return result{}, fmt.Errorf("start coding sandbox: %w", err)
	}
	defer box.close()

	if err := box.setupCodex(d.config.CodexVersion, authJSON, d.config.OpenAIAPIKey); err != nil {
		return result{}, fmt.Errorf("configure Codex: %w", err)
	}
	if strings.TrimSpace(d.config.GitHubToken) != "" {
		if err := box.setupGitHub(d.config.GitHubToken); err != nil {
			return result{}, fmt.Errorf("configure GitHub: %w", err)
		}
	}

	out, runErr := box.runCodex(d.config.CodexModel, d.brief(task))
	outcome := result{report: out}
	if authJSON != "" {
		refreshed, readErr := box.readCodexAuth()
		outcome.refreshed, outcome.refreshFailure = refreshed, readErr
	}
	return outcome, runErr
}

// brief appends standing preferences to the model-composed task. The main agent
// cannot be relied on to repeat them, so they are added here unconditionally.
func (d *Delegate) brief(task string) string {
	if d.memory == nil {
		return task
	}
	section := d.memory.BriefSection()
	if section == "" {
		return task
	}
	return task + "\n\n" + section
}
