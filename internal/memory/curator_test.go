package memory

import (
	"context"
	"strings"
	"sync"
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

func TestApplyAllowsAnyoneWhenNoOwnerConfigured(t *testing.T) {
	store := newStore(t)
	curator := &Curator{store: store}
	if result := curator.apply(Job{SpeakerID: "U_ANY"}, tools.UpdateMemory, preference); !strings.HasPrefix(result, "Saved") {
		t.Fatalf("write refused with no owner configured: %q", result)
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

func TestApplyReportsStoreRejections(t *testing.T) {
	store := newStore(t)
	curator := &Curator{store: store}
	cases := map[string]string{
		"invalid file": `{"file":"COMPANY.md","content":"anything"}`,
		"secret":       `{"file":"STACK.md","content":"OPENAI_API_KEY=sk-proj-abc123def456ghi789jkl"}`,
		"bad json":     `{"file":`,
		"unknown tool": preference,
	}
	for label, args := range cases {
		name := tools.UpdateMemory
		if label == "unknown tool" {
			name = "delete_everything"
		}
		result := curator.apply(Job{}, name, args)
		if !strings.Contains(result, "error") && !strings.Contains(result, "invalid") && !strings.Contains(result, "unknown") {
			t.Errorf("%s: expected a rejection, got %q", label, result)
		}
	}
	if !store.IsEmpty() {
		t.Fatal("a rejected call still wrote memory")
	}
}

func TestApplyReportsNoOpWrites(t *testing.T) {
	store := newStore(t)
	curator := &Curator{store: store}
	curator.apply(Job{}, tools.UpdateMemory, preference)
	if result := curator.apply(Job{}, tools.UpdateMemory, preference); !strings.HasPrefix(result, "No change") {
		t.Fatalf("repeat write not reported as a no-op: %q", result)
	}
}

func TestDisabledCuratorAcceptsAndDropsJobs(t *testing.T) {
	curator := NewCurator(openai.Client{}, newStore(t), "model", "", false)
	curator.Enqueue(Job{SpeakerID: "U_OWNER"})
	if err := curator.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestShutdownDrainsAndIsIdempotent(t *testing.T) {
	curator := NewCurator(openai.Client{}, newStore(t), "model", "", true)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := curator.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	// A second shutdown must not close the channel twice, and a late enqueue
	// from an in-flight request must not send on it.
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
