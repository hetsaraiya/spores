package coder

import (
	"strings"
	"testing"
)

type fakeMemory struct{ section string }

func (f fakeMemory) BriefSection() string { return f.section }

func TestBriefAppendsMemoryToTask(t *testing.T) {
	delegate := New(Config{}, nil, fakeMemory{section: "<personal-memory>\n- Prefer pnpm.\n</personal-memory>"})
	brief := delegate.brief("Fix the failing test in acme/web.")

	if !strings.HasPrefix(brief, "Fix the failing test in acme/web.") {
		t.Fatal("task was not preserved at the head of the brief")
	}
	if !strings.Contains(brief, "Prefer pnpm") {
		t.Fatal("memory did not reach the coding-agent brief")
	}
}

func TestBriefIsUnchangedWithoutMemory(t *testing.T) {
	task := "Fix the failing test in acme/web."
	for label, delegate := range map[string]*Delegate{
		"nil store":     New(Config{}, nil, nil),
		"empty section": New(Config{}, nil, fakeMemory{}),
	} {
		if brief := delegate.brief(task); brief != task {
			t.Errorf("%s: brief was modified: %q", label, brief)
		}
	}
}
