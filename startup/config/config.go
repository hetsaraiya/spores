// Package config is the one place env configuration is read; everything else
// takes values from a Config, so the full surface of tunables is visible here.
package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	// OpenAI-compatible chat API used by the router brain.
	OpenAIAPIKey  string
	OpenAIBaseURL string
	RouterModel   string // model for the router loop; ROUTER_MODEL then OPENAI_MODEL, default gpt-4o

	// Memory maintenance agent.
	MemoryDir        string // where long-term memory markdown lives
	MemorySmallModel string // model used while memory is still empty; default gpt-5.4-mini

	// Coding agent (Codex in an E2B sandbox).
	CodexModel    string // CODEX_MODEL then OPENAI_MODEL
	CodexAuthJSON string // resolved auth.json contents (env or file)
	E2BAPIKey     string
	E2BTemplateID string // empty means the sandbox package default

	// Integrations.
	GitHubToken   string // GITHUB_TOKEN then GH_TOKEN
	SlackBotToken string
	SlackAppToken string

	// LangSmith tracing. APIKey is empty when tracing is off (no key, or
	// LANGSMITH_TRACING/LANGCHAIN_TRACING_V2 explicitly "false").
	LangSmithAPIKey  string
	LangSmithProject string

	// One-shot / debug modes.
	AgentPrompt string // run this prompt once via CLI and exit
}

// Load reads .env (if present) and the environment into a Config.
// It only errors when a configured file (e.g. CODEX_AUTH_FILE) is unreadable.
func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		OpenAIAPIKey:     env("OPENAI_API_KEY"),
		OpenAIBaseURL:    env("OPENAI_BASE_URL"),
		RouterModel:      env("ROUTER_MODEL", "OPENAI_MODEL"),
		MemoryDir:        env("MEMORY_DIR"),
		MemorySmallModel: env("MEMORY_SMALL_MODEL"),
		CodexModel:       env("CODEX_MODEL", "OPENAI_MODEL"),
		E2BAPIKey:        env("E2B_API_KEY"),
		E2BTemplateID:    env("E2B_TEMPLATE_ID", "E2B_TEMPLATE"),
		GitHubToken:      env("GITHUB_TOKEN", "GH_TOKEN"),
		SlackBotToken:    env("SLACK_BOT_TOKEN"),
		SlackAppToken:    env("SLACK_APP_TOKEN"),
		LangSmithAPIKey:  env("LANGSMITH_API_KEY", "LANGCHAIN_API_KEY"),
		LangSmithProject: env("LANGSMITH_PROJECT", "LANGCHAIN_PROJECT"),
		AgentPrompt:      env("AGENT_PROMPT"),
	}
	if cfg.RouterModel == "" {
		cfg.RouterModel = "gpt-4o"
	}
	if cfg.MemorySmallModel == "" {
		cfg.MemorySmallModel = "gpt-5.4-mini"
	}
	if cfg.MemoryDir == "" {
		cfg.MemoryDir = "memory"
	}
	// LANGSMITH_TRACING=true is the normal "on" value; only an explicit
	// "false" disables tracing when a key is present.
	if strings.EqualFold(env("LANGSMITH_TRACING", "LANGCHAIN_TRACING_V2"), "false") {
		cfg.LangSmithAPIKey = ""
	}
	if cfg.LangSmithProject == "" {
		cfg.LangSmithProject = "spore"
	}

	auth, err := codexAuthJSON()
	if err != nil {
		return nil, err
	}
	cfg.CodexAuthJSON = auth
	return cfg, nil
}

// env returns the first non-empty value among the given variables, trimmed.
func env(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// codexAuthJSON resolves the Codex auth.json contents: the CODEX_AUTH_JSON
// env var wins, then CODEX_AUTH_FILE, then ./auth-codex.json, then
// ~/.codex/auth.json. Missing files are skipped; none found returns "".
func codexAuthJSON() (string, error) {
	if auth := env("CODEX_AUTH_JSON"); auth != "" {
		return auth, nil
	}
	paths := []string{}
	if path := env("CODEX_AUTH_FILE"); path != "" {
		paths = append(paths, path)
	}
	paths = append(paths, "auth-codex.json")
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".codex", "auth.json"))
	}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return "", nil
}