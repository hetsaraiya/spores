package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Minimal OpenAI Chat Completions client with tool calling. Uses net/http so we
// add no dependency and honor OPENAI_BASE_URL.

type oaClient struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
}

func newOAClient(apiKey, baseURL, model string) *oaClient {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if model == "" {
		model = "gpt-4o"
	}
	return &oaClient{
		apiKey:  apiKey,
		baseURL: baseURL,
		model:   model,
		http:    &http.Client{Timeout: 2 * time.Minute},
	}
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    string       `json:"content,omitempty"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function oaFunc `json:"function"`
}

type oaFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaTool struct {
	Type     string     `json:"type"`
	Function oaToolSpec `json:"function"`
}

type oaToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type oaRequest struct {
	Model      string      `json:"model"`
	Messages   []oaMessage `json:"messages"`
	Tools      []oaTool    `json:"tools,omitempty"`
	ToolChoice string      `json:"tool_choice,omitempty"`
}

type oaResponse struct {
	Choices []struct {
		Message oaMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *oaClient) complete(ctx context.Context, messages []oaMessage, tools []oaTool) (oaMessage, error) {
	return c.completeWithModel(ctx, c.model, messages, tools)
}

// completeWithModel is complete with a per-call model override, used by the
// memory updater to pick a small vs good model.
func (c *oaClient) completeWithModel(ctx context.Context, model string, messages []oaMessage, tools []oaTool) (oaMessage, error) {
	if model == "" {
		model = c.model
	}
	toolChoice := ""
	if len(tools) > 0 {
		toolChoice = "auto"
	}
	body, err := json.Marshal(oaRequest{
		Model:      model,
		Messages:   messages,
		Tools:      tools,
		ToolChoice: toolChoice,
	})
	if err != nil {
		return oaMessage{}, err
	}
	raw, err := c.post(ctx, body)
	if err != nil {
		return oaMessage{}, err
	}
	var parsed oaResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return oaMessage{}, fmt.Errorf("decode openai response: %w", err)
	}
	if parsed.Error != nil {
		return oaMessage{}, fmt.Errorf("openai error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return oaMessage{}, fmt.Errorf("openai returned no choices")
	}
	return parsed.Choices[0].Message, nil
}

// post sends the request, retrying transient failures (network errors, 429,
// and 5xx) with backoff. Returns the response body on a 200.
func (c *oaClient) post(ctx context.Context, body []byte) ([]byte, error) {
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
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)

		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, err
			}
			lastErr = err
			continue
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return raw, nil
		}
		lastErr = fmt.Errorf("openai %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return nil, lastErr
		}
	}
	return nil, lastErr
}
