package main

import (
	"context"
	"log"
	"time"

	"spore/router"
	"spore/slackhandler"
	"spore/startup"
)

func main() {
	a, err := startup.Boot()
	if err != nil {
		log.Fatalf("startup: %v", err)
	}
	if a.Config.AgentPrompt != "" {
		runOnce(a.Router, a.Config.AgentPrompt)
		return
	}
	h := slackhandler.New(a.Config.SlackBotToken, a.Config.SlackAppToken, a.Router)
	a.Router.SetHistory(h.History) // conversation history comes live from Slack, not local state
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