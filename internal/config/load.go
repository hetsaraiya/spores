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
	}
	if cfg.OpenAIAPIKey == "" {
		return Config{}, fmt.Errorf("OPENAI_API_KEY is required")
	}
	return cfg, nil
}

func valueOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
