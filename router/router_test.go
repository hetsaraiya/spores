package router

import (
	"strings"
	"testing"
)

func TestRouterContextSkipsSystemAndEmpty(t *testing.T) {
	out := routerContext([]chatMessage{
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
	out := routerContext([]chatMessage{{Role: "user", Content: strings.Repeat("x", 5000)}})
	if !strings.Contains(out, "... [truncated]") {
		t.Error("long message was not truncated")
	}
	out = routerContext([]chatMessage{
		{Role: "user", Content: strings.Repeat("a", 4000)},
		{Role: "user", Content: strings.Repeat("b", 4000)},
		{Role: "user", Content: strings.Repeat("c", 4000)},
		{Role: "user", Content: strings.Repeat("d", 4000)},
	})
	if !strings.HasPrefix(out, "... [older context truncated]") {
		t.Error("overall context was not trimmed from the front")
	}
}

func TestHistoryMessagesLabelsSpeakers(t *testing.T) {
	msgs := historyMessages([]Turn{
		{Speaker: "Het", Text: "please fix the login bug"},
		{IsBot: true, Text: "Opened PR #42."},
		{Speaker: "Studio", Text: "also add tests"},
		{Speaker: "Nobody", Text: "   "}, // dropped: empty after trim
	})
	if len(msgs) != 3 {
		t.Fatalf("historyMessages len = %d, want 3: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" || msgs[0].Content != "Het: please fix the login bug" {
		t.Errorf("first turn = %+v, want named user turn", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "Opened PR #42." {
		t.Errorf("bot turn = %+v, want unlabeled assistant turn", msgs[1])
	}
	if msgs[2].Content != "Studio: also add tests" {
		t.Errorf("third turn = %+v, want named user turn", msgs[2])
	}
}

func TestHistoryMessagesTrimsAndClips(t *testing.T) {
	var turns []Turn
	for i := 0; i < maxMemoryMessages+5; i++ {
		turns = append(turns, Turn{Speaker: "Het", Text: "q"})
	}
	turns = append(turns, Turn{Speaker: "Het", Text: strings.Repeat("z", maxMemoryChars+100)})
	msgs := historyMessages(turns)
	if len(msgs) != maxMemoryMessages {
		t.Errorf("history length = %d, want %d", len(msgs), maxMemoryMessages)
	}
	if !strings.HasSuffix(msgs[len(msgs)-1].Content, "... [truncated]") {
		t.Error("oversized turn was not clipped")
	}
}
