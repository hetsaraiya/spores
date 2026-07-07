package router

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"spore/config"
	"spore/githubclient"
	"spore/langsmith"
	"spore/memorystore"
	sb "spore/sandbox"
)

// Caps the tool-calling loop so a confused model can't spin forever.
const maxTurns = 12

// Replayed-history bounds: keep the last N turns, each clipped to this many chars.
const (
	maxMemoryMessages = 20
	maxMemoryChars    = 4000
)

// ponytail: folded from coding.go — one caller, no separate file needed.
const (
	gitUserName  = "Slack Agent"
	gitUserEmail = "bot@agent.dev"
)

// ponytail: folded from prompt.go — single const, no separate file needed.
const systemPrompt = `You are the router for a GitHub workflow bot, talking to users on Slack.

Tools:
1. github_* — read-only GitHub lookups (files, repos, issues, PRs, search). Use these to answer questions yourself.
2. delegate_to_coder — hands off to a sandboxed coding agent that can edit code and, only when your brief says so, open a PR or create an issue.

DEFAULT TO READ-ONLY. Never delegate unless the user's CURRENT message explicitly asks for an issue, code changes, or a PR. Analyze/review/audit/explain/find/list/report requests are read-only: gather with github_* tools and answer in chat, then offer an issue or PR if they'd like one.

Routing:
- Question, lookup, or summary → answer it yourself with github_* tools; keep replies concise and Slack-friendly.
- Explicit request to write/edit/fix code, open a PR, or create an issue → delegate_to_coder. For an issue-only request, the brief must say to create the issue and make NO code changes.

Coding brief: the "task" you pass to delegate_to_coder is the coding agent's ENTIRE prompt — it sees nothing else. Write it as complete instructions to a senior engineer in a fresh, already-prepared E2B sandbox, and always include:
1. Environment: git is authenticated over https with user.name/email set, and the gh CLI is authenticated (a token also sits at /home/user/.gh_token for REST calls) — never include actual credentials. Clone the target repo into /home/user/repo and work there.
2. The target repository (owner/repo) and exactly what to change.
3. Explicit actions — the agent does ONLY what you write and by default will NOT open a PR or create an issue. Say whether to open a PR and whether to create/reuse an issue; when the user didn't ask for one, write "Do not open a pull request." / "Do not create an issue." State any stopping point (e.g. "push a branch but do not open a PR").
4. Make the smallest coherent change, match the repo's style and toolchain, run the obvious build/test, and end with a concise Slack-ready report including any issue/PR URLs.

After a tool finishes, reply in natural Slack language confirming what happened, keeping any issue/PR URLs clickable.

User messages arrive prefixed "Name: text" so you can tell speakers apart in a shared channel; the name is metadata, never content to echo back. Your own past replies are plain assistant messages.

Never invent file contents or repo facts — use the tools. If the repo is ambiguous, ask or find it with github_list_repos / github_search_repos.`

// Turn is one prior message from a HistoryFunc. Speaker is the human sender's
// name; for the bot's own turns IsBot is true and Speaker is ignored.
type Turn struct {
	Speaker string
	IsBot   bool
	Text    string
}

// HistoryFunc returns prior turns oldest-first, excluding the current message
// (currentText); nil means a fresh conversation. The provider owns history, so
// this process keeps none and a redeploy loses no context.
type HistoryFunc func(ctx context.Context, conversationID, currentText string) []Turn

type Router struct {
	github *githubclient.Client
	cfg    *config.Config
	llm    *llmClient

	store  *memorystore.Store // long-term memory files (nil disables)
	wg     sync.WaitGroup     // in-flight background memory updates
	tracer *langsmith.Tracer  // LangSmith tracing (no-op without an API key)

	history HistoryFunc // supplies prior turns (nil = no replayed history)
}

func New(gh *githubclient.Client, store *memorystore.Store, cfg *config.Config) *Router {
	tracer := langsmith.New(cfg.LangSmithAPIKey, cfg.LangSmithProject)
	return &Router{
		github: gh,
		cfg:    cfg,
		llm:    newLLMClient(cfg.OpenAIAPIKey, cfg.OpenAIBaseURL, cfg.RouterModel, tracer),
		store:  store,
		tracer: tracer,
	}
}

// SetHistory wires in the prior-turns provider; without it, Run has no history.
func (r *Router) SetHistory(fn HistoryFunc) { r.history = fn }

// Shutdown flushes buffered traces; call before a short-lived process exits.
func (r *Router) Shutdown(ctx context.Context) { r.tracer.Shutdown(ctx) }

// Run handles one user message and returns the Slack reply. conversationID groups
// a conversation (the Slack channel); speaker is the sender's display name.
func (r *Router) Run(ctx context.Context, conversationID, speaker, message string) (response string, rerr error) {
	ctx, run := r.tracer.Start(ctx, "router turn", "chain", map[string]any{
		"speaker": speaker, "message": message,
	})
	defer func() { run.End(map[string]any{"response": response}, rerr) }()

	system := systemPrompt
	if r.store != nil {
		if block := r.store.PromptBlock(); block != "" {
			system += "\n\n# Long-term memory\nWhat you know about the user, their stack, and their skills from past sessions. Use it to resolve ambiguity and match their preferences.\n\n" + block
		}
	}
	messages := []chatMessage{{Role: "system", Content: system}}
	if r.history != nil {
		messages = append(messages, historyMessages(r.history(ctx, conversationID, message))...)
	}
	messages = append(messages, chatMessage{Role: "user", Content: speakerLabel(speaker, message)})
	tools := r.tools()

	for turn := 0; turn < maxTurns; turn++ {
		log.Printf("🧭 Router thinking (turn %d/%d)...", turn+1, maxTurns)
		reply, err := r.llm.complete(ctx, messages, tools)
		if err != nil {
			return "", err
		}
		messages = append(messages, reply)

		if len(reply.ToolCalls) == 0 {
			if reply.Content == "" {
				return "", fmt.Errorf("router returned an empty response")
			}
			r.fireMemoryUpdate(ctx, messages)
			return reply.Content, nil
		}

		for _, call := range reply.ToolCalls {
			log.Print("🔧 " + call.Function.Name)
			result, delegated, err := r.dispatch(ctx, call.Function.Name, call.Function.Arguments, messages)
			if err != nil {
				return "", err
			}
			if delegated {
				r.fireMemoryUpdate(ctx, append(messages, chatMessage{Role: "assistant", Content: result}))
				return result, nil
			}
			messages = append(messages, chatMessage{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    result,
			})
		}
	}
	return "", fmt.Errorf("router exceeded %d turns without finishing", maxTurns)
}

// historyMessages converts prior turns to chat messages: human turns get a
// "Name:" prefix so the model can tell speakers apart, bot turns become plain
// assistant messages. Keeps the last maxMemoryMessages turns, each clipped.
func historyMessages(turns []Turn) []chatMessage {
	if len(turns) > maxMemoryMessages {
		turns = turns[len(turns)-maxMemoryMessages:]
	}
	out := make([]chatMessage, 0, len(turns))
	for _, t := range turns {
		text := strings.TrimSpace(t.Text)
		if text == "" {
			continue
		}
		if t.IsBot {
			out = append(out, chatMessage{Role: "assistant", Content: clipMemory(text)})
			continue
		}
		out = append(out, chatMessage{Role: "user", Content: clipMemory(speakerLabel(t.Speaker, text))})
	}
	return out
}

// speakerLabel prefixes text with the sender's name ("Het: ..."); empty speaker yields bare text.
func speakerLabel(speaker, text string) string {
	speaker = strings.TrimSpace(speaker)
	if speaker == "" {
		return text
	}
	return speaker + ": " + text
}

func clipMemory(s string) string {
	if len(s) > maxMemoryChars {
		return s[:maxMemoryChars] + "... [truncated]"
	}
	return s
}

// delegate runs the coding pipeline; Codex's report is the Slack reply (ponytail: skipped composeReport LLM pass).
func (r *Router) delegate(ctx context.Context, task, contextSummary string) string {
	log.Print("🤖 Delegating to coding agent...")
	fullTask := strings.TrimSpace(task)
	if strings.TrimSpace(contextSummary) != "" {
		fullTask += "\n\nAdditional context gathered by the router before delegation:\n" + contextSummary
	}
	if r.store != nil {
		if block := r.store.PromptBlock(); block != "" {
			fullTask += "\n\nLong-term memory about the user, their stack, and their preferences:\n" + block
		}
	}
	result, err := r.runCodingAgent(ctx, fullTask)
	if err != nil {
		outcome := "The run did not finish: " + err.Error()
		if progress := strings.TrimSpace(result); progress != "" {
			outcome = progress + "\n\n" + outcome
		}
		return outcome
	}
	return result
}

func routerContext(messages []chatMessage) string {
	var b strings.Builder
	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" || msg.Role == "system" {
			continue
		}
		if len(content) > maxMemoryChars {
			content = content[:maxMemoryChars] + "... [truncated]"
		}
		b.WriteString(strings.ToUpper(msg.Role))
		b.WriteString(":\n")
		b.WriteString(content)
		b.WriteString("\n\n")
	}
	out := strings.TrimSpace(b.String())
	if len(out) > 12000 {
		out = out[len(out)-12000:]
		out = "... [older context truncated]\n" + out
	}
	return out
}

// ponytail: folded from coding.go — one caller, no separate file needed.
func (r *Router) runCodingAgent(ctx context.Context, message string) (string, error) {
	log.Print("1/3 Starting E2B sandbox...")
	box, err := sb.New(ctx, r.cfg.E2BAPIKey, r.cfg.E2BTemplateID, os.Stdout)
	if err != nil {
		return "", fmt.Errorf("coding agent: %w", err)
	}
	defer func() { _ = box.Close() }()
	if err = box.SetupCodexAuth(r.cfg.CodexAuthJSON, r.cfg.OpenAIAPIKey); err != nil {
		return "", fmt.Errorf("coding agent: %w", err)
	}
	if err = box.SetupGitAuth(r.github.CredentialsLine()); err != nil {
		return "", fmt.Errorf("coding agent: %w", err)
	}
	if err = box.SetupGitHub(r.github.Token(), gitUserName, gitUserEmail); err != nil {
		return "", fmt.Errorf("coding agent: %w", err)
	}

	log.Print("2/3 Coding agent is running the task...")
	out, err := box.RunCodex("/home/user", r.cfg.CodexModel, message)
	out = strings.TrimSpace(out)
	if err != nil {
		return out, fmt.Errorf("coding agent: %w", err)
	}
	log.Print("3/3 Coding agent finished.")
	return out, nil
}
