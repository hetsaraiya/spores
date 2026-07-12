package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/hetsaraiya/spores/internal/agent"
	"github.com/hetsaraiya/spores/internal/coder"
	"github.com/hetsaraiya/spores/internal/config"
	"github.com/hetsaraiya/spores/internal/github"
	"github.com/hetsaraiya/spores/internal/slackhandler"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	service := agent.New(
		openai.NewClient(option.WithAPIKey(cfg.OpenAIAPIKey), option.WithBaseURL(cfg.OpenAIBaseURL)),
		github.New(cfg.GitHubToken),
		coder.New(coder.Config{E2BAPIKey: cfg.E2BAPIKey, E2BTemplateID: cfg.E2BTemplateID, CodexModel: cfg.CodexModel, CodexAuthJSON: cfg.CodexAuthJSON, OpenAIAPIKey: cfg.OpenAIAPIKey, GitHubToken: cfg.GitHubToken}, os.Stdout),
		cfg.Model,
	)
	if prompt := os.Getenv("PROMPT"); prompt != "" {
		runCLI(service, prompt)
		return
	}
	handler, err := slackhandler.New(cfg.SlackBotToken, cfg.SlackAppToken, service)
	if err != nil {
		log.Fatal(err)
	}
	if err := handler.Run(); err != nil {
		log.Fatal(err)
	}
}

func runCLI(service *agent.Agent, prompt string) {
	result, err := service.Run(context.Background(), agent.Request{Message: prompt})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}
