package codexauth

import (
	"errors"
	"os"
	"testing"
)

func TestUseRetainsRefreshedCredentials(t *testing.T) {
	const initial = `{"auth_mode":"chatgpt","last_refresh":"old"}`
	const refreshed = `{"auth_mode":"chatgpt","last_refresh":"new"}`
	t.Setenv(environmentKey, initial)
	credentials := NewFromEnvironment()

	if err := credentials.Use(func(authJSON string) (string, error) {
		if authJSON != initial {
			t.Fatalf("first use received %q, want initial credentials", authJSON)
		}
		return refreshed, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := credentials.Use(func(authJSON string) (string, error) {
		if authJSON != refreshed {
			t.Fatalf("second use received %q, want refreshed credentials", authJSON)
		}
		return authJSON, nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(environmentKey); got != refreshed {
		t.Fatalf("%s = %q, want refreshed credentials", environmentKey, got)
	}
}

func TestUseRejectsInvalidRefreshedJSON(t *testing.T) {
	const initial = `{"auth_mode":"chatgpt"}`
	t.Setenv(environmentKey, initial)
	credentials := NewFromEnvironment()

	err := credentials.Use(func(string) (string, error) { return "not json at all", nil })
	if err == nil {
		t.Fatal("invalid refreshed credentials were retained")
	}
	if got := os.Getenv(environmentKey); got != initial {
		t.Fatalf("%s was overwritten with invalid JSON: %q", environmentKey, got)
	}
}

// The run's own error wins: a refresh problem must not mask why the run failed.
func TestUsePropagatesTheRunError(t *testing.T) {
	t.Setenv(environmentKey, `{"auth_mode":"chatgpt"}`)
	credentials := NewFromEnvironment()

	runErr := errors.New("sandbox exploded")
	if err := credentials.Use(func(string) (string, error) { return "not json", runErr }); !errors.Is(err, runErr) {
		t.Fatalf("got %v, want the run error", err)
	}
}

func TestUseKeepsCredentialsWhenUnchanged(t *testing.T) {
	const initial = `{"auth_mode":"chatgpt"}`
	t.Setenv(environmentKey, initial)
	credentials := NewFromEnvironment()

	if err := credentials.Use(func(authJSON string) (string, error) { return authJSON, nil }); err != nil {
		t.Fatalf("Use: %v", err)
	}
	if err := credentials.Use(func(string) (string, error) { return "", nil }); err != nil {
		t.Fatalf("Use with a blank refresh: %v", err)
	}
	if got := os.Getenv(environmentKey); got != initial {
		t.Fatalf("%s = %q, want the original credentials", environmentKey, got)
	}
}

func TestConfiguredReportsAvailability(t *testing.T) {
	t.Setenv(environmentKey, "")
	if NewFromEnvironment().Configured() {
		t.Error("blank credentials reported as configured")
	}
	t.Setenv(environmentKey, "   ")
	if NewFromEnvironment().Configured() {
		t.Error("whitespace-only credentials reported as configured")
	}
	t.Setenv(environmentKey, `{"auth_mode":"chatgpt"}`)
	if !NewFromEnvironment().Configured() {
		t.Error("valid credentials reported as unavailable")
	}
	// main.go always passes a value, but Run's guard dereferences it first.
	var absent *Credentials
	if absent.Configured() {
		t.Error("a nil Credentials reported as configured")
	}
}
