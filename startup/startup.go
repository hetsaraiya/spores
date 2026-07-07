// Package startup loads config, prepares on-disk layout, and builds the app graph.
package startup

import (
	"fmt"

	"spore/agent"
	"spore/githubclient"
	"spore/memorystore"
	"spore/router"
	"spore/startup/app"
	"spore/startup/config"
)

// Boot loads config, prepares on-disk layout, and builds the application graph.
func Boot() (*app.App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := memorystore.EnsureLayout(cfg.MemoryDir); err != nil {
		return nil, fmt.Errorf("memory: %w", err)
	}
	gh := githubclient.New(cfg.GitHubToken)
	a := agent.New(gh, cfg)
	store := memorystore.New(cfg.MemoryDir)
	rt := router.New(gh, a, store, cfg)
	return &app.App{Config: cfg, Router: rt}, nil
}