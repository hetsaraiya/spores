package langsmith

import (
	"context"
	"net/http"
	"testing"
)

// A disabled tracer (and a nil one) must be a complete no-op: Start returns the
// context unchanged with a nil Run, WrapHTTPClient passes the client through
// untouched, and every method is panic-free.
func TestDisabledTracerIsNoOp(t *testing.T) {
	for _, tr := range []*Tracer{nil, New("", "spore")} {
		if tr.Enabled() {
			t.Fatal("tracer without a key should be disabled")
		}
		ctx := context.Background()
		got, run := tr.Start(ctx, "x", "chain", map[string]any{"a": 1})
		if got != ctx {
			t.Error("disabled Start should return the context unchanged")
		}
		if run != nil {
			t.Error("disabled Start should return a nil Run")
		}
		run.End(map[string]any{"b": 2}, nil) // must not panic on nil Run

		base := &http.Client{}
		if wrapped := tr.WrapHTTPClient(base); wrapped != base || wrapped.Transport != nil {
			t.Error("disabled WrapHTTPClient should return the client untouched")
		}
		if tr.WrapHTTPClient(nil) == nil {
			t.Error("WrapHTTPClient(nil) must still return a usable client")
		}
		tr.Shutdown(ctx) // must not panic when disabled
	}
}
