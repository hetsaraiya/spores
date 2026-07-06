package router

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"spore/agent"
	"spore/config"
	"spore/githubclient"
	"spore/langsmith"
	"spore/memorystore"
)

// Caps the tool-calling loop so a confused model can't spin forever.
const maxTurns = 12

// Replayed-history bounds: keep the last N turns, each clipped to this many chars.
const (
	maxMemoryMessages = 20
	maxMemoryChars    = 4000
)

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
	agent  *agent.Agent
	llm    *llmClient

	store      *memorystore.Store // long-term memory files (nil disables)
	smallModel string             // model for memory updates while memory is empty
	wg         sync.WaitGroup     // in-flight background memory updates
	tracer     *langsmith.Tracer  // LangSmith tracing (no-op without an API key)

	history HistoryFunc // supplies prior turns (nil = no replayed history)
}

func New(gh *githubclient.Client, a *agent.Agent, store *memorystore.Store, cfg *config.Config) *Router {
	tracer := langsmith.New(cfg.LangSmithAPIKey, cfg.LangSmithProject)
	return &Router{
		github:     gh,
		agent:      a,
		llm:        newLLMClient(cfg.OpenAIAPIKey, cfg.OpenAIBaseURL, cfg.RouterModel, tracer),
		store:      store,
		smallModel: cfg.MemorySmallModel,
		tracer:     tracer,
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

// delegate runs the coding pipeline, then routes the raw outcome (success or
// failure) through composeReport for a natural, teammate-style reply.
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
	result, err := r.agent.Run(ctx, fullTask)
	if err != nil {
		outcome := "The run did not finish: " + err.Error()
		if progress := strings.TrimSpace(result); progress != "" {
			outcome = progress + "\n\n" + outcome
		}
		return r.composeReport(ctx, task, outcome, false)
	}
	return r.composeReport(ctx, task, result, true)
}

func routerContext(messages []chatMessage) string {
	var b strings.Builder
	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" || msg.Role == "system" {
			continue
		}
		if len(content) > 4000 {
			content = content[:4000] + "... [truncated]"
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
