// Package clients contains small reusable API clients.
package clients

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

type HTTPClient struct {
	client *http.Client
	token  string
}

func NewHTTP(token string) *HTTPClient {
	return &HTTPClient{client: http.DefaultClient, token: token}
}

func (c *HTTPClient) Get(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	res, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("GitHub API %s: %s", res.Status, string(body))
	}
	return body, nil
}
