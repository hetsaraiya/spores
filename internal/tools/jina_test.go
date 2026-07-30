package tools

import (
	"context"
	"testing"
)

type fakeJinaClient struct {
	searchQuery string
}

func (f *fakeJinaClient) Read(_ context.Context, targetURL string) (string, error) {
	return "read: " + targetURL, nil
}

func (f *fakeJinaClient) Search(_ context.Context, query string) (string, error) {
	f.searchQuery = query
	return "search results", nil
}

func TestJinaDefinitionsAndDispatchAgree(t *testing.T) {
	defined := make(map[string]bool)
	for _, definition := range JinaDefinitions() {
		defined[definition.OfFunction.Function.Name] = true
	}
	for name := range defined {
		if _, dispatched := jinaHandlers[name]; !dispatched {
			t.Errorf("%s is defined but not dispatched", name)
		}
	}
	for name := range jinaHandlers {
		if !defined[name] {
			t.Errorf("%s is dispatched but not defined", name)
		}
	}
}

func TestRunJinaWebSearchUsesInjectedClient(t *testing.T) {
	client := &fakeJinaClient{}

	got, known, err := RunJina(context.Background(), client, JinaWebSearch, map[string]any{
		argQuery: "latest Go release",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !known {
		t.Fatal("jina web search was not recognized")
	}
	if got != "search results" {
		t.Fatalf("RunJina = %q", got)
	}
	if client.searchQuery != "latest Go release" {
		t.Fatalf("search query = %q", client.searchQuery)
	}
}
