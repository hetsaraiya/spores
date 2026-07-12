package github

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type issue struct {
	Number      int    `json:"number"`
	State       string `json:"state"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	HTMLURL     string `json:"html_url"`
	PullRequest any    `json:"pull_request"`
}

func (c *Client) ListIssues(ctx context.Context, full, state string) (string, error) {
	base, err := repoPath(full)
	if err != nil {
		return "", err
	}
	if state == "" {
		state = "open"
	}
	if state != "open" && state != "closed" && state != "all" {
		return "", fmt.Errorf("state must be open, closed, or all")
	}
	var issues []issue
	if err := c.get(ctx, base+"/issues?state="+state+"&per_page=50", &issues); err != nil {
		return "", err
	}
	var out strings.Builder
	for _, issue := range issues {
		if issue.PullRequest != nil {
			continue
		}
		fmt.Fprintf(&out, "#%d [%s] %s\n", issue.Number, issue.State, issue.Title)
	}
	return clip(out.String()), nil
}

func (c *Client) GetIssueDetail(ctx context.Context, full string, number int) (string, error) {
	base, err := repoPath(full)
	if err != nil {
		return "", err
	}
	if number <= 0 {
		return "", fmt.Errorf("number must be positive")
	}
	var issue issue
	if err := c.get(ctx, base+"/issues/"+strconv.Itoa(number), &issue); err != nil {
		return "", err
	}
	var out strings.Builder
	fmt.Fprintf(&out, "#%d [%s] %s\n%s\n%s\n", issue.Number, issue.State, issue.Title, issue.HTMLURL, issue.Body)
	var comments []struct {
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := c.get(ctx, base+"/issues/"+strconv.Itoa(number)+"/comments?per_page=50", &comments); err == nil {
		for _, comment := range comments {
			fmt.Fprintf(&out, "\n--- %s ---\n%s\n", comment.User.Login, comment.Body)
		}
	}
	return clip(out.String()), nil
}

func (c *Client) ListPRs(ctx context.Context, full, state string) (string, error) {
	base, err := repoPath(full)
	if err != nil {
		return "", err
	}
	if state == "" {
		state = "open"
	}
	if state != "open" && state != "closed" && state != "all" {
		return "", fmt.Errorf("state must be open, closed, or all")
	}
	var prs []struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		Title  string `json:"title"`
		Head   struct {
			Ref string `json:"ref"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	if err := c.get(ctx, base+"/pulls?state="+state+"&per_page=50", &prs); err != nil {
		return "", err
	}
	var out strings.Builder
	for _, pr := range prs {
		fmt.Fprintf(&out, "#%d [%s] %s (%s -> %s)\n", pr.Number, pr.State, pr.Title, pr.Head.Ref, pr.Base.Ref)
	}
	return clip(out.String()), nil
}

func (c *Client) GetPRDetail(ctx context.Context, full string, number int) (string, error) {
	base, err := repoPath(full)
	if err != nil {
		return "", err
	}
	if number <= 0 {
		return "", fmt.Errorf("number must be positive")
	}
	var pr struct {
		Number       int    `json:"number"`
		State        string `json:"state"`
		Title        string `json:"title"`
		HTMLURL      string `json:"html_url"`
		Body         string `json:"body"`
		Additions    int    `json:"additions"`
		Deletions    int    `json:"deletions"`
		ChangedFiles int    `json:"changed_files"`
		Head         struct {
			Ref string `json:"ref"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	if err := c.get(ctx, base+"/pulls/"+strconv.Itoa(number), &pr); err != nil {
		return "", err
	}
	return clip(fmt.Sprintf("#%d [%s] %s\n%s\nhead: %s base: %s\n+%d -%d across %d files\n\n%s\n", pr.Number, pr.State, pr.Title, pr.HTMLURL, pr.Head.Ref, pr.Base.Ref, pr.Additions, pr.Deletions, pr.ChangedFiles, pr.Body)), nil
}
