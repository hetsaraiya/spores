// Package jina provides access to Jina AI's Reader and Search APIs.
package jina

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	defaultReaderURL = "https://r.jina.ai"
	defaultSearchURL = "https://s.jina.ai"
	maxResponseBytes = 5 << 20
)

type Client struct {
	httpClient *http.Client
	apiKey     string
	readerURL  string
	searchURL  string
}

func New(apiKey string) *Client {
	return newClient(http.DefaultClient, apiKey, defaultReaderURL, defaultSearchURL)
}

func newClient(httpClient *http.Client, apiKey, readerURL, searchURL string) *Client {
	return &Client{
		httpClient: httpClient,
		apiKey:     strings.TrimSpace(apiKey),
		readerURL:  strings.TrimRight(readerURL, "/"),
		searchURL:  strings.TrimRight(searchURL, "/"),
	}
}

func (c *Client) Read(ctx context.Context, targetURL string) (string, error) {
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return "", fmt.Errorf("url is required")
	}
	return c.request(ctx, c.readerURL, map[string]string{"url": targetURL})
}

func (c *Client) Search(ctx context.Context, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	if c.apiKey == "" {
		return "", fmt.Errorf("JINA_API_KEY is required for web search")
	}
	return c.request(ctx, c.searchURL, map[string]string{"q": query})
}

func (c *Client) request(ctx context.Context, endpoint string, payload map[string]string) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode Jina request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create Jina request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/plain")
	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("call Jina API: %w", err)
	}
	defer response.Body.Close()

	contents, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("read Jina response: %w", err)
	}
	if len(contents) > maxResponseBytes {
		return "", fmt.Errorf("jina response exceeds %d bytes", maxResponseBytes)
	}
	text := strings.TrimSpace(string(contents))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if text == "" {
			text = response.Status
		}
		return "", fmt.Errorf("jina API returned %s: %s", response.Status, text)
	}
	return text, nil
}
