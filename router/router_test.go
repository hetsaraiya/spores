package router

import (
	"strings"
	"testing"
)

func TestRouterContextSkipsSystemAndEmpty(t *testing.T) {
	out := routerContext([]oaMessage{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: ""},
		{Role: "tool", Content: "tool result"},
	})
	if strings.Contains(out, "system prompt") {
		t.Error("routerContext included the system prompt")
	}
	if !strings.Contains(out, "USER:\nhello") || !strings.Contains(out, "TOOL:\ntool result") {
		t.Errorf("routerContext missing expected content:\n%s", out)
	}
}

func TestRouterContextTruncatesLongMessages(t *testing.T) {
	out := routerContext([]oaMessage{{Role: "user", Content: strings.Repeat("x", 5000)}})
	if !strings.Contains(out, "... [truncated]") {
		t.Error("long message was not truncated")
	}
	out = routerContext([]oaMessage{
		{Role: "user", Content: strings.Repeat("a", 4000)},
		{Role: "user", Content: strings.Repeat("b", 4000)},
		{Role: "user", Content: strings.Repeat("c", 4000)},
		{Role: "user", Content: strings.Repeat("d", 4000)},
	})
	if !strings.HasPrefix(out, "... [older context truncated]") {
		t.Error("overall context was not trimmed from the front")
	}
}

func TestMemoryRememberRecall(t *testing.T) {
	r := &Router{}
	if got := r.recall("chan"); len(got) != 0 {
		t.Fatalf("recall on empty memory = %d messages", len(got))
	}
	r.remember("chan", "question", "answer")
	history := r.recall("chan")
	if len(history) != 2 || history[0].Content != "question" || history[1].Content != "answer" {
		t.Fatalf("unexpected history: %+v", history)
	}
	if got := r.recall("other"); len(got) != 0 {
		t.Errorf("conversations leaked across IDs: %+v", got)
	}
}

func TestMemoryTrimsOldTurns(t *testing.T) {
	r := &Router{}
	for i := 0; i < maxMemoryMessages; i++ {
		r.remember("chan", "q", "a")
	}
	history := r.recall("chan")
	if len(history) != maxMemoryMessages {
		t.Errorf("history length = %d, want %d", len(history), maxMemoryMessages)
	}
	r.remember("chan", strings.Repeat("z", maxMemoryChars+100), "a")
	history = r.recall("chan")
	last := history[len(history)-2].Content
	if !strings.HasSuffix(last, "... [truncated]") {
		t.Error("oversized remembered message was not clipped")
	}
}
