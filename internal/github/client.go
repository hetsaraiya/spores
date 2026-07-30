// Package github provides read-only GitHub operations for the agent.
package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	apiBase      = "https://api.github.com"
	apiVersion   = "2022-11-28"
	acceptHeader = "application/vnd.github+json"

	// http.DefaultClient has no timeout, so a stalled connection would hang the
	// whole agent turn.
	requestTimeout   = 20 * time.Second
	maxResponseBytes = 4 << 20
	maxToolOutput    = 8000

	truncationNote = "\n... [truncated]"
)

// First-page sizes; callers label a full page so the model is never told a
// truncated list is complete.
const (
	listPageSize   = 50
	searchPageSize = 30
	maxPageSize    = 100
)

type Client struct {
	http  *http.Client
	token string
}

func New(token string) *Client {
	return &Client{http: &http.Client{Timeout: requestTimeout}, token: token}
}

func (c *Client) get(ctx context.Context, endpoint string, target any) error {
	body, err := c.fetch(ctx, apiBase+endpoint)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

// fetch performs one authenticated GET, bounding the response body.
func (c *Client) fetch(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", acceptHeader)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("GitHub API %s: %s", res.Status, clip(string(body)))
	}
	return body, nil
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
	if len(value) > maxToolOutput {
		return value[:maxToolOutput] + truncationNote
	}
	return value
}

// notePartialPage marks a listing that filled its page, so the model does not
// report a first page as the complete set.
func notePartialPage(out *strings.Builder, returned, pageSize int) {
	if returned >= pageSize {
		fmt.Fprintf(out, "... [only the first %d shown; more exist]\n", pageSize)
	}
}

func decodeContent(value string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(value, "\n", ""))
	if err != nil {
		return "", fmt.Errorf("decode GitHub file content: %w", err)
	}
	return string(decoded), nil
}
