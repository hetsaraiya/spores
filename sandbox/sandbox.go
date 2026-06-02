package sandbox

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	e2b "github.com/matiasinsaurralde/go-e2b"
)

const defaultTemplateID = "u1yrkaokyjzef8qchho5"

type Sandbox struct {
	inner *e2b.Sandbox
	ctx   context.Context
	logW  io.Writer // if non-nil, sandbox stdout/stderr is streamed here live
}

// New creates a sandbox. Pass os.Stdout (or any io.Writer) as the optional
// logW argument to stream all command output to your terminal in real time.
func New(ctx context.Context, key string, logW ...io.Writer) (*Sandbox, error) {
	templateID := os.Getenv("E2B_TEMPLATE_ID")
	if strings.TrimSpace(templateID) == "" {
		templateID = os.Getenv("E2B_TEMPLATE")
	}
	if strings.TrimSpace(templateID) == "" {
		templateID = defaultTemplateID
	}
	client, err := e2b.NewClient(e2b.ClientConfig{
		APIKey:     key,
		HTTPClient: userClient(),
	})
	if err != nil {
		return nil, err
	}
	inner, err := client.NewSandbox(ctx, e2b.SandboxConfig{Template: templateID, Timeout: 900})
	if err != nil {
		return nil, err
	}
	sb := &Sandbox{inner: inner, ctx: ctx}
	if len(logW) > 0 && logW[0] != nil {
		sb.logW = logW[0]
	}
	sb.logf("[sandbox] created id=%s template=%s timeout=900s\n", inner.ID, templateID)
	return sb, nil
}

func (s *Sandbox) ProbeIO() error {
	out, stderr, err := s.RunCommand("printf '[sandbox-io] stdout is live\\n'; printf '[sandbox-io] stderr is live\\n' >&2; pwd; whoami; command -v codex || true; codex --version || true")
	if err != nil {
		return fmt.Errorf("%w\n%s%s", err, out, stderr)
	}
	return nil
}

func (s *Sandbox) RunCommand(cmd string) (string, string, error) {
	return s.run(cmd)
}

func (s *Sandbox) run(cmd string, opts ...e2b.RunOption) (string, string, error) {
	opts = append(opts, e2b.WithTimeout(12*time.Minute))
	s.logf("\n[sandbox] $ %s\n", sanitizeCommand(cmd))
	start := time.Now()
	defer func() {
		s.logf("[sandbox] finished in %s\n", time.Since(start).Round(time.Millisecond))
	}()

	var stdoutDest, stderrDest io.Writer = io.Discard, io.Discard
	var stdoutBuf, stderrBuf strings.Builder

	if s.logW != nil {
		stdoutDest = s.logW
		stderrDest = s.logW
	}
	opts = append(opts, e2b.WithStdout(io.MultiWriter(&stdoutBuf, stdoutDest)))
	opts = append(opts, e2b.WithStderr(io.MultiWriter(&stderrBuf, stderrDest)))

	res, err := s.inner.Commands.RunWithContext(s.ctx, "bash", []string{"-lc", cmd}, opts...)
	if err != nil {
		s.logf("[sandbox] error: %v\n", err)
		return stdoutBuf.String(), stderrBuf.String(), err
	}
	stdout := stdoutBuf.String()
	stderr := stderrBuf.String()
	if stdout == "" && res.Stdout != "" {
		stdout = res.Stdout
	}
	if stderr == "" && res.Stderr != "" {
		stderr = res.Stderr
	}
	if res.ExitCode != 0 {
		detail := strings.TrimSpace(stdout + "\n" + stderr)
		s.logf("[sandbox] exit %d\n", res.ExitCode)
		return stdout, stderr, fmt.Errorf("command exited %d: %s", res.ExitCode, detail)
	}
	s.logf("[sandbox] exit 0\n")
	return stdout, stderr, nil
}

func (s *Sandbox) WriteFile(path, content string) error {
	_, err := s.inner.Filesystem.WriteString(s.ctx, path, content)
	return err
}

func (s *Sandbox) ReadFile(path string) (string, error) {
	return s.inner.Filesystem.ReadString(s.ctx, path)
}

func (s *Sandbox) ListFiles(dir string) (string, error) {
	out, _, err := s.RunCommand("ls -R " + quote(dir))
	return out, err
}

func (s *Sandbox) SetupCodexAuth(authJSON, openAIKey string) error {
	if strings.TrimSpace(authJSON) == "" && strings.TrimSpace(openAIKey) == "" {
		return fmt.Errorf("Codex auth is required; set CODEX_AUTH_FILE, CODEX_AUTH_JSON, or OPENAI_API_KEY")
	}
	if _, _, err := s.RunCommand("npm i -g @openai/codex"); err != nil {
		return err
	}
	if _, _, err := s.RunCommand("mkdir -p /home/user/.codex"); err != nil {
		return err
	}
	if strings.TrimSpace(authJSON) != "" {
		return s.writeRemoteFile("/home/user/.codex/auth.json", authJSON)
	}
	auth := fmt.Sprintf(`{"auth_mode":"apikey","OPENAI_API_KEY":%q}`, openAIKey)
	return s.writeRemoteFile("/home/user/.codex/auth.json", auth)
}

func (s *Sandbox) RunCodex(cwd, model, prompt string) (string, error) {
	promptPath := "/tmp/codex-prompt.md"
	if err := s.writeRemoteFile(promptPath, prompt); err != nil {
		return "", err
	}
	output := "/tmp/codex-output.md"
	_, _, _ = s.RunCommand("rm -f " + quote(output))
	cmd := "cd " + quote(cwd) + " && codex exec --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox --color never -o " + quote(output)
	if strings.TrimSpace(model) != "" {
		cmd += " -m " + quote(model)
	}
	cmd += " - < /tmp/codex-prompt.md"
	out, stderr, err := s.RunCommand(cmd)
	if err != nil {
		return "", fmt.Errorf("%w\n%s%s", err, out, stderr)
	}
	out, stderr, err = s.RunCommand("cat " + quote(output))
	if err != nil {
		return "", fmt.Errorf("%w\n%s%s", err, out, stderr)
	}
	return out, nil
}

func (s *Sandbox) writeRemoteFile(path, content string) error {
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	cmd := "printf %s " + quote(encoded) + " | base64 -d > " + quote(path)
	_, _, err := s.RunCommand(cmd)
	return err
}

func (s *Sandbox) logf(format string, args ...any) {
	if s.logW != nil {
		_, _ = fmt.Fprintf(s.logW, format, args...)
	}
}

func (s *Sandbox) Close() error {
	return s.inner.CloseWithContext(context.Background())
}

func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func sanitizeCommand(cmd string) string {
	if idx := strings.Index(cmd, "| base64 -d >"); idx >= 0 {
		return "write remote file " + strings.TrimSpace(cmd[idx+len("| base64 -d >"):])
	}
	if len(cmd) > 500 {
		return cmd[:500] + "... [truncated]"
	}
	return cmd
}
