package slackhandler

import (
	"testing"
	"time"
)

func TestStripMention(t *testing.T) {
	cases := []struct{ in, want string }{
		{"<@U12345> do the thing", "do the thing"},
		{"  <@U12345>   do the thing", "do the thing"},
		{"no mention here", "no mention here"},
	}
	for _, c := range cases {
		if got := stripMention(c.in); got != c.want {
			t.Errorf("stripMention(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAlreadySeen(t *testing.T) {
	h := &Handler{seen: make(map[string]time.Time)}
	if h.alreadySeen("Ev123") {
		t.Error("first delivery reported as duplicate")
	}
	if !h.alreadySeen("Ev123") {
		t.Error("second delivery not deduped")
	}
	if h.alreadySeen("") || h.alreadySeen("") {
		t.Error("empty event IDs must never dedupe")
	}
	h.seen["EvOld"] = time.Now().Add(-2 * seenTTL)
	if h.alreadySeen("EvOld") {
		t.Error("expired entry should have been pruned and treated as new")
	}
}
