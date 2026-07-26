// Package config loads runtime configuration from the environment.
package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	OpenAIAPIKey  string
	OpenAIBaseURL string
	Model         string
	GitHubToken   string
	SlackBotToken string
	SlackAppToken string

	// Coding-agent configuration is validated only when delegation is used.
	E2BAPIKey     string
	E2BTemplateID string
	CodexModel    string
	CodexAuthJSON string

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
		OpenAIAPIKey:  os.Getenv("OPENAI_API_KEY"),
		OpenAIBaseURL: valueOr("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		Model:         valueOr("MODEL", "gpt-5.5"),
		GitHubToken:   os.Getenv("GITHUB_TOKEN"),
		SlackBotToken: os.Getenv("SLACK_BOT_TOKEN"),
		SlackAppToken: os.Getenv("SLACK_APP_TOKEN"),
		E2BAPIKey:     os.Getenv("E2B_API_KEY"),
		E2BTemplateID: os.Getenv("E2B_TEMPLATE_ID"),
		CodexModel:    os.Getenv("CODEX_MODEL"),
		CodexAuthJSON: os.Getenv("CODEX_AUTH_JSON"),

		MemoryDir:     valueOr("MEMORY_DIR", "./memory"),
		OwnerSlackID:  os.Getenv("OWNER_SLACK_USER_ID"),
		CurateEnabled: valueOr("MEMORY_UPDATE_MODE", "always") != "off",

		PortalEnabled: os.Getenv("PORTAL_ENABLED") == "true",
		PortalAddr:    valueOr("PORTAL_ADDR", ":8080"),
		PortalToken:   os.Getenv("PORTAL_TOKEN"),
	}
	if cfg.OpenAIAPIKey == "" {
		return Config{}, fmt.Errorf("OPENAI_API_KEY is required")
	}
	// Fail closed: the portal edits what the agent believes.
	if cfg.PortalEnabled && cfg.PortalToken == "" {
		return Config{}, fmt.Errorf("PORTAL_TOKEN is required when PORTAL_ENABLED=true")
	}
	return cfg, nil
}

func valueOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
