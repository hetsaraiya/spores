package github

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const (
	stateOpen   = "open"
	stateClosed = "closed"
	stateAll    = "all"
)

// normalizeState defaults a blank state and rejects values GitHub would reject.
func normalizeState(state string) (string, error) {
	switch state {
	case "":
		return stateOpen, nil
	case stateOpen, stateClosed, stateAll:
		return state, nil
	default:
		return "", fmt.Errorf("state must be %s, %s, or %s", stateOpen, stateClosed, stateAll)
	}
}

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
	state, err = normalizeState(state)
	if err != nil {
		return "", err
	}
	var issues []issue
	if err := c.get(ctx, fmt.Sprintf("%s/issues?state=%s&per_page=%d", base, state, listPageSize), &issues); err != nil {
		return "", err
	}
	var out strings.Builder
	for _, issue := range issues {
		if issue.PullRequest != nil {
			continue
		}
		fmt.Fprintf(&out, "#%d [%s] %s\n", issue.Number, issue.State, issue.Title)
	}
	notePartialPage(&out, len(issues), listPageSize)
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
	// Reported inline, not swallowed: an issue missing its comments is otherwise
	// indistinguishable from one that has none.
	if err := c.get(ctx, fmt.Sprintf("%s/issues/%d/comments?per_page=%d", base, number, listPageSize), &comments); err != nil {
		fmt.Fprintf(&out, "\n[comments could not be retrieved: %v]\n", err)
	} else {
		for _, comment := range comments {
			fmt.Fprintf(&out, "\n--- %s ---\n%s\n", comment.User.Login, comment.Body)
		}
		notePartialPage(&out, len(comments), listPageSize)
	}
	return clip(out.String()), nil
}

func (c *Client) ListPRs(ctx context.Context, full, state string) (string, error) {
	base, err := repoPath(full)
	if err != nil {
		return "", err
	}
	state, err = normalizeState(state)
	if err != nil {
		return "", err
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
	if err := c.get(ctx, fmt.Sprintf("%s/pulls?state=%s&per_page=%d", base, state, listPageSize), &prs); err != nil {
		return "", err
	}
	var out strings.Builder
	for _, pr := range prs {
		fmt.Fprintf(&out, "#%d [%s] %s (%s -> %s)\n", pr.Number, pr.State, pr.Title, pr.Head.Ref, pr.Base.Ref)
	}
	notePartialPage(&out, len(prs), listPageSize)
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
