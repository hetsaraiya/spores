package jina

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReaderSendsURLAndReturnsContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer jina-test" {
			t.Errorf("Authorization = %q", got)
		}
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["url"] != "https://example.com/article" {
			t.Errorf("url = %q", body["url"])
		}
		_, _ = w.Write([]byte("# Clean article"))
	}))
	defer server.Close()

	client := newClient(server.Client(), "jina-test", server.URL, server.URL)
	got, err := client.Read(context.Background(), "https://example.com/article")
	if err != nil {
		t.Fatal(err)
	}
	if got != "# Clean article" {
		t.Fatalf("Read = %q", got)
	}
}

func TestSearchRequiresAPIKey(t *testing.T) {
	client := newClient(http.DefaultClient, "", "unused", "unused")
	if _, err := client.Search(context.Background(), "spores"); err == nil {
		t.Fatal("search without an API key succeeded")
	}
}

func TestSearchSendsQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["q"] != "latest Go release" {
			t.Errorf("q = %q", body["q"])
		}
		_, _ = w.Write([]byte("search results"))
	}))
	defer server.Close()

	client := newClient(server.Client(), "jina-test", server.URL, server.URL)
	got, err := client.Search(context.Background(), "latest Go release")
	if err != nil {
		t.Fatal(err)
	}
	if got != "search results" {
		t.Fatalf("Search = %q", got)
	}
}
