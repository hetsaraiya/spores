package coder

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The Daytona integration is hand-rolled against the REST API, so the wire
// contract is the part that can silently break: which host each call goes to,
// that the bearer token is attached, that a sandbox created as "creating" is
// waited on, and that the snapshot's user decides where credentials are written.
// One fake control plane plus one fake toolbox covers all of it.
func TestSandboxSpeaksTheDaytonaWireContract(t *testing.T) {
	startPollDelay = time.Millisecond
	t.Cleanup(func() { startPollDelay = 2 * time.Second })

	var toolboxURL string
	unauthenticated := make(chan string, 16)
	requireToken := func(r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer dtn_secret" {
			unauthenticated <- r.Method + " " + r.URL.Path
		}
	}

	var polls int
	uploads := map[string]string{}
	var executed []string
	deleted := make(chan string, 1)

	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireToken(r)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandbox":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["snapshot"] != "codex-image" {
				t.Errorf("snapshot not forwarded: %v", body)
			}
			// Real creates come back before the runner is ready.
			writeJSON(t, w, map[string]any{"id": "sbx-1", "state": "creating"})
		case r.Method == http.MethodGet && r.URL.Path == "/sandbox/sbx-1":
			polls++
			writeJSON(t, w, map[string]any{
				"id": "sbx-1", "state": "started",
				"user": "coder", "toolboxProxyUrl": toolboxURL + "/toolbox/",
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/sandbox/sbx-1":
			deleted <- "yes"
		default:
			t.Errorf("unexpected control-plane call: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer control.Close()

	toolbox := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireToken(r)
		// Every toolbox call must be scoped to the sandbox id.
		if !strings.HasPrefix(r.URL.Path, "/toolbox/sbx-1/") {
			t.Errorf("toolbox call not scoped to the sandbox: %s", r.URL.Path)
		}
		endpoint := strings.TrimPrefix(r.URL.Path, "/toolbox/sbx-1")
		switch endpoint {
		case "/process/execute":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			executed = append(executed, body["command"].(string))
			if body["cwd"] != "/home/coder" {
				t.Errorf("cwd = %v, want the snapshot user's home", body["cwd"])
			}
			// 0 disables the toolbox's 10s default; a Codex run always exceeds it.
			if body["timeout"] != float64(0) {
				t.Errorf("timeout = %v, want 0", body["timeout"])
			}
			if strings.HasPrefix(body["command"].(string), "cat ") {
				writeJSON(t, w, map[string]any{"exitCode": 0, "result": "Opened PR #12."})
				return
			}
			writeJSON(t, w, map[string]any{"exitCode": 0, "result": ""})
		case "/files/upload-v2":
			content, _ := io.ReadAll(r.Body)
			uploads[r.URL.Query().Get("path")] = string(content)
			w.WriteHeader(http.StatusOK)
		case "/files/download":
			_, _ = io.WriteString(w, uploads[r.URL.Query().Get("path")])
		default:
			t.Errorf("unexpected toolbox call: %s", endpoint)
		}
	}))
	defer toolbox.Close()
	toolboxURL = toolbox.URL

	box, err := newSandbox(context.Background(), control.URL, "dtn_secret", "codex-image", nil)
	if err != nil {
		t.Fatalf("newSandbox: %v", err)
	}
	if polls == 0 {
		t.Fatal("a sandbox reported as \"creating\" was used without waiting for it to start")
	}

	if err := box.setupCodex("1.2.3", `{"tokens":"live"}`, ""); err != nil {
		t.Fatalf("setupCodex: %v", err)
	}
	// Credentials must land under the snapshot user's home, not a hardcoded one.
	if got := uploads["/home/coder/.codex/auth.json"]; got != `{"tokens":"live"}` {
		t.Fatalf("auth.json = %q, want the supplied credentials at the user's home", got)
	}
	if err := box.setupGitHub("ghs_token"); err != nil {
		t.Fatalf("setupGitHub: %v", err)
	}
	if got := uploads["/home/coder/.git-credentials"]; !strings.Contains(got, "ghs_token") {
		t.Fatalf("git credentials = %q", got)
	}
	// The token is written as a file, never interpolated into a command.
	for _, command := range executed {
		if strings.Contains(command, "ghs_token") || strings.Contains(command, "live") {
			t.Fatalf("a secret reached a command line: %s", command)
		}
	}

	report, err := box.runCodex("gpt-5.6", "Fix the build.")
	if err != nil {
		t.Fatalf("runCodex: %v", err)
	}
	if report != "Opened PR #12." {
		t.Fatalf("report = %q", report)
	}
	if uploads[promptPath] != "Fix the build." {
		t.Fatalf("prompt = %q", uploads[promptPath])
	}

	refreshed, err := box.readCodexAuth()
	if err != nil || refreshed != `{"tokens":"live"}` {
		t.Fatalf("readCodexAuth = %q, %v", refreshed, err)
	}

	box.close()
	select {
	case <-deleted:
	default:
		t.Fatal("the sandbox was never deleted; it would keep billing")
	}

	close(unauthenticated)
	for call := range unauthenticated {
		t.Errorf("request sent without the API key: %s", call)
	}
}

// A non-zero exit arrives as HTTP 200, so only the exitCode field distinguishes
// a failed command from a successful one.
func TestRunTurnsNonZeroExitIntoAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"exitCode": 127, "result": "codex: not found"})
	}))
	defer server.Close()

	box := &sandbox{ctx: context.Background(), toolbox: server.URL}
	out, err := box.run("codex --version")
	if err == nil {
		t.Fatal("a command that exited 127 was reported as success")
	}
	if !strings.Contains(out, "not found") {
		t.Fatalf("output was discarded: %q", out)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, body any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
