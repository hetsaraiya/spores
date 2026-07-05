package main

import (
	"context"
	"log"
	"os"
	"time"

	"spore/agent"
	"spore/config"
	"spore/githubclient"
	"spore/memorystore"
	"spore/router"
	sb "spore/sandbox"
	"spore/slackhandler"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	log.Print("spore build: e2b-codex-direct")
	if cfg.SandboxProbe {
		runSandboxProbe(cfg)
		return
	}

	gh := githubclient.New(cfg.GitHubToken)
	a := agent.New(gh, cfg)
	store, err := memorystore.New(cfg.MemoryDir)
	if err != nil {
		log.Fatalf("failed to init memory store: %v", err)
	}
	rt := router.New(gh, a, store, cfg)
	if cfg.AgentPrompt != "" {
		runOnce(rt, cfg.AgentPrompt)
		return
	}
	h := slackhandler.New(cfg.SlackBotToken, cfg.SlackAppToken, rt)
	rt.SetHistory(h.History) // conversation history comes live from Slack, not local state
	log.Println("Agent online")
	h.Run()
}

func runOnce(rt *router.Router, prompt string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	ctx = agent.WithStatus(ctx, func(msg string) { log.Print(msg) })
	result, err := rt.Run(ctx, "cli", "You", prompt)
	if err != nil {
		log.Fatal(err)
	}
	log.Print(result)
	rt.Wait()                         // let the background memory update finish before exiting
	rt.Shutdown(context.Background()) // flush LangSmith traces before the process exits
}

func runSandboxProbe(cfg *config.Config) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	log.Print("sandbox probe: creating sandbox and testing stdout/stderr streaming")
	box, err := sb.New(ctx, cfg.E2BAPIKey, cfg.E2BTemplateID, os.Stdout)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = box.Close() }()

	if err := box.ProbeIO(); err != nil {
		log.Fatal(err)
	}

	out, stderr, err := box.RunCommand("for i in 1 2 3 4 5; do printf '[probe stdout] tick %s\\n' \"$i\"; printf '[probe stderr] tick %s\\n' \"$i\" >&2; sleep 1; done")
	if err != nil {
		log.Fatalf("%v\n%s%s", err, out, stderr)
	}
	log.Print("sandbox probe: complete")
}
