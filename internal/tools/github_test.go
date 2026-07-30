package tools

import (
	"context"
	"testing"
)

func TestIntArgAcceptsModelNumberShapes(t *testing.T) {
	cases := map[string]struct {
		value any
		want  int
		ok    bool
	}{
		"json float":     {float64(42), 42, true},
		"int":            {7, 7, true},
		"numeric string": {"42", 42, true},
		"padded string":  {" 42 ", 42, true},
		"word":           {"forty-two", 0, false},
		"absent":         {nil, 0, false},
		"bool":           {true, 0, false},
	}
	for label, test := range cases {
		got, ok := IntArg(test.value)
		if got != test.want || ok != test.ok {
			t.Errorf("%s: IntArg(%v) = %d,%t want %d,%t", label, test.value, got, ok, test.want, test.ok)
		}
	}
}

// A name in one list and not the other is either a tool the model can call that
// nothing implements, or an implementation the model can never reach.
func TestDefinitionsAndDispatchAgree(t *testing.T) {
	defined := make(map[string]bool)
	for _, definition := range GitHubDefinitions() {
		defined[definition.OfFunction.Function.Name] = true
	}
	for name := range defined {
		if _, dispatched := githubHandlers[name]; !dispatched {
			t.Errorf("%s is defined but not dispatched", name)
		}
	}
	for name := range githubHandlers {
		if !defined[name] {
			t.Errorf("%s is dispatched but not defined, so the model cannot call it", name)
		}
	}
}

func TestRunGitHubReportsUnknownTools(t *testing.T) {
	result, known, err := RunGitHub(context.Background(), nil, "github_delete_everything", map[string]any{})
	if known {
		t.Fatal("an undefined tool was dispatched")
	}
	if result != "" || err != nil {
		t.Fatalf("unknown tool returned result=%q err=%v", result, err)
	}
}

func TestRunGitHubValidatesTheNumberArgument(t *testing.T) {
	for label, args := range map[string]map[string]any{
		"missing": {"repo": "owner/repo"},
		"word":    {"repo": "owner/repo", "number": "twelve"},
		"bool":    {"repo": "owner/repo", "number": true},
	} {
		_, known, err := RunGitHub(context.Background(), nil, GitHubGetIssue, args)
		if !known {
			t.Fatalf("%s: tool was not recognised", label)
		}
		if err == nil {
			t.Errorf("%s: invalid number was accepted", label)
		}
	}
}

// Argument types come from argTypes, not from the argument's name.
func TestPropertiesUseDeclaredTypes(t *testing.T) {
	props := properties(argRepo, argNumber)
	if kind := props[argRepo].(map[string]any)["type"]; kind != typeString {
		t.Errorf("%s typed as %v", argRepo, kind)
	}
	if kind := props[argNumber].(map[string]any)["type"]; kind != typeInteger {
		t.Errorf("%s typed as %v", argNumber, kind)
	}
}

func TestPropertiesPanicsOnUndeclaredArgument(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("an undeclared argument was silently given a type")
		}
	}()
	properties("undeclared_argument")
}

func TestToolDefinitionsAreUniqueAndNamed(t *testing.T) {
	definitions := append(GitHubDefinitions(), DelegateDefinition(), MemorySearchDefinition())
	seen := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		name := definition.OfFunction.Function.Name
		if name == "" {
			t.Error("a tool was defined without a name")
		}
		if seen[name] {
			t.Errorf("%s is defined twice", name)
		}
		seen[name] = true
	}
	for _, required := range []string{DelegateToCoder, SearchMemory} {
		if !seen[required] {
			t.Errorf("%s is missing from the toolset", required)
		}
	}
	if _, present := seen[UpdateMemory]; present {
		t.Errorf("%s must stay out of the agent toolset; it is curation-only", UpdateMemory)
	}
}
