package portal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hetsaraiya/spores/internal/memory"
)

const token = "test-token"

func newServer(t *testing.T) (http.Handler, *memory.Store) {
	t.Helper()
	store, err := memory.New(t.TempDir())
	if err != nil {
		t.Fatalf("memory.New: %v", err)
	}
	server, err := New(store, token)
	if err != nil {
		t.Fatalf("portal.New: %v", err)
	}
	return server.Handler(), store
}

func call(t *testing.T, handler http.Handler, method, target, auth, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if auth != "" {
		request.Header.Set("Authorization", auth)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestNewRequiresToken(t *testing.T) {
	store, _ := memory.New(t.TempDir())
	if _, err := New(store, "  "); err == nil {
		t.Fatal("portal started without a token")
	}
}

func TestAPIRequiresValidBearerToken(t *testing.T) {
	handler, _ := newServer(t)
	for label, auth := range map[string]string{
		"missing": "",
		"wrong":   "Bearer nope",
		"raw":     token,
		"prefix":  "Bearer test-toke",
	} {
		if code := call(t, handler, "GET", "/api/files", auth, "").Code; code != http.StatusUnauthorized {
			t.Errorf("%s token: got %d, want 401", label, code)
		}
	}
	if code := call(t, handler, "GET", "/api/files", "Bearer "+token, "").Code; code != http.StatusOK {
		t.Errorf("valid token rejected: %d", code)
	}
}

func TestCRUDRoundTrip(t *testing.T) {
	handler, store := newServer(t)
	bearer := "Bearer " + token

	if code := call(t, handler, "PUT", "/api/file?name=PROFILE/testing.md", bearer, "- Run go test ./... before pushing.\n").Code; code != http.StatusOK {
		t.Fatalf("PUT: %d", code)
	}
	if store.IsEmpty() {
		t.Fatal("PUT did not reach the store")
	}

	response := call(t, handler, "GET", "/api/file?name=PROFILE/testing.md", bearer, "")
	var read struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &read); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if !strings.Contains(read.Content, "go test") {
		t.Fatalf("GET returned %q", read.Content)
	}

	if code := call(t, handler, "DELETE", "/api/file?name=PROFILE/testing.md", bearer, "").Code; code != http.StatusOK {
		t.Fatalf("DELETE: %d", code)
	}
	if !store.IsEmpty() {
		t.Fatal("DELETE did not reach the store")
	}
}

func TestWriteRejectsInvalidNamesAndSecrets(t *testing.T) {
	handler, store := newServer(t)
	bearer := "Bearer " + token
	for label, target := range map[string]string{
		"traversal": "/api/file?name=SKILLS/../../etc/passwd",
		"non-md":    "/api/file?name=COMPANY.txt",
		"missing":   "/api/file",
	} {
		if code := call(t, handler, "PUT", target, bearer, "content").Code; code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", label, code)
		}
	}
	// The store's credential screen covers the portal too, not just the curator.
	if code := call(t, handler, "PUT", "/api/file?name=STACK.md", bearer, "OPENAI_API_KEY=sk-proj-abc123def456ghi789jkl").Code; code != http.StatusBadRequest {
		t.Errorf("secret accepted: %d", code)
	}
	if !store.IsEmpty() {
		t.Fatal("a rejected request wrote memory")
	}
}

func TestWriteRejectsOversizedBody(t *testing.T) {
	handler, _ := newServer(t)
	body := strings.Repeat("a", maxBody+1)
	if code := call(t, handler, "PUT", "/api/file?name=STACK.md", "Bearer "+token, body).Code; code == http.StatusOK {
		t.Fatal("oversized body was accepted")
	}
}

func TestUnauthenticatedRoutesExposeNoMemory(t *testing.T) {
	handler, store := newServer(t)
	if _, err := store.Write("STACK.md", "- A stored preference.\n"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	for _, target := range []string{"/health", "/"} {
		response := call(t, handler, "GET", target, "", "")
		if response.Code != http.StatusOK {
			t.Errorf("%s: %d", target, response.Code)
		}
		if strings.Contains(response.Body.String(), "A stored preference") {
			t.Errorf("%s leaked memory content", target)
		}
	}
}
