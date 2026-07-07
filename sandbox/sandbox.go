package sandbox

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

type Sandbox struct {
	inner *e2b.Sandbox
	ctx   context.Context
	logW  io.Writer // if non-nil, sandbox stdout/stderr is streamed here live
}

// New creates a sandbox (empty templateID uses the package default). A non-nil
// logW (e.g. os.Stdout) streams command output live.
func New(ctx context.Context, key, templateID string, logW io.Writer) (*Sandbox, error) {
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
	sb := &Sandbox{inner: inner, ctx: ctx, logW: logW}
	sb.logf("[sandbox] created id=%s template=%s timeout=900s\n", inner.ID, templateID)
	return sb, nil
}

func (s *Sandbox) RunCommand(cmd string) (string, string, error) {
	s.logf("\n[sandbox] $ %s\n", sanitizeCommand(cmd))
	start := time.Now()
	defer func() {
		s.logf("[sandbox] finished in %s\n", time.Since(start).Round(time.Millisecond))
	}()

	res, err := s.inner.Commands.RunWithContext(s.ctx, "bash", []string{"-lc", cmd}, e2b.WithTimeout(12*time.Minute))
	if err != nil {
		s.logf("[sandbox] error: %v\n", err)
		return "", "", err
	}
	stdout := res.Stdout
	stderr := res.Stderr
	if s.logW != nil {
		if stdout != "" {
			io.WriteString(s.logW, stdout)
		}
		if stderr != "" {
			io.WriteString(s.logW, stderr)
		}
	}
	if res.ExitCode != 0 {
		detail := strings.TrimSpace(stdout + "\n" + stderr)
		s.logf("[sandbox] exit %d\n", res.ExitCode)
		return stdout, stderr, fmt.Errorf("command exited %d: %s", res.ExitCode, detail)
	}
	s.logf("[sandbox] exit 0\n")
	return stdout, stderr, nil
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

// SetupGitAuth points git at the credential store so clone/push URLs stay
// credential-free. The line is written via writeRemoteFile (redacted from logs).
func (s *Sandbox) SetupGitAuth(credentialsLine string) error {
	if strings.TrimSpace(credentialsLine) == "" {
		return fmt.Errorf("git credentials are required; set GITHUB_TOKEN or GH_TOKEN")
	}
	if _, _, err := s.RunCommand("git config --global credential.helper store"); err != nil {
		return err
	}
	return s.writeRemoteFile("/home/user/.git-credentials", credentialsLine+"\n")
}

// SetupGitHub prepares the agent's GitHub access: authenticates the gh CLI via
// hosts.yml, drops the token at /home/user/.gh_token for raw REST, and sets a git
// identity so commits succeed. Token written via writeRemoteFile (redacted from logs).
func (s *Sandbox) SetupGitHub(token, name, email string) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("GitHub token is required; set GITHUB_TOKEN or GH_TOKEN")
	}
	if _, _, err := s.RunCommand("mkdir -p /home/user/.config/gh"); err != nil {
		return err
	}
	hosts := "github.com:\n    oauth_token: " + token + "\n    user: x-access-token\n    git_protocol: https\n"
	if err := s.writeRemoteFile("/home/user/.config/gh/hosts.yml", hosts); err != nil {
		return err
	}
	if err := s.writeRemoteFile("/home/user/.gh_token", token+"\n"); err != nil {
		return err
	}
	for _, cmd := range []string{
		"git config --global user.name " + Quote(name),
		"git config --global user.email " + Quote(email),
	} {
		if _, _, err := s.RunCommand(cmd); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sandbox) RunCodex(cwd, model, prompt string) (string, error) {
	promptPath := "/tmp/codex-prompt.md"
	if err := s.writeRemoteFile(promptPath, prompt); err != nil {
		return "", err
	}
	output := "/tmp/codex-output.md"
	_, _, _ = s.RunCommand("rm -f " + Quote(output))
	cmd := "cd " + Quote(cwd) + " && codex exec --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox --color never -o " + Quote(output)
	if strings.TrimSpace(model) != "" {
		cmd += " -m " + Quote(model)
	}
	cmd += " - < /tmp/codex-prompt.md"
	out, stderr, err := s.RunCommand(cmd)
	if err != nil {
		return "", fmt.Errorf("%w\n%s%s", err, out, stderr)
	}
	out, stderr, err = s.RunCommand("cat " + Quote(output))
	if err != nil {
		return "", fmt.Errorf("%w\n%s%s", err, out, stderr)
	}
	return out, nil
}

func (s *Sandbox) writeRemoteFile(path, content string) error {
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	cmd := "printf %s " + Quote(encoded) + " | base64 -d > " + Quote(path)
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

// Quote single-quotes s for safe interpolation into a shell command.
func Quote(s string) string {
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

// ponytail: folded from transport.go — trivial transport, no separate file needed.
func userClient() *http.Client {
	return &http.Client{Transport: userTransport{base: http.DefaultTransport}}
}

type userTransport struct {
	base http.RoundTripper
}

func (t userTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if user := req.Header.Get("User"); user != "" {
		req.SetBasicAuth(user, "")
		req.Header.Del("User")
	}
	return t.base.RoundTrip(req)
}
