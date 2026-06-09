package router

import (
	"testing"

	"spore/memorystore"
)

func TestParseMemoryUpdates(t *testing.T) {
	updates, err := parseMemoryUpdates("```json\n{\"updates\":[{\"file\":\"STACK.md\",\"content\":\"Go\"}]}\n```")
	if err != nil || len(updates) != 1 || updates[0].File != "STACK.md" || updates[0].Content != "Go" {
		t.Fatalf("parseMemoryUpdates = (%+v, %v)", updates, err)
	}
	updates, err = parseMemoryUpdates(`{"updates":[]}`)
	if err != nil || len(updates) != 0 {
		t.Errorf("empty updates = (%+v, %v)", updates, err)
	}
	if _, err = parseMemoryUpdates("sorry, nothing to store"); err == nil {
		t.Error("malformed response did not error")
	}
}

func TestMemoryModelPick(t *testing.T) {
	store, err := memorystore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &Router{store: store, smallModel: "small", oa: newOAClient("k", "", "good")}
	if got := r.memoryModel(); got != "small" {
		t.Errorf("empty memory should use the small model, got %q", got)
	}
	_ = store.Write("COMPANY.md", "Acme")
	if got := r.memoryModel(); got != "good" {
		t.Errorf("populated memory should use the good model, got %q", got)
	}
	r = &Router{oa: newOAClient("k", "", "good")}
	if got := r.memoryModel(); got != "good" {
		t.Errorf("nil store should use the good model, got %q", got)
	}
}
