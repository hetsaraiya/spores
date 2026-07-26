package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hetsaraiya/spores/internal/agent"
	"github.com/hetsaraiya/spores/internal/coder"
	"github.com/hetsaraiya/spores/internal/config"
	"github.com/hetsaraiya/spores/internal/github"
	"github.com/hetsaraiya/spores/internal/memory"
	"github.com/hetsaraiya/spores/internal/portal"
	"github.com/hetsaraiya/spores/internal/slackhandler"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// drainTimeout bounds how long shutdown waits for queued memory updates.
const drainTimeout = 30 * time.Second

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	store, err := memory.New(cfg.MemoryDir)
	if err != nil {
		log.Fatal(err)
	}
	if err := store.Check(); err != nil {
		log.Fatal(err)
	}
	// Mounted-volume mistakes are silent otherwise: memory just resets each deploy.
	log.Printf("memory directory: %s (writable, history=%t, curation=%t)", store.Dir(), store.HistoryEnabled(), cfg.CurateEnabled)

	client := openai.NewClient(option.WithAPIKey(cfg.OpenAIAPIKey), option.WithBaseURL(cfg.OpenAIBaseURL))
	curator := memory.NewCurator(client, store, cfg.Model, cfg.OwnerSlackID, cfg.CurateEnabled)
	service := agent.New(
		client,
		github.New(cfg.GitHubToken),
		coder.New(coder.Config{E2BAPIKey: cfg.E2BAPIKey, E2BTemplateID: cfg.E2BTemplateID, CodexModel: cfg.CodexModel, CodexAuthJSON: cfg.CodexAuthJSON, OpenAIAPIKey: cfg.OpenAIAPIKey, GitHubToken: cfg.GitHubToken}, os.Stdout, store),
		store,
		curator,
		cfg.Model,
	)

	if prompt := os.Getenv("PROMPT"); prompt != "" {
		// Running the binary locally requires shell access to the host, so the
		// CLI speaks as the owner; otherwise it could never update USER.md.
		runCLI(service, prompt, cfg.OwnerSlackID)
		drain(curator)
		return
	}

	if cfg.PortalEnabled {
		server, err := portal.New(store, cfg.PortalToken)
		if err != nil {
			log.Fatal(err)
		}
		go server.Serve(cfg.PortalAddr)
	}

	handler, err := slackhandler.New(cfg.SlackBotToken, cfg.SlackAppToken, service)
	if err != nil {
		log.Fatal(err)
	}
	failed := make(chan error, 1)
	go func() { failed <- handler.Run() }()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-failed:
		log.Printf("slack handler stopped: %v", err)
		drain(curator)
		os.Exit(1)
	case signal := <-signals:
		log.Printf("received %s, draining memory updates", signal)
		drain(curator)
	}
}

// drain waits for queued curation so a deploy does not discard memory the agent
// has already earned.
func drain(curator *memory.Curator) {
	ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	if err := curator.Shutdown(ctx); err != nil {
		log.Print(err)
	}
}

func runCLI(service *agent.Agent, prompt, speakerID string) {
	result, err := service.Run(context.Background(), agent.Request{Message: prompt, SpeakerID: speakerID})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}
