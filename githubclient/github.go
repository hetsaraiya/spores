package githubclient

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/go-github/v60/github"
	"golang.org/x/oauth2"
)

type IssueRequest struct {
	Repo   string
	Title  string
	Body   string
	Labels []string
}

type Issue struct {
	Repo   string
	Number int
	Title  string
	Body   string
	URL    string
}

type PRRequest struct {
	Repo  string
	Title string
	Body  string
	Head  string
	Base  string
}

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

func (c *Client) CreateIssue(ctx context.Context, r IssueRequest) (int, string, error) {
	owner, repo, err := splitRepo(r.Repo)
	if err != nil {
		return 0, "", err
	}
	issue := &github.IssueRequest{
		Title:  github.String(r.Title),
		Body:   github.String(r.Body),
		Labels: &r.Labels,
	}
	out, _, err := c.api.Issues.Create(ctx, owner, repo, issue)
	if err != nil {
		return 0, "", err
	}
	return out.GetNumber(), out.GetHTMLURL(), nil
}

func (c *Client) CreatePR(ctx context.Context, r PRRequest) (string, error) {
	owner, repo, err := splitRepo(r.Repo)
	if err != nil {
		return "", err
	}
	pr := &github.NewPullRequest{
		Title: github.String(r.Title),
		Body:  github.String(r.Body),
		Head:  github.String(r.Head),
		Base:  github.String(r.Base),
	}
	out, _, err := c.api.PullRequests.Create(ctx, owner, repo, pr)
	if err != nil {
		return "", err
	}
	return out.GetHTMLURL(), nil
}

func (c *Client) GetIssue(ctx context.Context, full string, number int) (Issue, error) {
	owner, repo, err := splitRepo(full)
	if err != nil {
		return Issue{}, err
	}
	out, _, err := c.api.Issues.Get(ctx, owner, repo, number)
	if err != nil {
		return Issue{}, err
	}
	return Issue{Repo: full, Number: out.GetNumber(), Title: out.GetTitle(), Body: out.GetBody(), URL: out.GetHTMLURL()}, nil
}

// CloneURL returns the plain https clone URL. Credentials are injected via
// the git credential store (see CredentialsLine) so the token never appears
// in command lines, logs, or the cloned repo's .git/config.
func (c *Client) CloneURL(repo string) string {
	u := &url.URL{Scheme: "https", Host: "github.com", Path: repo + ".git"}
	return u.String()
}

// CredentialsLine returns the line for a git credential store file
// (credential.helper store) granting access to github.com.
func (c *Client) CredentialsLine() string {
	u := &url.URL{Scheme: "https", Host: "github.com"}
	u.User = url.UserPassword("x-access-token", c.token)
	return u.String()
}

// DefaultBranch returns the repository's default branch name.
func (c *Client) DefaultBranch(ctx context.Context, full string) (string, error) {
	owner, repo, err := splitRepo(full)
	if err != nil {
		return "", err
	}
	r, _, err := c.api.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return "", err
	}
	return r.GetDefaultBranch(), nil
}

func splitRepo(full string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(full), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("target repo must use owner/repo format")
	}
	return parts[0], parts[1], nil
}
