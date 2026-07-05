package slackhandler

import (
	"testing"
	"time"
)

func TestAlreadySeen(t *testing.T) {
	h := &Handler{seen: make(map[string]time.Time)}
	if h.alreadySeen("Ev123") {
		t.Error("first delivery reported as duplicate")
	}
	if !h.alreadySeen("Ev123") {
		t.Error("second delivery not deduped")
	}
	for i := 0; i < 2; i++ {
		if h.alreadySeen("") {
			t.Error("empty event IDs must never dedupe")
		}
	}
	h.seen["EvOld"] = time.Now().Add(-2 * seenTTL)
	if h.alreadySeen("EvOld") {
		t.Error("expired entry should have been pruned and treated as new")
	}
}
