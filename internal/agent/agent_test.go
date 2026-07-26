package agent

import (
	"strings"
	"testing"

	"github.com/hetsaraiya/spores/internal/memory"
)

func storeWith(t *testing.T, name, content string) *memory.Store {
	t.Helper()
	store, err := memory.New(t.TempDir())
	if err != nil {
		t.Fatalf("memory.New: %v", err)
	}
	if content != "" {
		if _, err := store.Write(name, content); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	return store
}

func TestSystemMessageInjectsMemory(t *testing.T) {
	agent := &Agent{memory: storeWith(t, "STACK.md", "- Deploys are containers on a self-hosted host.\n")}
	message := agent.systemMessage()

	if !strings.Contains(message, systemPrompt) {
		t.Fatal("base system prompt was dropped")
	}
	if !strings.Contains(message, "self-hosted host") {
		t.Fatal("memory did not reach the system prompt")
	}
	if !strings.Contains(message, "<personal-memory>") || !strings.Contains(message, "</personal-memory>") {
		t.Fatal("memory was injected without delimiters")
	}
	// Without this, a stale stored preference can outrank what the user just said.
	if !strings.Contains(message, "the current message wins") {
		t.Fatal("precedence rule missing from the memory preamble")
	}
}

func TestSystemMessageOmitsBlockWhenMemoryIsEmpty(t *testing.T) {
	agent := &Agent{memory: storeWith(t, "", "")}
	if message := agent.systemMessage(); message != systemPrompt {
		t.Fatalf("empty memory added a prompt block: %q", strings.TrimPrefix(message, systemPrompt))
	}
}
