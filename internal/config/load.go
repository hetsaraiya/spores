// Package config loads runtime configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

const (
	envOpenAIAPIKey  = "OPENAI_API_KEY"
	envOpenAIBaseURL = "OPENAI_BASE_URL"
	envModel         = "MODEL"
	envGitHubToken   = "GITHUB_TOKEN"
	envJinaAPIKey    = "JINA_API_KEY"
	envSlackBotToken = "SLACK_BOT_TOKEN"
	envSlackAppToken = "SLACK_APP_TOKEN"

	envE2BAPIKey     = "E2B_API_KEY"
	envE2BTemplateID = "E2B_TEMPLATE_ID"
	envCodexModel    = "CODEX_MODEL"
	envCodexVersion  = "CODEX_VERSION"

	envMemoryDir    = "MEMORY_DIR"
	envOwnerSlackID = "OWNER_SLACK_USER_ID"
	envUpdateMode   = "MEMORY_UPDATE_MODE"

	envPortalEnabled = "PORTAL_ENABLED"
	envPortalAddr    = "PORTAL_ADDR"
	envPortalToken   = "PORTAL_TOKEN"
)

const (
	defaultOpenAIBaseURL = "https://api.openai.com/v1"
	defaultModel         = "gpt-5.5"
	defaultMemoryDir     = "./memory"
	defaultPortalAddr    = ":8080"

	updateModeAlways = "always"
	updateModeOff    = "off"

	booleanTrue = "true"
)

type Config struct {
	OpenAIAPIKey  string
	OpenAIBaseURL string
	Model         string
	GitHubToken   string
	JinaAPIKey    string
	SlackBotToken string
	SlackAppToken string

	// Coding-agent configuration is validated only when delegation is used.
	E2BAPIKey     string
	E2BTemplateID string
	CodexModel    string
	CodexVersion  string

	MemoryDir     string
	OwnerSlackID  string
	CurateEnabled bool

	PortalEnabled bool
	PortalAddr    string
	PortalToken   string
}

func Load() (Config, error) {
	_ = godotenv.Load()
	cfg := Config{
		OpenAIAPIKey:  os.Getenv(envOpenAIAPIKey),
		OpenAIBaseURL: valueOr(envOpenAIBaseURL, defaultOpenAIBaseURL),
		Model:         valueOr(envModel, defaultModel),
		GitHubToken:   os.Getenv(envGitHubToken),
		JinaAPIKey:    strings.TrimSpace(os.Getenv(envJinaAPIKey)),
		SlackBotToken: os.Getenv(envSlackBotToken),
		SlackAppToken: os.Getenv(envSlackAppToken),
		E2BAPIKey:     os.Getenv(envE2BAPIKey),
		E2BTemplateID: os.Getenv(envE2BTemplateID),
		CodexModel:    os.Getenv(envCodexModel),
		CodexVersion:  strings.TrimSpace(os.Getenv(envCodexVersion)),

		MemoryDir:    valueOr(envMemoryDir, defaultMemoryDir),
		OwnerSlackID: strings.TrimSpace(os.Getenv(envOwnerSlackID)),

		PortalEnabled: os.Getenv(envPortalEnabled) == booleanTrue,
		PortalAddr:    valueOr(envPortalAddr, defaultPortalAddr),
		PortalToken:   os.Getenv(envPortalToken),
	}

	curate, err := curationEnabled(valueOr(envUpdateMode, updateModeAlways))
	if err != nil {
		return Config{}, err
	}
	cfg.CurateEnabled = curate

	if cfg.OpenAIAPIKey == "" {
		return Config{}, fmt.Errorf("%s is required", envOpenAIAPIKey)
	}
	// Fail closed: the portal edits what the agent believes.
	if cfg.PortalEnabled && strings.TrimSpace(cfg.PortalToken) == "" {
		return Config{}, fmt.Errorf("%s is required when %s=%s", envPortalToken, envPortalEnabled, booleanTrue)
	}
	return cfg, nil
}

// curationEnabled rejects unrecognised values; comparing only against "off"
// meant any typo silently turned curation back on.
func curationEnabled(mode string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case updateModeAlways:
		return true, nil
	case updateModeOff:
		return false, nil
	default:
		return false, fmt.Errorf("%s must be %q or %q, got %q", envUpdateMode, updateModeAlways, updateModeOff, mode)
	}
}

// OwnerConfigured reports whether owner-gated memory is usable.
func (c Config) OwnerConfigured() bool { return c.OwnerSlackID != "" }

func valueOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
