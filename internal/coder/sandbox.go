// Package coder runs delegated coding work in an isolated E2B sandbox.
package coder

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	e2b "github.com/matiasinsaurralde/go-e2b"
)

const defaultTemplateID = "u1yrkaokyjzef8qchho5"

type sandbox struct {
	inner *e2b.Sandbox
	ctx   context.Context
	logW  io.Writer
}

func newSandbox(ctx context.Context, key, templateID string, logW io.Writer) (*sandbox, error) {
	if strings.TrimSpace(templateID) == "" {
		templateID = defaultTemplateID
	}
	client, err := e2b.NewClient(e2b.ClientConfig{APIKey: key, HTTPClient: userClient()})
	if err != nil {
		return nil, err
	}
	inner, err := client.NewSandbox(ctx, e2b.SandboxConfig{Template: templateID, Timeout: 900})
	if err != nil {
		return nil, err
	}
	return &sandbox{inner: inner, ctx: ctx, logW: logW}, nil
}

func (s *sandbox) run(command string) (string, string, error) {
	s.logf("[sandbox] $ %s\n", sanitizeCommand(command))
	started := time.Now()
	defer func() { s.logf("[sandbox] finished in %s\n", time.Since(started).Round(time.Millisecond)) }()
	result, err := s.inner.Commands.RunWithContext(s.ctx, "bash", []string{"-lc", command}, e2b.WithTimeout(12*time.Minute))
	if err != nil {
		return "", "", err
	}
	if s.logW != nil {
		_, _ = io.WriteString(s.logW, result.Stdout)
		_, _ = io.WriteString(s.logW, result.Stderr)
	}
	if result.ExitCode != 0 {
		return result.Stdout, result.Stderr, fmt.Errorf("command exited %d: %s", result.ExitCode, strings.TrimSpace(result.Stdout+"\n"+result.Stderr))
	}
	return result.Stdout, result.Stderr, nil
}

func (s *sandbox) writeFile(path, content string) error {
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	_, _, err := s.run("printf %s " + quote(encoded) + " | base64 -d > " + quote(path))
	return err
}

func (s *sandbox) setupCodex(authJSON, apiKey string) error {
	if _, _, err := s.run("npm i -g @openai/codex && mkdir -p /home/user/.codex"); err != nil {
		return err
	}
	if strings.TrimSpace(authJSON) != "" {
		return s.writeFile("/home/user/.codex/auth.json", authJSON)
	}
	return s.writeFile("/home/user/.codex/auth.json", fmt.Sprintf(`{"auth_mode":"apikey","OPENAI_API_KEY":%q}`, apiKey))
}

func (s *sandbox) setupGitHub(token string) error {
	if _, _, err := s.run("git config --global credential.helper store && mkdir -p /home/user/.config/gh"); err != nil {
		return err
	}
	if err := s.writeFile("/home/user/.git-credentials", "https://x-access-token:"+token+"@github.com\n"); err != nil {
		return err
	}
	hosts := "github.com:\n    oauth_token: " + token + "\n    user: x-access-token\n    git_protocol: https\n"
	if err := s.writeFile("/home/user/.config/gh/hosts.yml", hosts); err != nil {
		return err
	}
	if err := s.writeFile("/home/user/.gh_token", token+"\n"); err != nil {
		return err
	}
	_, _, err := s.run("git config --global user.name 'spores-ai' && git config --global user.email 'hey@hetsaraiya.com'")
	return err
}

func (s *sandbox) runCodex(model, task string) (string, error) {
	if err := s.writeFile("/tmp/codex-prompt.md", task); err != nil {
		return "", err
	}
	command := "cd /home/user && codex exec --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox --color never -o /tmp/codex-output.md"
	if strings.TrimSpace(model) != "" {
		command += " -m " + quote(model)
	}
	command += " - < /tmp/codex-prompt.md"
	stdout, stderr, err := s.run(command)
	if err != nil {
		return "", fmt.Errorf("%w\n%s%s", err, stdout, stderr)
	}
	out, stderr, err := s.run("cat /tmp/codex-output.md")
	if err != nil {
		return "", fmt.Errorf("%w\n%s%s", err, out, stderr)
	}
	return out, nil
}

func (s *sandbox) close() { _ = s.inner.CloseWithContext(context.Background()) }
func (s *sandbox) logf(format string, args ...any) {
	if s.logW != nil {
		_, _ = fmt.Fprintf(s.logW, format, args...)
	}
}

func quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
func sanitizeCommand(command string) string {
	if index := strings.Index(command, "| base64 -d >"); index >= 0 {
		return "write remote file " + strings.TrimSpace(command[index+len("| base64 -d >"):])
	}
	if len(command) > 500 {
		return command[:500] + "... [truncated]"
	}
	return command
}

func userClient() *http.Client {
	return &http.Client{Transport: userTransport{base: http.DefaultTransport}}
}

type userTransport struct{ base http.RoundTripper }

func (t userTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if user := req.Header.Get("User"); user != "" {
		req.SetBasicAuth(user, "")
		req.Header.Del("User")
	}
	return t.base.RoundTrip(req)
}
