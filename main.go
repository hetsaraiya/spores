package main

import (
	"context"
	"log"
	"time"

	"spore/config"
	"spore/githubclient"
	"spore/memorystore"
	"spore/router"
	"spore/slackhandler"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	gh := githubclient.New(cfg.GitHubToken)
	store, err := memorystore.New(cfg.MemoryDir)
	if err != nil {
		log.Fatalf("failed to init memory store: %v", err)
	}
	rt := router.New(gh, store, cfg)
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
	result, err := rt.Run(ctx, "cli", "You", prompt)
	if err != nil {
		log.Fatal(err)
	}
	log.Print(result)
	rt.Wait()                         // let the background memory update finish before exiting
	rt.Shutdown(context.Background()) // flush LangSmith traces before the process exits
}
