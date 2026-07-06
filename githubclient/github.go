package githubclient

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/go-github/v60/github"
	"golang.org/x/oauth2"
)

type Client struct {
	api   *github.Client
	token string
}

func New(token string) *Client {
	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	return &Client{
		api:   github.NewClient(oauth2.NewClient(context.Background(), src)),
		token: token,
	}
}

// Token returns the raw GitHub token. Used to authenticate the gh CLI and
// GitHub REST calls the coding agent makes from inside the sandbox.
func (c *Client) Token() string { return c.token }

// CredentialsLine returns the line for a git credential store file
// (credential.helper store) granting access to github.com.
func (c *Client) CredentialsLine() string {
	u := &url.URL{Scheme: "https", Host: "github.com"}
	u.User = url.UserPassword("x-access-token", c.token)
	return u.String()
}

func splitRepo(full string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(full), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("target repo must use owner/repo format")
	}
	return parts[0], parts[1], nil
}