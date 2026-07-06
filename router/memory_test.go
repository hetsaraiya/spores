package router

import (
	"testing"

	"spore/memorystore"
)

func TestApplyMemoryCall(t *testing.T) {
	store, err := memorystore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &Router{store: store}

	if got := r.applyMemoryCall("update_memory", `{"file":"STACK.md","content":"Go"}`); got != "Saved STACK.md." {
		t.Errorf("first write = %q", got)
	}
	if content, _ := store.Read("STACK.md"); content != "Go" {
		t.Errorf("STACK.md content = %q", content)
	}
	// Re-emitting the same content is a guarded no-op.
	if got := r.applyMemoryCall("update_memory", `{"file":"STACK.md","content":"Go"}`); got == "Saved STACK.md." {
		t.Errorf("unchanged write should be skipped, got %q", got)
	}
	// Invalid file name surfaces an error result rather than writing.
	if got := r.applyMemoryCall("update_memory", `{"file":"../escape.md","content":"x"}`); got == "Saved ../escape.md." {
		t.Errorf("invalid name should not be saved, got %q", got)
	}
	// Malformed arguments are reported, not panicked on.
	if got := r.applyMemoryCall("update_memory", `{not json`); got == "" {
		t.Error("malformed arguments returned empty result")
	}
	if got := r.applyMemoryCall("noop_tool", `{}`); got == "" {
		t.Error("unknown tool returned empty result")
	}
}

func TestMemoryModelPick(t *testing.T) {
	store, err := memorystore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &Router{store: store, smallModel: "small", llm: newLLMClient("k", "", "good", nil)}
	if got := r.memoryModel(); got != "small" {
		t.Errorf("empty memory should use the small model, got %q", got)
	}
	_ = store.Write("STACK.md", "Go")
	if got := r.memoryModel(); got != "good" {
		t.Errorf("populated memory should use the good model, got %q", got)
	}
	r = &Router{llm: newLLMClient("k", "", "good", nil)}
	if got := r.memoryModel(); got != "good" {
		t.Errorf("nil store should use the good model, got %q", got)
	}
}
