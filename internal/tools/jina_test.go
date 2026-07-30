package tools

import "testing"

func TestJinaDefinitionsAndDispatchAgree(t *testing.T) {
	defined := make(map[string]bool)
	for _, definition := range JinaDefinitions() {
		defined[definition.OfFunction.Function.Name] = true
	}
	for name := range defined {
		if _, dispatched := jinaHandlers[name]; !dispatched {
			t.Errorf("%s is defined but not dispatched", name)
		}
	}
	for name := range jinaHandlers {
		if !defined[name] {
			t.Errorf("%s is dispatched but not defined", name)
		}
	}
}
