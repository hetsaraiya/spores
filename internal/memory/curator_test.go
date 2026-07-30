package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hetsaraiya/spores/internal/tools"
	"github.com/openai/openai-go/v3"
)

const preference = `{"file":"USER.md","content":"- Prefers short Slack replies.\n"}`

func TestApplyGatesUserFileOnOwner(t *testing.T) {
	store := newStore(t)
	curator := &Curator{store: store, owner: "U_OWNER"}

	result := curator.apply(Job{SpeakerID: "U_OTHER"}, tools.UpdateMemory, preference)
	if !strings.HasPrefix(result, "Refused") {
		t.Fatalf("non-owner write was not refused: %q", result)
	}
	if !store.IsEmpty() {
		t.Fatal("non-owner write reached disk")
	}

	if result := curator.apply(Job{SpeakerID: "U_OWNER"}, tools.UpdateMemory, preference); !strings.HasPrefix(result, "Saved") {
		t.Fatalf("owner write was refused: %q", result)
	}
	if store.IsEmpty() {
		t.Fatal("owner write did not reach disk")
	}
}

// With no owner configured the gate must fail closed. Allowing the write meant
// the most sensitive file was the only one anyone could overwrite.
func TestApplyRefusesUserFileWhenNoOwnerConfigured(t *testing.T) {
	store := newStore(t)
	curator := &Curator{store: store}
	if result := curator.apply(Job{SpeakerID: "U_ANY"}, tools.UpdateMemory, preference); !strings.HasPrefix(result, "Refused") {
		t.Fatalf("write accepted with no owner configured: %q", result)
	}
	if !store.IsEmpty() {
		t.Fatal("an unowned write reached disk")
	}
}

func TestApplyGateIsScopedToUserFile(t *testing.T) {
	store := newStore(t)
	curator := &Curator{store: store, owner: "U_OWNER"}
	args := `{"file":"STACK.md","content":"- Deploys are containers.\n"}`
	if result := curator.apply(Job{SpeakerID: "U_OTHER"}, tools.UpdateMemory, args); !strings.HasPrefix(result, "Saved") {
		t.Fatalf("non-owner STACK.md write was refused: %q", result)
	}
}

// Each case asserts the specific rejection, so a call failing for an unrelated
// reason no longer counts as a pass.
func TestApplyReportsStoreRejections(t *testing.T) {
	cases := []struct {
		label string
		tool  string
		args  string
		want  string
	}{
		{"invalid file", tools.UpdateMemory, `{"file":"COMPANY.txt","content":"anything"}`, "invalid memory file"},
		{"secret", tools.UpdateMemory, `{"file":"STACK.md","content":"OPENAI_API_KEY=sk-proj-abc123def456ghi789jkl"}`, "must never contain credentials"},
		{"oversized", tools.UpdateMemory, `{"file":"STACK.md","content":"` + strings.Repeat("a", maxFileBytes+1) + `"}`, "over the"},
		{"bad json", tools.UpdateMemory, `{"file":`, "invalid arguments"},
		{"unknown tool", "delete_everything", preference, "unknown tool"},
	}
	for _, test := range cases {
		t.Run(test.label, func(t *testing.T) {
			store := newStore(t)
			curator := &Curator{store: store, owner: "U_OWNER"}
			result := curator.apply(Job{SpeakerID: "U_OWNER"}, test.tool, test.args)
			if !strings.Contains(result, test.want) {
				t.Errorf("got %q, want it to mention %q", result, test.want)
			}
			if !store.IsEmpty() {
				t.Error("a rejected call still wrote memory")
			}
		})
	}
}

func TestApplyReportsNoOpWrites(t *testing.T) {
	store := newStore(t)
	curator := &Curator{store: store, owner: "U_OWNER"}
	job := Job{SpeakerID: "U_OWNER"}
	if result := curator.apply(job, tools.UpdateMemory, preference); !strings.HasPrefix(result, "Saved") {
		t.Fatalf("first write was not saved: %q", result)
	}
	if result := curator.apply(job, tools.UpdateMemory, preference); !strings.HasPrefix(result, "No change") {
		t.Fatalf("repeat write not reported as a no-op: %q", result)
	}
}

// writeOnce answers with a single update_memory call, then reports it is done.
func writeOnce(calls *atomic.Int32, file, content string) completionFunc {
	return func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		if calls.Add(1) > 1 {
			return &openai.ChatCompletion{Choices: []openai.ChatCompletionChoice{{}}}, nil
		}
		args := fmt.Sprintf(`{"file":%q,"content":%q}`, file, content)
		return &openai.ChatCompletion{Choices: []openai.ChatCompletionChoice{{
			Message: openai.ChatCompletionMessage{ToolCalls: []openai.ChatCompletionMessageToolCallUnion{{
				ID:       "call_1",
				Type:     "function",
				Function: openai.ChatCompletionMessageFunctionToolCallFunction{Name: tools.UpdateMemory, Arguments: args},
			}}},
		}}}, nil
	}
}

func newTestCurator(t *testing.T, store *Store, owner string, complete completionFunc) *Curator {
	t.Helper()
	curator := NewCurator(openai.Client{}, store, "model", owner, true)
	curator.complete = complete
	return curator
}

// Shutdown must process what is already queued, not just close the channel.
func TestShutdownDrainsQueuedJobs(t *testing.T) {
	store := newStore(t)
	var calls atomic.Int32
	curator := newTestCurator(t, store, "U_OWNER", writeOnce(&calls, stackFile, "- Prefer pnpm over npm.\n"))

	curator.Enqueue(Job{SpeakerID: "U_OWNER"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := curator.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if calls.Load() == 0 {
		t.Fatal("queued job was discarded instead of curated")
	}
	content, err := store.Read(stackFile)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(content, "pnpm") {
		t.Fatalf("drained job did not reach disk: %q", content)
	}
}

func TestDisabledCuratorDropsJobsWithoutWriting(t *testing.T) {
	store := newStore(t)
	var calls atomic.Int32
	curator := NewCurator(openai.Client{}, store, "model", "U_OWNER", false)
	curator.complete = writeOnce(&calls, stackFile, "- Should never be stored.\n")

	curator.Enqueue(Job{SpeakerID: "U_OWNER"})
	if err := curator.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("disabled curator ran the model")
	}
	if !store.IsEmpty() {
		t.Fatal("disabled curator wrote memory")
	}
}

// A second shutdown must not close the channel twice, and a late enqueue from an
// in-flight request must not send on it.
func TestShutdownIsIdempotentUnderConcurrentEnqueue(t *testing.T) {
	var calls atomic.Int32
	curator := newTestCurator(t, newStore(t), "U_OWNER", writeOnce(&calls, stackFile, "- Late.\n"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := curator.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	var group sync.WaitGroup
	for range 4 {
		group.Add(1)
		go func() {
			defer group.Done()
			curator.Enqueue(Job{})
			if err := curator.Shutdown(ctx); err != nil {
				t.Errorf("repeat Shutdown: %v", err)
			}
		}()
	}
	group.Wait()
}
