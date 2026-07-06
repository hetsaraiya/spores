package router

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"

	"spore/langsmith"
)

// Chat goes through langchaingo; we keep our own chatMessage/toolDef types and
// translate at the boundary. LLM tracing lives at the HTTP layer — the client is
// wrapped with LangSmith's traceopenai middleware, so runs carry real messages,
// tool calls, and usage, nested under the ctx span.

type llmClient struct {
	llm     llms.Model
	model   string
	initErr error // set if the provider failed to construct; surfaced on calls
}

func newLLMClient(apiKey, baseURL, model string, tracer *langsmith.Tracer) *llmClient {
	opts := []openai.Option{
		openai.WithModel(model),
		openai.WithHTTPClient(tracer.WrapHTTPClient(nil)),
	}
	if apiKey != "" {
		opts = append(opts, openai.WithToken(apiKey))
	}
	if baseURL != "" {
		opts = append(opts, openai.WithBaseURL(strings.TrimRight(baseURL, "/")))
	}
	llm, err := openai.New(opts...)
	return &llmClient{llm: llm, model: model, initErr: err}
}

// chatMessage is the router's internal chat message. Kept independent of
// langchaingo so the rest of the package doesn't depend on its types.
type chatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function funcCall `json:"function"`
}

type funcCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type toolDef struct {
	Type     string     `json:"type"`
	Function funcSchema `json:"function"`
}

type funcSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

func (c *llmClient) complete(ctx context.Context, messages []chatMessage, tools []toolDef) (chatMessage, error) {
	return c.completeWithModel(ctx, c.model, messages, tools)
}

// completeWithModel is complete with a per-call model override (memory updater picks small vs good).
func (c *llmClient) completeWithModel(ctx context.Context, model string, messages []chatMessage, tools []toolDef) (chatMessage, error) {
	if c.initErr != nil {
		return chatMessage{}, fmt.Errorf("openai client not initialized: %w", c.initErr)
	}

	opts := []llms.CallOption{llms.WithModel(model)}
	if len(tools) > 0 {
		opts = append(opts, llms.WithTools(toLangchainTools(tools)))
	}
	resp, err := c.generate(ctx, toLangchainMessages(messages), opts)
	if err != nil {
		return chatMessage{}, err
	}
	if len(resp.Choices) == 0 {
		return chatMessage{}, fmt.Errorf("openai returned no choices")
	}
	return fromLangchainChoice(resp.Choices[0]), nil
}

// generate retries GenerateContent with backoff — the gateway at OPENAI_BASE_URL may intermittently 5xx.
func (c *llmClient) generate(ctx context.Context, messages []llms.MessageContent, opts []llms.CallOption) (*llms.ContentResponse, error) {
	const attempts = 3
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * 2 * time.Second):
			}
		}
		resp, err := c.llm.GenerateContent(ctx, messages, opts...)
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

// toLangchainMessages maps the router's messages to langchaingo message parts.
func toLangchainMessages(messages []chatMessage) []llms.MessageContent {
	out := make([]llms.MessageContent, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case "tool":
			out = append(out, llms.MessageContent{
				Role:  llms.ChatMessageTypeTool,
				Parts: []llms.ContentPart{llms.ToolCallResponse{ToolCallID: m.ToolCallID, Content: m.Content}},
			})
		case "assistant":
			var parts []llms.ContentPart
			if m.Content != "" {
				parts = append(parts, llms.TextContent{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				parts = append(parts, llms.ToolCall{
					ID:           tc.ID,
					Type:         tc.Type,
					FunctionCall: &llms.FunctionCall{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
				})
			}
			out = append(out, llms.MessageContent{Role: llms.ChatMessageTypeAI, Parts: parts})
		case "system":
			out = append(out, llms.TextParts(llms.ChatMessageTypeSystem, m.Content))
		default: // user
			out = append(out, llms.TextParts(llms.ChatMessageTypeHuman, m.Content))
		}
	}
	return out
}

func toLangchainTools(tools []toolDef) []llms.Tool {
	out := make([]llms.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, llms.Tool{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			},
		})
	}
	return out
}

func fromLangchainChoice(choice *llms.ContentChoice) chatMessage {
	msg := chatMessage{Role: "assistant", Content: choice.Content}
	for _, tc := range choice.ToolCalls {
		var fn funcCall
		if tc.FunctionCall != nil {
			fn = funcCall{Name: tc.FunctionCall.Name, Arguments: tc.FunctionCall.Arguments}
		}
		typ := tc.Type
		if typ == "" {
			typ = "function"
		}
		msg.ToolCalls = append(msg.ToolCalls, toolCall{ID: tc.ID, Type: typ, Function: fn})
	}
	return msg
}
