// Package coder runs delegated tasks in an isolated Daytona sandbox.
package coder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	defaultAPIURL = "https://app.daytona.io/api"
	// Daytona's stock snapshot runs as this user; only a custom snapshot that
	// omits the field can leave it blank.
	defaultUser = "daytona"

	codexPackage = "@openai/codex"

	promptPath = "/tmp/codex-prompt.md"
	outputPath = "/tmp/codex-output.md"

	gitUserName  = "spores-ai"
	gitUserEmail = "hey@hetsaraiya.com"

	maxLoggedCommand = 500
)

// startPollDelay is a var so tests do not pay the real wait.
var startPollDelay = 2 * time.Second

type sandbox struct {
	ctx context.Context
	key string
	// apiURL is the control plane; toolbox is the per-sandbox data plane, and
	// the two live on different hosts.
	apiURL  string
	toolbox string
	id      string
	home    string
	logW    io.Writer
}

// sandboxDTO is the subset of Daytona's sandbox record this package reads.
type sandboxDTO struct {
	ID              string `json:"id"`
	State           string `json:"state"`
	User            string `json:"user"`
	ToolboxProxyURL string `json:"toolboxProxyUrl"`
	ErrorReason     string `json:"errorReason"`
}

func newSandbox(ctx context.Context, apiURL, key, snapshot string, logW io.Writer) (*sandbox, error) {
	if strings.TrimSpace(apiURL) == "" {
		apiURL = defaultAPIURL
	}
	s := &sandbox{ctx: ctx, key: key, apiURL: strings.TrimRight(apiURL, "/"), logW: logW}

	body := map[string]string{}
	if snapshot = strings.TrimSpace(snapshot); snapshot != "" {
		body["snapshot"] = snapshot
	}
	info, err := s.decodeSandbox(s.do(http.MethodPost, s.apiURL+"/sandbox", jsonBody(body)))
	if err != nil {
		return nil, err
	}
	// Past this point the sandbox is billable, so any failure has to delete it
	// rather than leave it running unreferenced.
	s.id = info.ID
	if err := s.waitUntilStarted(info); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

// waitUntilStarted polls because POST /sandbox returns as soon as the record
// exists; commands are refused until the runner reports "started".
func (s *sandbox) waitUntilStarted(info sandboxDTO) error {
	for info.State != "started" {
		switch info.State {
		case "error", "build_failed", "destroyed":
			return fmt.Errorf("sandbox entered state %q: %s", info.State, info.ErrorReason)
		}
		s.logf("[sandbox] waiting to start (state=%s)\n", info.State)
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-time.After(startPollDelay):
		}
		next, err := s.decodeSandbox(s.do(http.MethodGet, s.apiURL+"/sandbox/"+s.id, nil))
		if err != nil {
			return err
		}
		info = next
	}
	user := info.User
	if user == "" {
		user = defaultUser
	}
	s.home = "/home/" + user
	s.toolbox = strings.TrimRight(info.ToolboxProxyURL, "/") + "/" + s.id
	return nil
}

func (s *sandbox) decodeSandbox(payload []byte, err error) (sandboxDTO, error) {
	if err != nil {
		return sandboxDTO{}, err
	}
	var info sandboxDTO
	if err := json.Unmarshal(payload, &info); err != nil {
		return sandboxDTO{}, fmt.Errorf("decode sandbox: %w", err)
	}
	return info, nil
}

// do issues one authenticated request. The body is a func so a retry would not
// read from an already-drained reader; today there is exactly one attempt.
func (s *sandbox) do(method, endpoint string, body *bytes.Reader) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = body
	}
	req, err := http.NewRequestWithContext(s.ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("daytona %s: %s: %s", method, resp.Status, bytes.TrimSpace(payload))
	}
	return payload, nil
}

// run executes a shell command. Daytona merges stdout and stderr into one
// stream, so callers get one combined output rather than a separate stdout and stderr.
func (s *sandbox) run(command string) (string, error) {
	s.logf("[sandbox] $ %s\n", shortenCommand(command))
	started := time.Now()
	defer func() { s.logf("[sandbox] finished in %s\n", time.Since(started).Round(time.Millisecond)) }()

	// timeout 0 disables the toolbox's 10-second default, which every Codex run
	// would otherwise blow through.
	payload, err := s.do(http.MethodPost, s.toolbox+"/process/execute",
		jsonBody(map[string]any{"command": command, "cwd": s.home, "timeout": 0}))
	if err != nil {
		return "", err
	}
	var result struct {
		ExitCode int    `json:"exitCode"`
		Result   string `json:"result"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return "", fmt.Errorf("decode command result: %w", err)
	}
	if s.logW != nil {
		_, _ = io.WriteString(s.logW, result.Result)
	}
	// A non-zero exit is reported as HTTP 200, so it has to become an error here.
	if result.ExitCode != 0 {
		return result.Result, fmt.Errorf("command exited %d", result.ExitCode)
	}
	return result.Result, nil
}

// writeFile goes through the filesystem API, not a shell command, so tokens and
// Codex credentials never appear on a command line or in a process list.
func (s *sandbox) writeFile(target, content string) error {
	s.logf("[sandbox] write %s (%d bytes)\n", target, len(content))
	req, err := http.NewRequestWithContext(s.ctx, http.MethodPost,
		s.toolbox+"/files/upload-v2?path="+url.QueryEscape(target), strings.NewReader(content))
	if err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	req.Header.Set("Authorization", "Bearer "+s.key)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("write %s: %s", target, resp.Status)
	}
	return nil
}

func (s *sandbox) readCodexAuth() (string, error) {
	payload, err := s.do(http.MethodGet, s.toolbox+"/files/download?path="+url.QueryEscape(s.codexAuthPath()), nil)
	if err != nil {
		return "", fmt.Errorf("read refreshed Codex credentials: %w", err)
	}
	return string(payload), nil
}

// Home-relative paths depend on the snapshot's user, which is only known once
// the sandbox exists.
func (s *sandbox) codexHome() string       { return s.home + "/.codex" }
func (s *sandbox) codexAuthPath() string   { return s.codexHome() + "/auth.json" }
func (s *sandbox) credentialsPath() string { return s.home + "/.git-credentials" }
func (s *sandbox) ghHostsPath() string     { return s.home + "/.config/gh/hosts.yml" }

func (s *sandbox) setupCodex(version, authJSON, apiKey string) error {
	if _, err := s.run("npm i -g " + quote(codexSpec(version)) + " && mkdir -p " + quote(s.codexHome())); err != nil {
		return err
	}
	if strings.TrimSpace(authJSON) != "" {
		return s.writeFile(s.codexAuthPath(), authJSON)
	}
	return s.writeFile(s.codexAuthPath(), fmt.Sprintf(`{"auth_mode":"apikey","OPENAI_API_KEY":%q}`, apiKey))
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
	if _, err := s.run("git config --global credential.helper store && mkdir -p " + quote(path.Dir(s.ghHostsPath()))); err != nil {
		return err
	}
	if err := s.writeFile(s.credentialsPath(), "https://x-access-token:"+token+"@github.com\n"); err != nil {
		return err
	}
	hosts := "github.com:\n    oauth_token: " + token + "\n    user: x-access-token\n    git_protocol: https\n"
	if err := s.writeFile(s.ghHostsPath(), hosts); err != nil {
		return err
	}
	_, err := s.run("git config --global user.name " + quote(gitUserName) + " && git config --global user.email " + quote(gitUserEmail))
	return err
}

func (s *sandbox) runCodex(model, task string) (string, error) {
	if err := s.writeFile(promptPath, task); err != nil {
		return "", err
	}
	output, err := s.run(codexCommand(model))
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, output)
	}
	out, err := s.run("cat " + quote(outputPath))
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, out)
	}
	return out, nil
}

// The working directory is set per request via the execute API's cwd, so the
// command itself no longer hardcodes a home path.
func codexCommand(model string) string {
	command := "codex --search exec --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox --color never -o " + quote(outputPath)
	if strings.TrimSpace(model) != "" {
		command += " -m " + quote(model)
	}
	return command + " - < " + quote(promptPath)
}

// close runs on a background context: the request context is usually already
// cancelled by the time cleanup happens, and an undeleted sandbox keeps billing.
func (s *sandbox) close() {
	if s.id == "" {
		return
	}
	deleter := &sandbox{ctx: context.Background(), key: s.key, apiURL: s.apiURL}
	if _, err := deleter.do(http.MethodDelete, s.apiURL+"/sandbox/"+s.id, nil); err != nil {
		s.logf("[sandbox] delete %s failed: %v\n", s.id, err)
	}
}

func (s *sandbox) logf(format string, args ...any) {
	if s.logW != nil {
		_, _ = fmt.Fprintf(s.logW, format, args...)
	}
}

func jsonBody(value any) *bytes.Reader {
	encoded, err := json.Marshal(value)
	if err != nil {
		// Only map[string]string and map[string]any of scalars reach this.
		encoded = []byte("{}")
	}
	return bytes.NewReader(encoded)
}

func quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
func shortenCommand(command string) string {
	if len(command) > maxLoggedCommand {
		return command[:maxLoggedCommand] + "... [truncated]"
	}
	return command
}
