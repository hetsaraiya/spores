package router

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"spore/agent"
	"spore/config"
	"spore/githubclient"
	"spore/langsmith"
	"spore/memorystore"
)

// maxTurns caps the router's tool-calling loop so a confused model can't spin
// forever (and burn tokens) before answering.
const maxTurns = 12

// Conversation memory bounds: how many past turns are replayed per
// conversation and how large any single turn may be.
const (
	maxMemoryMessages = 20
	maxMemoryChars    = 4000
)

// Turn is one prior message in a conversation, as supplied by a HistoryFunc
// (e.g. fetched live from Slack). Speaker is the human sender's display name;
// for the bot's own past messages IsBot is true and Speaker is ignored.
type Turn struct {
	Speaker string
	IsBot   bool
	Text    string
}

// HistoryFunc returns the prior turns of a conversation, oldest first, and
// excludes the current message (identified by currentText). Returning nil is
// fine — it just means the conversation starts fresh. The provider is the
// source of truth for history, so nothing is kept in this process; a redeploy
// loses no context.
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
	smallModel := cfg.MemorySmallModel
	if smallModel == "" {
		smallModel = defaultSmallModel
	}
	tracer := langsmith.New(cfg.LangSmithAPIKey, cfg.LangSmithProject)
	return &Router{
		github:     gh,
		agent:      a,
		llm:        newLLMClient(cfg.OpenAIAPIKey, cfg.OpenAIBaseURL, cfg.RouterModel, tracer),
		store:      store,
		smallModel: smallModel,
		tracer:     tracer,
	}
}

// SetHistory wires in a provider for prior conversation turns (e.g. fetched
// live from Slack). Without it, Run starts each message with no history.
func (r *Router) SetHistory(fn HistoryFunc) { r.history = fn }

// Shutdown flushes any buffered LangSmith traces. Call before a short-lived
// process exits so the last spans are not lost.
func (r *Router) Shutdown(ctx context.Context) { r.tracer.Shutdown(ctx) }

// Run processes one user message and returns the final text to show in Slack.
// conversationID groups messages into a conversation (e.g. the Slack channel);
// prior turns are pulled from the history provider. speaker is the
// display name of whoever sent this message. Progress is emitted via agent's
// status mechanism (logged, not posted).
func (r *Router) Run(ctx context.Context, conversationID, speaker, message string) (response string, rerr error) {
	ctx, run := r.tracer.Start(ctx, "router turn", "chain", map[string]any{
		"speaker": speaker, "message": message,
	})
	defer func() { run.End(map[string]any{"response": response}, rerr) }()

	system := systemPrompt
	if r.store != nil {
		if block := r.store.PromptBlock(); block != "" {
			system += "\n\n# Long-term memory\nWhat you know about the user's company, product, stack, and skills from past sessions. Use it to resolve ambiguity and match their preferences.\n\n" + block
		}
	}
	messages := []chatMessage{{Role: "system", Content: system}}
	if r.history != nil {
		messages = append(messages, historyMessages(r.history(ctx, conversationID, message))...)
	}
	messages = append(messages, chatMessage{Role: "user", Content: speakerLabel(speaker, message)})
	tools := r.tools()

	for turn := 0; turn < maxTurns; turn++ {
		agent.Emit(ctx, fmt.Sprintf("🧭 Router thinking (turn %d/%d)...", turn+1, maxTurns))
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
			agent.Emit(ctx, "🔧 "+call.Function.Name)
			result, delegated, err := r.dispatch(ctx, call.Function.Name, call.Function.Arguments, routerContext(messages))
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

// historyMessages converts prior turns into replayable chat messages. Each
// human turn is labeled with the speaker's name so the model can tell
// participants apart in a multi-person channel without being told who is who;
// the bot's own turns become plain assistant messages. Only the most recent
// maxMemoryMessages turns are kept, each clipped to maxMemoryChars.
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

// speakerLabel prefixes a message with the sender's display name ("Het: ...")
// so multi-person context is legible to the model. An unknown speaker yields
// the bare text.
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

// delegate runs the full coding pipeline, then hands the raw outcome to the
// communication agent so the user gets a natural, teammate-style reply rather
// than a templated dump. The reporter runs on both success and failure.
func (r *Router) delegate(ctx context.Context, task, contextSummary string) string {
	agent.Emit(ctx, "🤖 Delegating to coding agent...")
	fullTask := strings.TrimSpace(task)
	if strings.TrimSpace(contextSummary) != "" {
		fullTask += "\n\nAdditional context gathered by the router before delegation:\n" + contextSummary
	}
	if r.store != nil {
		if block := r.store.PromptBlock(); block != "" {
			fullTask += "\n\nLong-term memory about the user's company, product, stack, and preferences:\n" + block
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
