package config

import (
	"strings"
	"testing"
)

// Load reads the process environment, so every case sets the required key.
func withEnv(t *testing.T, pairs map[string]string) {
	t.Helper()
	t.Setenv(envOpenAIAPIKey, "sk-test")
	for key, value := range pairs {
		t.Setenv(key, value)
	}
}

func TestLoadRequiresAnAPIKey(t *testing.T) {
	t.Setenv(envOpenAIAPIKey, "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), envOpenAIAPIKey) {
		t.Fatalf("missing key was accepted: %v", err)
	}
}

func TestPortalFailsClosedWithoutAToken(t *testing.T) {
	withEnv(t, map[string]string{envPortalEnabled: booleanTrue, envPortalToken: ""})
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), envPortalToken) {
		t.Fatalf("portal came up without a token: %v", err)
	}

	withEnv(t, map[string]string{envPortalEnabled: booleanTrue, envPortalToken: "   "})
	if _, err := Load(); err == nil {
		t.Fatal("a whitespace-only portal token was accepted")
	}

	withEnv(t, map[string]string{envPortalEnabled: booleanTrue, envPortalToken: "real-token"})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("valid portal config rejected: %v", err)
	}
	if !cfg.PortalEnabled || cfg.PortalToken != "real-token" {
		t.Fatalf("portal config not applied: %+v", cfg)
	}
}

// Comparing only against "off" meant any typo silently re-enabled curation.
func TestCurationModeRejectsUnrecognisedValues(t *testing.T) {
	enabled := map[string]bool{
		updateModeAlways: true,
		"ALWAYS":         true,
		" always ":       true,
		updateModeOff:    false,
		"OFF":            false,
	}
	for mode, want := range enabled {
		withEnv(t, map[string]string{envUpdateMode: mode})
		cfg, err := Load()
		if err != nil {
			t.Errorf("%q was rejected: %v", mode, err)
			continue
		}
		if cfg.CurateEnabled != want {
			t.Errorf("%q gave CurateEnabled=%t, want %t", mode, cfg.CurateEnabled, want)
		}
	}
	for _, mode := range []string{"disabled", "none", "0", "no"} {
		withEnv(t, map[string]string{envUpdateMode: mode})
		if _, err := Load(); err == nil {
			t.Errorf("%q was accepted and would silently enable curation", mode)
		}
	}
}

func TestDefaultsApplyWhenUnset(t *testing.T) {
	withEnv(t, map[string]string{
		envOpenAIBaseURL: "", envModel: "", envMemoryDir: "", envPortalAddr: "", envUpdateMode: "",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for label, got := range map[string][2]string{
		"base URL":    {cfg.OpenAIBaseURL, defaultOpenAIBaseURL},
		"model":       {cfg.Model, defaultModel},
		"memory dir":  {cfg.MemoryDir, defaultMemoryDir},
		"portal addr": {cfg.PortalAddr, defaultPortalAddr},
	} {
		if got[0] != got[1] {
			t.Errorf("%s = %q, want %q", label, got[0], got[1])
		}
	}
	if !cfg.CurateEnabled {
		t.Error("curation should default to enabled")
	}
}

func TestOwnerConfiguredIgnoresWhitespace(t *testing.T) {
	withEnv(t, map[string]string{envOwnerSlackID: "   "})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OwnerConfigured() {
		t.Fatal("a whitespace-only owner ID counted as configured")
	}

	withEnv(t, map[string]string{envOwnerSlackID: "U_OWNER"})
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.OwnerConfigured() || cfg.OwnerSlackID != "U_OWNER" {
		t.Fatalf("owner not applied: %+v", cfg)
	}
}

func TestPortalStaysDisabledUnlessExplicitlyTrue(t *testing.T) {
	for _, value := range []string{"", "1", "yes", "TRUE", "false"} {
		withEnv(t, map[string]string{envPortalEnabled: value})
		cfg, err := Load()
		if err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		if cfg.PortalEnabled {
			t.Errorf("%q enabled the portal; only %q should", value, booleanTrue)
		}
	}
}
