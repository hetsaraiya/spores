package coder

import (
	"context"
	"fmt"
	"io"
	"strings"
)

type Config struct {
	E2BAPIKey, E2BTemplateID, CodexModel, CodexAuthJSON, OpenAIAPIKey, GitHubToken string
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

func (d *Delegate) Run(ctx context.Context, task string) (string, error) {
	if strings.TrimSpace(task) == "" {
		return "", fmt.Errorf("coding task is required")
	}
	if strings.TrimSpace(d.config.E2BAPIKey) == "" {
		return "", fmt.Errorf("E2B_API_KEY is required for delegate_to_coder")
	}
	if strings.TrimSpace(d.config.GitHubToken) == "" {
		return "", fmt.Errorf("GITHUB_TOKEN is required for delegate_to_coder")
	}
	if strings.TrimSpace(d.config.CodexAuthJSON) == "" && strings.TrimSpace(d.config.OpenAIAPIKey) == "" {
		return "", fmt.Errorf("CODEX_AUTH_JSON or OPENAI_API_KEY is required for delegate_to_coder")
	}
	box, err := newSandbox(ctx, d.config.E2BAPIKey, d.config.E2BTemplateID, d.logW)
	if err != nil {
		return "", fmt.Errorf("start coding sandbox: %w", err)
	}
	defer box.close()
	if err := box.setupCodex(d.config.CodexAuthJSON, d.config.OpenAIAPIKey); err != nil {
		return "", fmt.Errorf("configure Codex: %w", err)
	}
	if err := box.setupGitHub(d.config.GitHubToken); err != nil {
		return "", fmt.Errorf("configure GitHub: %w", err)
	}
	out, err := box.runCodex(d.config.CodexModel, d.brief(task))
	return strings.TrimSpace(out), err
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
