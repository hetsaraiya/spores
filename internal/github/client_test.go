package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSplitRepoRejectsMalformedNames(t *testing.T) {
	valid := map[string][2]string{
		"owner/repo":   {"owner", "repo"},
		"  owner/repo": {"owner", "repo"},
	}
	for input, want := range valid {
		owner, repo, err := splitRepo(input)
		if err != nil || owner != want[0] || repo != want[1] {
			t.Errorf("splitRepo(%q) = %q/%q err=%v", input, owner, repo, err)
		}
	}
	for _, input := range []string{"", "owner", "owner/", "/repo", "a/b/c", "   "} {
		if _, _, err := splitRepo(input); err == nil {
			t.Errorf("splitRepo(%q) was accepted", input)
		}
	}
}

func TestRepoPathEscapesComponents(t *testing.T) {
	path, err := repoPath("my owner/my repo")
	if err != nil {
		t.Fatalf("repoPath: %v", err)
	}
	if strings.Contains(path, " ") {
		t.Fatalf("path was not escaped: %q", path)
	}
}

func TestClipBoundsToolOutput(t *testing.T) {
	short := strings.Repeat("a", maxToolOutput)
	if clip(short) != short {
		t.Fatal("output at the limit was clipped")
	}
	long := clip(strings.Repeat("a", maxToolOutput+100))
	if len(long) != maxToolOutput+len(truncationNote) {
		t.Fatalf("clipped output is %d bytes", len(long))
	}
	if !strings.HasSuffix(long, truncationNote) {
		t.Fatal("truncation was not marked")
	}
}

func TestDecodeContentHandlesWrappedBase64(t *testing.T) {
	// GitHub wraps base64 file contents across lines.
	decoded, err := decodeContent("aGVsbG8g\nd29ybGQ=")
	if err != nil {
		t.Fatalf("decodeContent: %v", err)
	}
	if decoded != "hello world" {
		t.Fatalf("got %q", decoded)
	}
	if _, err := decodeContent("!!not base64!!"); err == nil {
		t.Fatal("invalid base64 was accepted")
	}
}

func TestNotePartialPageMarksFullPages(t *testing.T) {
	var full, partial strings.Builder
	notePartialPage(&full, listPageSize, listPageSize)
	notePartialPage(&partial, listPageSize-1, listPageSize)

	if !strings.Contains(full.String(), "more exist") {
		t.Fatal("a full page was not marked as possibly truncated")
	}
	if partial.String() != "" {
		t.Fatalf("a partial page was marked: %q", partial.String())
	}
}

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &Client{http: server.Client(), token: "test-token"}, server
}

func TestFetchSendsAPIHeaders(t *testing.T) {
	var got http.Header
	client, server := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Write([]byte(`{}`))
	})
	if _, err := client.fetch(context.Background(), server.URL); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got.Get("Accept") != acceptHeader {
		t.Errorf("Accept = %q", got.Get("Accept"))
	}
	if got.Get("X-GitHub-Api-Version") != apiVersion {
		t.Errorf("X-GitHub-Api-Version = %q", got.Get("X-GitHub-Api-Version"))
	}
	if got.Get("Authorization") != "Bearer test-token" {
		t.Errorf("Authorization = %q", got.Get("Authorization"))
	}
}

func TestFetchBoundsTheResponseBody(t *testing.T) {
	client, server := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		for range (maxResponseBytes / 1024) + 64 {
			w.Write([]byte(strings.Repeat("a", 1024)))
		}
	})
	body, err := client.fetch(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(body) > maxResponseBytes {
		t.Fatalf("buffered %d bytes, over the %d-byte cap", len(body), maxResponseBytes)
	}
}

func TestFetchReportsErrorStatuses(t *testing.T) {
	client, server := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not Found"}`))
	})
	_, err := client.fetch(context.Background(), server.URL)
	if err == nil {
		t.Fatal("a 404 was treated as success")
	}
	if !strings.Contains(err.Error(), "Not Found") {
		t.Fatalf("error lost the API message: %v", err)
	}
}

func TestNormalizeState(t *testing.T) {
	if state, err := normalizeState(""); err != nil || state != stateOpen {
		t.Errorf("blank state = %q err=%v, want %q", state, err, stateOpen)
	}
	for _, state := range []string{stateOpen, stateClosed, stateAll} {
		if got, err := normalizeState(state); err != nil || got != state {
			t.Errorf("normalizeState(%q) = %q err=%v", state, got, err)
		}
	}
	if _, err := normalizeState("merged"); err == nil {
		t.Error("an invalid state was accepted")
	}
}
