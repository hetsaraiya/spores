// Package github provides read-only GitHub operations for the agent.
package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/hetsaraiya/spores/internal/clients"
)

const (
	apiBase = "https://api.github.com"
	maxBody = 8000
)

type Client struct{ http *clients.HTTPClient }

func New(token string) *Client { return &Client{http: clients.NewHTTP(token)} }

func (c *Client) get(ctx context.Context, endpoint string, target any) error {
	body, err := c.http.Get(ctx, apiBase+endpoint)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

func splitRepo(full string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(full), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("repo must use owner/repo format")
	}
	return parts[0], parts[1], nil
}

func repoPath(full string) (string, error) {
	owner, repo, err := splitRepo(full)
	if err != nil {
		return "", err
	}
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo), nil
}

func clip(value string) string {
	if len(value) > maxBody {
		return value[:maxBody] + "\n... [truncated]"
	}
	return value
}

func decodeContent(value string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(value, "\n", ""))
	if err != nil {
		return "", fmt.Errorf("decode GitHub file content: %w", err)
	}
	return string(decoded), nil
}
