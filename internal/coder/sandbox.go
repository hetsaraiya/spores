// Package coder runs delegated tasks in an isolated E2B sandbox.
package coder

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	e2b "github.com/matiasinsaurralde/go-e2b"
)

const (
	defaultTemplateID = "u1yrkaokyjzef8qchho5"

	// commandTimeout stays under sandboxLifetime so a hung command fails with a
	// usable error rather than a vanished sandbox.
	sandboxLifetime = 900 // seconds, E2B's unit
	commandTimeout  = 12 * time.Minute

	codexPackage = "@openai/codex"

	codexHome       = "/home/user/.codex"
	codexAuthPath   = codexHome + "/auth.json"
	promptPath      = "/tmp/codex-prompt.md"
	outputPath      = "/tmp/codex-output.md"
	credentialsPath = "/home/user/.git-credentials"
	ghHostsPath     = "/home/user/.config/gh/hosts.yml"
	workDir         = "/home/user"

	gitUserName  = "spores-ai"
	gitUserEmail = "hey@hetsaraiya.com"

	maxLoggedCommand = 500
)

type sandbox struct {
	inner *e2b.Sandbox
	ctx   context.Context
	logW  io.Writer
}

func newSandbox(ctx context.Context, key, templateID string, logW io.Writer) (*sandbox, error) {
	if strings.TrimSpace(templateID) == "" {
		templateID = defaultTemplateID
	}
	client, err := e2b.NewClient(e2b.ClientConfig{APIKey: key})
	if err != nil {
		return nil, err
	}
	inner, err := client.NewSandbox(ctx, e2b.SandboxConfig{Template: templateID, Timeout: sandboxLifetime})
	if err != nil {
		return nil, err
	}
	return &sandbox{inner: inner, ctx: ctx, logW: logW}, nil
}

func (s *sandbox) run(command string) (string, string, error) {
	s.logf("[sandbox] $ %s\n", shortenCommand(command))
	started := time.Now()
	defer func() { s.logf("[sandbox] finished in %s\n", time.Since(started).Round(time.Millisecond)) }()
	result, err := s.inner.Commands.Run(s.ctx, command, e2b.WithTimeout(commandTimeout))
	if result == nil {
		return "", "", err
	}
	if s.logW != nil {
		_, _ = io.WriteString(s.logW, result.Stdout)
		_, _ = io.WriteString(s.logW, result.Stderr)
	}
	// A non-zero exit arrives as *e2b.CommandExitError, already carrying output.
	return result.Stdout, result.Stderr, err
}

// writeFile goes through the filesystem API, not a shell command, so tokens and
// Codex credentials never appear on a command line or in a process list.
func (s *sandbox) writeFile(target, content string) error {
	s.logf("[sandbox] write %s (%d bytes)\n", target, len(content))
	if _, err := s.inner.Filesystem.WriteString(s.ctx, target, content); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	return nil
}

func (s *sandbox) readCodexAuth() (string, error) {
	authJSON, err := s.inner.Filesystem.ReadString(s.ctx, codexAuthPath)
	if err != nil {
		return "", fmt.Errorf("read refreshed Codex credentials: %w", err)
	}
	return authJSON, nil
}

func (s *sandbox) setupCodex(version, authJSON, apiKey string) error {
	if _, _, err := s.run("npm i -g " + quote(codexSpec(version)) + " && mkdir -p " + quote(codexHome)); err != nil {
		return err
	}
	if strings.TrimSpace(authJSON) != "" {
		return s.writeFile(codexAuthPath, authJSON)
	}
	return s.writeFile(codexAuthPath, fmt.Sprintf(`{"auth_mode":"apikey","OPENAI_API_KEY":%q}`, apiKey))
}

// codexSpec pins the release when CODEX_VERSION is set; unset installs whatever
// the registry serves at that moment.
func codexSpec(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return codexPackage
	}
	return codexPackage + "@" + version
}

func (s *sandbox) setupGitHub(token string) error {
	if _, _, err := s.run("git config --global credential.helper store && mkdir -p " + quote(path.Dir(ghHostsPath))); err != nil {
		return err
	}
	if err := s.writeFile(credentialsPath, "https://x-access-token:"+token+"@github.com\n"); err != nil {
		return err
	}
	hosts := "github.com:\n    oauth_token: " + token + "\n    user: x-access-token\n    git_protocol: https\n"
	if err := s.writeFile(ghHostsPath, hosts); err != nil {
		return err
	}
	_, _, err := s.run("git config --global user.name " + quote(gitUserName) + " && git config --global user.email " + quote(gitUserEmail))
	return err
}

func (s *sandbox) runCodex(model, task string) (string, error) {
	if err := s.writeFile(promptPath, task); err != nil {
		return "", err
	}
	command := codexCommand(model)
	stdout, stderr, err := s.run(command)
	if err != nil {
		return "", fmt.Errorf("%w\n%s%s", err, stdout, stderr)
	}
	out, stderr, err := s.run("cat " + quote(outputPath))
	if err != nil {
		return "", fmt.Errorf("%w\n%s%s", err, out, stderr)
	}
	return out, nil
}

func codexCommand(model string) string {
	command := "cd " + quote(workDir) + " && codex exec --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox --color never -o " + quote(outputPath)
	if strings.TrimSpace(model) != "" {
		command += " -m " + quote(model)
	}
	return command + " - < " + quote(promptPath)
}

func (s *sandbox) close() { _ = s.inner.CloseWithContext(context.Background()) }
func (s *sandbox) logf(format string, args ...any) {
	if s.logW != nil {
		_, _ = fmt.Fprintf(s.logW, format, args...)
	}
}

func quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
func shortenCommand(command string) string {
	if len(command) > maxLoggedCommand {
		return command[:maxLoggedCommand] + "... [truncated]"
	}
	return command
}
