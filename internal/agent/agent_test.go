package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hetsaraiya/spores/internal/memory"
	"github.com/hetsaraiya/spores/internal/tools"
	"github.com/openai/openai-go/v3"
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
	message := agent.systemMessage("")

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
	if message := agent.systemMessage(""); message != systemPrompt {
		t.Fatalf("empty memory added a prompt block: %q", strings.TrimPrefix(message, systemPrompt))
	}
}

func TestSystemMessageAddsSlackFormattingOnlyWhenRequested(t *testing.T) {
	agent := &Agent{memory: storeWith(t, "", "")}

	slackMessage := agent.systemMessage(ResponseFormatSlack)
	for _, expected := range []string{"*bold*", "_italics_", "Do not use Markdown headings", "or tables"} {
		if !strings.Contains(slackMessage, expected) {
			t.Errorf("Slack system message omitted %q", expected)
		}
	}

	defaultMessage := agent.systemMessage("")
	if strings.Contains(defaultMessage, slackFormattingPrompt) {
		t.Fatal("Slack formatting instructions leaked into the default response format")
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

// Scoped to userMessage's own encoding. Building the reaction text is the Slack
// handler's job and is tested there; this only asserts it survives encoding.
func TestUserMessageEncodesSpeakerImagesAndText(t *testing.T) {
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

// alwaysCallsTool mimics a model stuck in a tool loop.
func alwaysCallsTool(calls *atomic.Int32, sawTools *atomic.Bool) completionFunc {
	return func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		calls.Add(1)
		if len(params.Tools) == 0 {
			sawTools.Store(true)
			return &openai.ChatCompletion{Choices: []openai.ChatCompletionChoice{{
				Message: openai.ChatCompletionMessage{Content: "final answer"},
			}}}, nil
		}
		return &openai.ChatCompletion{Choices: []openai.ChatCompletionChoice{{
			Message: openai.ChatCompletionMessage{ToolCalls: []openai.ChatCompletionMessageToolCallUnion{{
				ID:       "call_1",
				Type:     "function",
				Function: openai.ChatCompletionMessageFunctionToolCallFunction{Name: tools.SearchMemory, Arguments: `{"query":"anything"}`},
			}}},
		}}}, nil
	}
}

func newTestAgent(t *testing.T, complete completionFunc) *Agent {
	t.Helper()
	return &Agent{
		completions: complete,
		memory:      storeWith(t, "", ""),
		curator:     &memory.Curator{},
		owner:       "U_OWNER",
		tools:       tools.GitHubDefinitions(),
	}
}

// Without a cap this never returns; the guard must stop and still answer.
func TestRunStopsAtTheToolBudget(t *testing.T) {
	var calls atomic.Int32
	var forcedFinal atomic.Bool
	agent := newTestAgent(t, alwaysCallsTool(&calls, &forcedFinal))

	done := make(chan struct{})
	var result string
	var err error
	go func() {
		defer close(done)
		result, err = agent.Run(context.Background(), Request{Message: "list repos", SpeakerID: "U_OWNER"})
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not terminate: the tool loop is unbounded")
	}

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "final answer" {
		t.Fatalf("got %q, want the forced final answer", result)
	}
	if !forcedFinal.Load() {
		t.Fatal("the budget was hit without a final tool-free call")
	}
	// maxToolTurns tool-bearing calls, then one without tools.
	if got := int(calls.Load()); got != maxToolTurns+1 {
		t.Fatalf("made %d model calls, want %d", got, maxToolTurns+1)
	}
}

func TestRunReturnsTheReplyWhenNoToolsAreCalled(t *testing.T) {
	agent := newTestAgent(t, func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		return &openai.ChatCompletion{Choices: []openai.ChatCompletionChoice{{
			Message: openai.ChatCompletionMessage{Content: "hello"},
		}}}, nil
	})
	result, err := agent.Run(context.Background(), Request{Message: "hi", SpeakerID: "U_OWNER"})
	if err != nil || result != "hello" {
		t.Fatalf("got %q err=%v", result, err)
	}
}

func TestRunFailsWhenTheModelReturnsNoChoices(t *testing.T) {
	agent := newTestAgent(t, func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		return &openai.ChatCompletion{}, nil
	})
	if _, err := agent.Run(context.Background(), Request{Message: "hi"}); err == nil {
		t.Fatal("an empty completion was treated as a reply")
	}
}

func TestExecuteToolRejectsMalformedArguments(t *testing.T) {
	agent := newTestAgent(t, nil)
	result := agent.executeTool(context.Background(), "U_OWNER", tools.SearchMemory, `{"query":`)
	if !strings.Contains(result, "invalid tool arguments") {
		t.Fatalf("got %q", result)
	}
}

func TestExecuteToolReportsUnknownTools(t *testing.T) {
	agent := newTestAgent(t, nil)
	result := agent.executeTool(context.Background(), "U_OWNER", "definitely_not_a_tool", `{}`)
	if !strings.Contains(result, "unknown tool") {
		t.Fatalf("got %q", result)
	}
}
