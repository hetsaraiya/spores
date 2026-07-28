package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hetsaraiya/spores/internal/memory"
	"github.com/hetsaraiya/spores/internal/tools"
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

func TestExecuteToolSearchesMemory(t *testing.T) {
	store := storeWith(t, "profile/infrastructure.md", "The Mac Mini kernel is pinned for Broadcom WiFi compatibility.")
	agent := &Agent{memory: store, owner: "U_OWNER"}

	result := agent.executeTool(context.Background(), "U_OWNER", tools.SearchMemory, `{"query":"Mac Mini kernel","limit":2}`)
	if !strings.Contains(result, "profile/infrastructure.md") || !strings.Contains(result, "kernel is pinned") {
		t.Fatalf("search_memory returned %q", result)
	}
}

func TestExecuteToolRefusesMemorySearchForNonOwner(t *testing.T) {
	agent := &Agent{memory: storeWith(t, "profile/people.md", "Private relationship details."), owner: "U_OWNER"}
	result := agent.executeTool(context.Background(), "U_OTHER", tools.SearchMemory, `{"query":"relationship"}`)
	if !strings.Contains(result, "only to the configured owner") {
		t.Fatalf("non-owner search returned %q", result)
	}
}

func TestUserMessageIncludesImagesAndReactions(t *testing.T) {
	message := userMessage("Ada", "Looks good\nReaction: :thumbsup: ×2 by Grace, Linus",
		[]string{"data:image/png;base64,cG5n"})

	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	body := string(encoded)
	for _, expected := range []string{`Ada: Looks good`, `:thumbsup: ×2 by Grace, Linus`,
		`"type":"image_url"`, `data:image/png;base64,cG5n`} {
		if !strings.Contains(body, expected) {
			t.Errorf("message omitted %q: %s", expected, body)
		}
	}
}
