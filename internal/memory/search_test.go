package memory

import (
	"fmt"
	"strings"
	"testing"
)

func TestSearchFindsRelevantProfileWithoutDumpingWholeFile(t *testing.T) {
	store := newStore(t)
	mustWrite(t, store, "profile/infrastructure.md", strings.Join([]string{
		"# Infrastructure",
		"",
		"## Mac Mini",
		"",
		"Kernel pinned to 6.14 because the Broadcom WiFi driver fails on newer kernels.",
		"",
		"## VPS",
		"",
		strings.Repeat("Unrelated deployment detail. ", 80),
	}, "\n"))

	results, err := store.Search("Mac Mini kernel", 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].File != "profile/infrastructure.md" {
		t.Fatalf("wrong file: %s", results[0].File)
	}
	if !strings.Contains(results[0].Snippet, "Kernel pinned") {
		t.Fatalf("matching detail absent: %q", results[0].Snippet)
	}
	if len(results[0].Snippet) > maxSearchSnippet+len("…") {
		t.Fatalf("snippet is unbounded: %d bytes", len(results[0].Snippet))
	}
	if strings.Contains(results[0].Snippet, "Unrelated deployment detail") {
		t.Fatal("search dumped unrelated sections")
	}
}

func TestSearchRanksExactPhraseAndHonorsLimit(t *testing.T) {
	store := newStore(t)
	mustWrite(t, store, "profile/people.md", "Works directly with Zaher Minkara on DigitalYou.")
	mustWrite(t, store, "profile/projects.md", "DigitalYou is an AI-avatar application. Zaher owns it.")
	mustWrite(t, store, "NOTES.md", "Zaher was mentioned in a note.")

	results, err := store.Search("Zaher Minkara", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].File != "profile/people.md" {
		t.Fatalf("unexpected ranking: %+v", results)
	}
}

func TestSearchRejectsEmptyQueryAndCapsResults(t *testing.T) {
	store := newStore(t)
	if _, err := store.Search(" -- ", 3); err == nil {
		t.Fatal("empty query accepted")
	}
	for index := range 8 {
		mustWrite(t, store, fmt.Sprintf("note-%d.md", index), "Shared searchable phrase.")
	}
	results, err := store.Search("searchable", 100)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != maxSearchLimit {
		t.Fatalf("got %d results, want cap %d", len(results), maxSearchLimit)
	}
}
