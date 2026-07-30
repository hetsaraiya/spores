package coder

import (
	"strings"
	"testing"
)

type fakeMemory struct{ section string }

func (f fakeMemory) BriefSection() string { return f.section }

func TestBriefAppendsMemoryOnlyWhenPresent(t *testing.T) {
	const task = "Fix the failing test in acme/web."
	cases := map[string]struct {
		memory Memory
		want   string
	}{
		"with memory":   {fakeMemory{section: "<personal-memory>\n- Prefer pnpm.\n</personal-memory>"}, task + "\n\n<personal-memory>\n- Prefer pnpm.\n</personal-memory>"},
		"empty section": {fakeMemory{}, task},
		"nil store":     {nil, task},
	}
	for label, test := range cases {
		got := New(Config{}, nil, test.memory).brief(task)
		if got != test.want {
			t.Errorf("%s: brief = %q, want %q", label, got, test.want)
		}
		if !strings.HasPrefix(got, task) {
			t.Errorf("%s: task was not preserved at the head", label)
		}
	}
}
