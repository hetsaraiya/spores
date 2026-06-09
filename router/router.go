package router

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"spore/agent"
	"spore/githubclient"
)

// maxTurns caps the router's tool-calling loop so a confused model can't spin
// forever (and burn tokens) before answering.
const maxTurns = 12

// Conversation memory bounds: how many past messages are replayed per
// conversation and how large any single remembered message may be.
const (
	maxMemoryMessages = 20
	maxMemoryChars    = 4000
)

type Router struct {
	github *githubclient.Client
	agent  *agent.Agent
	oa     *oaClient

	mu     sync.Mutex
	memory map[string][]oaMessage // past user/assistant turns per conversation
}

func New(gh *githubclient.Client, a *agent.Agent, openAIKey, baseURL, model string) *Router {
	return &Router{
		github: gh,
		agent:  a,
		oa:     newOAClient(openAIKey, baseURL, model),
		memory: make(map[string][]oaMessage),
	}
}

const systemPrompt = `You are a routing agent for a GitHub workflow bot, talking to a user over Slack.

You have two kinds of tools:
1. github_* tools: read-only access to GitHub (files, repos, issues, PRs, search) using the user's token. Use these to answer questions and look things up yourself.
2. delegate_to_coder: hands the task to a full coding agent that edits code in a sandbox and opens a pull request.

Decide based on the request:
- If the user is asking a question, wants information, an explanation, a lookup, a summary, or a small detail you can resolve by reading the repo with github_* tools, do it yourself and answer directly. Keep answers concise and Slack-friendly.
- Only call delegate_to_coder when the user actually needs code written, edited, fixed, or a pull request opened. Before delegating, gather enough context (the right repo, relevant files) so the task description you pass is clear and self-contained.

Never invent file contents or repo facts; use the tools. If a repo is ambiguous, ask the user or use github_list_repos / github_search_repos to find it.`

// Run processes one user message and returns the final text to show in Slack.
// conversationID groups messages into a conversation (e.g. the Slack channel)
// whose recent history is replayed so follow-up messages keep their context.
// Progress is emitted via agent's status mechanism (logged, not posted).
func (r *Router) Run(ctx context.Context, conversationID, message string) (string, error) {
	messages := []oaMessage{{Role: "system", Content: systemPrompt}}
	messages = append(messages, r.recall(conversationID)...)
	messages = append(messages, oaMessage{Role: "user", Content: message})
	tools := r.tools()

	for turn := 0; turn < maxTurns; turn++ {
		agent.Emit(ctx, fmt.Sprintf("🧭 Router thinking (turn %d/%d)...", turn+1, maxTurns))
		reply, err := r.oa.complete(ctx, messages, tools)
		if err != nil {
			return "", err
		}
		messages = append(messages, reply)

		if len(reply.ToolCalls) == 0 {
			if reply.Content == "" {
				return "", fmt.Errorf("router returned an empty response")
			}
			r.remember(conversationID, message, reply.Content)
			return reply.Content, nil
		}

		for _, call := range reply.ToolCalls {
			agent.Emit(ctx, "🔧 "+call.Function.Name)
			result, delegated, err := r.dispatch(ctx, call.Function.Name, call.Function.Arguments, routerContext(messages))
			if err != nil {
				return "", err
			}
			if delegated {
				r.remember(conversationID, message, result)
				return result, nil
			}
			messages = append(messages, oaMessage{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    result,
			})
		}
	}
	return "", fmt.Errorf("router exceeded %d turns without finishing", maxTurns)
}

// recall returns a copy of the remembered turns for a conversation.
func (r *Router) recall(conversationID string) []oaMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	history := r.memory[conversationID]
	out := make([]oaMessage, len(history))
	copy(out, history)
	return out
}

// remember stores a completed user/assistant exchange. Only plain text turns
// are kept (no tool calls or tool results), so replayed history is compact
// and always a valid message sequence.
func (r *Router) remember(conversationID, userMsg, assistantMsg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.memory == nil {
		r.memory = make(map[string][]oaMessage)
	}
	history := append(r.memory[conversationID],
		oaMessage{Role: "user", Content: clipMemory(userMsg)},
		oaMessage{Role: "assistant", Content: clipMemory(assistantMsg)},
	)
	if len(history) > maxMemoryMessages {
		history = history[len(history)-maxMemoryMessages:]
	}
	r.memory[conversationID] = history
}

func clipMemory(s string) string {
	if len(s) > maxMemoryChars {
		return s[:maxMemoryChars] + "... [truncated]"
	}
	return s
}

// delegate runs the full coding pipeline and returns its result message.
func (r *Router) delegate(ctx context.Context, task, contextSummary string) string {
	agent.Emit(ctx, "🤖 Delegating to coding agent...")
	if strings.TrimSpace(contextSummary) != "" {
		task = strings.TrimSpace(task) + "\n\nAdditional context gathered by the router before delegation:\n" + contextSummary
	}
	result, err := r.agent.Run(ctx, task)
	if err != nil {
		return "❌ " + err.Error()
	}
	return result
}

func routerContext(messages []oaMessage) string {
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
