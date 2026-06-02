package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"spore/agent"
	"spore/githubclient"
	sb "spore/sandbox"
	"spore/slackhandler"

	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Printf("failed to load .env: %v", err)
	}
	log.Print("spore build: e2b-codex-direct")
	if os.Getenv("SANDBOX_PROBE") != "" {
		runSandboxProbe(os.Getenv("E2B_API_KEY"))
		return
	}

	codexAuth, err := codexAuthJSON()
	if err != nil {
		log.Fatalf("failed to load Codex auth: %v", err)
	}
	a := agent.New(
		githubclient.New(githubToken()),
		os.Getenv("E2B_API_KEY"),
		codexModel(),
		codexAuth,
		os.Getenv("OPENAI_API_KEY"),
	)
	if prompt := os.Getenv("AGENT_PROMPT"); prompt != "" {
		runOnce(a, prompt)
		return
	}
	h := slackhandler.New(os.Getenv("SLACK_BOT_TOKEN"), os.Getenv("SLACK_APP_TOKEN"), a)
	log.Println("Agent online")
	h.Run()
}

func githubToken() string {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token
	}
	return os.Getenv("GH_TOKEN")
}

func codexModel() string {
	if model := os.Getenv("CODEX_MODEL"); model != "" {
		return model
	}
	return os.Getenv("OPENAI_MODEL")
}

func codexAuthJSON() (string, error) {
	if auth := os.Getenv("CODEX_AUTH_JSON"); auth != "" {
		return auth, nil
	}
	paths := []string{}
	if path := os.Getenv("CODEX_AUTH_FILE"); path != "" {
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

func runOnce(a *agent.Agent, prompt string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	ctx = agent.WithStatus(ctx, func(msg string) { log.Print(msg) })
	if err := a.Run(ctx, prompt); err != nil {
		log.Fatal(err)
	}
}

func runSandboxProbe(key string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	log.Print("sandbox probe: creating sandbox and testing stdout/stderr streaming")
	box, err := sb.New(ctx, key, os.Stdout)
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
