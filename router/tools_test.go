package router

import (
	"context"
	"strings"
	"testing"
)

func TestToolArgs(t *testing.T) {
	args := toolArgs{"repo": "foo/bar", "number": float64(7), "count": 3}
	if got := args.str("repo"); got != "foo/bar" {
		t.Errorf("str(repo) = %q", got)
	}
	if got := args.str("missing"); got != "" {
		t.Errorf("str(missing) = %q", got)
	}
	if got := args.intVal("number"); got != 7 {
		t.Errorf("intVal(number) = %d", got)
	}
	if got := args.intVal("count"); got != 3 {
		t.Errorf("intVal(count) = %d", got)
	}
	if got := args.intVal("missing"); got != 0 {
		t.Errorf("intVal(missing) = %d", got)
	}
}

func TestDispatchUnknownTool(t *testing.T) {
	r := &Router{}
	result, delegated, err := r.dispatch(context.Background(), "nope", "{}", "")
	if err != nil || delegated {
		t.Fatalf("dispatch = (%q, %t, %v)", result, delegated, err)
	}
	if !strings.Contains(result, "unknown tool") {
		t.Errorf("result = %q, want unknown tool message", result)
	}
}

func TestDispatchInvalidArguments(t *testing.T) {
	r := &Router{}
	result, delegated, err := r.dispatch(context.Background(), "github_get_file", "{not json", "")
	if err != nil || delegated {
		t.Fatalf("dispatch = (%q, %t, %v)", result, delegated, err)
	}
	if !strings.Contains(result, "invalid arguments") {
		t.Errorf("result = %q, want invalid arguments message", result)
	}
}
