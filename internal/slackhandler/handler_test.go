package slackhandler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

func TestHistoryThreadKeepsContextSeparate(t *testing.T) {
	cases := map[string]struct {
		event *slackevents.AppMentionEvent
		want  string
	}{
		"in a thread":        {&slackevents.AppMentionEvent{TimeStamp: "200.1", ThreadTimeStamp: "100.0"}, "100.0"},
		"starts one":         {&slackevents.AppMentionEvent{TimeStamp: "200.1"}, "200.1"},
		"is a thread's root": {&slackevents.AppMentionEvent{TimeStamp: "100.0", ThreadTimeStamp: "100.0"}, "100.0"},
	}
	for label, test := range cases {
		if got := historyThread(test.event); got != test.want {
			t.Errorf("%s: got %q, want %q", label, got, test.want)
		}
	}
}

func TestStripMentionRemovesOnlyTheLeadingMention(t *testing.T) {
	cases := map[string]string{
		"<@U012AB3CD> deploy the bot": "deploy the bot",
		"  <@U012AB3CD>   spaced":     "spaced",
		"no mention here":             "no mention here",
		"hey <@U012AB3CD> mid-text":   "hey <@U012AB3CD> mid-text",
		"<@U012AB3CD>":                "",
	}
	for input, want := range cases {
		if got := stripMention(input); got != want {
			t.Errorf("stripMention(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestIsDuplicateSuppressesRedeliveries(t *testing.T) {
	h := &Handler{seen: make(map[string]time.Time)}
	if h.isDuplicate("Ev1") {
		t.Fatal("first delivery reported as duplicate")
	}
	if !h.isDuplicate("Ev1") {
		t.Fatal("redelivery was not suppressed")
	}
	if h.isDuplicate("Ev2") {
		t.Fatal("a different event was reported as duplicate")
	}
	// A blank ID carries no identity, so it must never suppress anything.
	for range 2 {
		if h.isDuplicate("") {
			t.Fatal("blank event ID was treated as a duplicate")
		}
	}
}

func TestIsDuplicateExpiresOldEntries(t *testing.T) {
	h := &Handler{seen: map[string]time.Time{"old": time.Now().Add(-2 * seenTTL)}}
	h.isDuplicate("new")
	if _, present := h.seen["old"]; present {
		t.Fatal("expired entry was not swept")
	}
}

func TestIsDuplicateIsSafeUnderConcurrency(t *testing.T) {
	h := &Handler{seen: make(map[string]time.Time)}
	var group sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for range 50 {
		group.Add(1)
		go func() {
			defer group.Done()
			if !h.isDuplicate("Ev1") {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	group.Wait()
	if accepted != 1 {
		t.Fatalf("%d goroutines accepted the same event, want 1", accepted)
	}
}

func TestErrorTextIsFlattenedAndBounded(t *testing.T) {
	err := errors.New("gateway failure:\n<html>\n  <body>502</body>\n</html>")
	got := errorText(err)
	if strings.Contains(got, "\n") {
		t.Fatalf("newlines survived: %q", got)
	}

	long := errorText(fmt.Errorf("%s", strings.Repeat("x", maxErrorChars*2)))
	if len(long) > maxErrorChars+len(ellipsis) {
		t.Fatalf("error text is %d bytes, over the %d-byte bound", len(long), maxErrorChars)
	}
	if !strings.HasSuffix(long, ellipsis) {
		t.Fatal("truncation was not marked")
	}
}

func TestCappedBufferRejectsOversizedDownloads(t *testing.T) {
	buffer := &cappedBuffer{limit: 10}
	if _, err := buffer.Write([]byte("12345")); err != nil {
		t.Fatalf("write within the limit failed: %v", err)
	}
	if _, err := buffer.Write([]byte("123456")); err == nil {
		t.Fatal("write past the limit was accepted")
	}
	if buffer.buf.Len() != 5 {
		t.Fatalf("buffer holds %d bytes, want the rejected write dropped", buffer.buf.Len())
	}
}

func TestReactionsRenderWithResolvedNames(t *testing.T) {
	h := &Handler{names: map[string]string{"U1": "Grace", "U2": "Linus"}}
	got := h.reactions(context.Background(), []slack.ItemReaction{
		{Name: "thumbsup", Count: 2, Users: []string{"U1", "U2"}},
	})
	for _, want := range []string{":thumbsup:", "×2", "Grace", "Linus"} {
		if !strings.Contains(got, want) {
			t.Errorf("reaction text %q is missing %q", got, want)
		}
	}
}

func TestReactionEventsNeverTriggerAgentRun(t *testing.T) {
	events := map[string]any{
		"added":   &slackevents.ReactionAddedEvent{},
		"removed": &slackevents.ReactionRemovedEvent{},
	}
	for label, event := range events {
		if mention, ok := mentionToRun(event); ok || mention != nil {
			t.Errorf("%s reaction was treated as an agent-triggering mention", label)
		}
	}

	want := &slackevents.AppMentionEvent{Text: "hello"}
	if mention, ok := mentionToRun(want); !ok || mention != want {
		t.Fatal("app mention no longer triggers the agent")
	}
}
