package githubclient

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v60/github"
)

// maxBody bounds any single field we hand back to the router LLM so a giant file
// or issue body can't blow up the context window.
const maxBody = 8000

func clip(s string) string {
	if len(s) > maxBody {
		return s[:maxBody] + "\n... [truncated]"
	}
	return s
}

// GetFileContent returns the decoded contents of a file at an optional ref
// (branch, tag, or commit SHA; empty means the default branch).
func (c *Client) GetFileContent(ctx context.Context, full, path, ref string) (string, error) {
	owner, repo, err := splitRepo(full)
	if err != nil {
		return "", err
	}
	opts := &github.RepositoryContentGetOptions{Ref: ref}
	file, _, _, err := c.api.Repositories.GetContents(ctx, owner, repo, path, opts)
	if err != nil {
		return "", err
	}
	if file == nil {
		return "", fmt.Errorf("%s is a directory, not a file", path)
	}
	content, err := file.GetContent()
	if err != nil {
		return "", err
	}
	return clip(content), nil
}

// ListDir lists the entries of a directory at an optional ref.
func (c *Client) ListDir(ctx context.Context, full, path, ref string) (string, error) {
	owner, repo, err := splitRepo(full)
	if err != nil {
		return "", err
	}
	opts := &github.RepositoryContentGetOptions{Ref: ref}
	_, dir, _, err := c.api.Repositories.GetContents(ctx, owner, repo, path, opts)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, e := range dir {
		fmt.Fprintf(&b, "%s\t%s\n", e.GetType(), e.GetPath())
	}
	return clip(b.String()), nil
}

// GetTree returns the recursive file tree of a repo at a ref (branch/tag/SHA).
func (c *Client) GetTree(ctx context.Context, full, ref string) (string, error) {
	owner, repo, err := splitRepo(full)
	if err != nil {
		return "", err
	}
	if ref == "" {
		ref = "HEAD"
	}
	tree, _, err := c.api.Git.GetTree(ctx, owner, repo, ref, true)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, e := range tree.Entries {
		fmt.Fprintf(&b, "%s\t%s\n", e.GetType(), e.GetPath())
	}
	if tree.GetTruncated() {
		b.WriteString("... [tree truncated by GitHub]\n")
	}
	return clip(b.String()), nil
}

// ListBranches lists branch names for a repo.
func (c *Client) ListBranches(ctx context.Context, full string) (string, error) {
	owner, repo, err := splitRepo(full)
	if err != nil {
		return "", err
	}
	branches, _, err := c.api.Repositories.ListBranches(ctx, owner, repo, &github.BranchListOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, br := range branches {
		fmt.Fprintf(&b, "%s\n", br.GetName())
	}
	return clip(b.String()), nil
}

// GetRepo returns metadata about a repository.
func (c *Client) GetRepo(ctx context.Context, full string) (string, error) {
	owner, repo, err := splitRepo(full)
	if err != nil {
		return "", err
	}
	r, _, err := c.api.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s\ndefault_branch: %s\nlanguage: %s\nstars: %d\nopen_issues: %d\ndescription: %s\nurl: %s",
		r.GetFullName(), r.GetDefaultBranch(), r.GetLanguage(), r.GetStargazersCount(),
		r.GetOpenIssuesCount(), r.GetDescription(), r.GetHTMLURL()), nil
}

// ListRepos lists repositories accessible to the token (authenticated user).
func (c *Client) ListRepos(ctx context.Context) (string, error) {
	repos, _, err := c.api.Repositories.ListByAuthenticatedUser(ctx, &github.RepositoryListByAuthenticatedUserOptions{
		Sort:        "updated",
		ListOptions: github.ListOptions{PerPage: 100},
	})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, r := range repos {
		fmt.Fprintf(&b, "%s\t%s\n", r.GetFullName(), r.GetDescription())
	}
	return clip(b.String()), nil
}

// ListIssues lists issues (excluding PRs) for a repo. state is open/closed/all.
func (c *Client) ListIssues(ctx context.Context, full, state string) (string, error) {
	owner, repo, err := splitRepo(full)
	if err != nil {
		return "", err
	}
	if state == "" {
		state = "open"
	}
	issues, _, err := c.api.Issues.ListByRepo(ctx, owner, repo, &github.IssueListByRepoOptions{
		State:       state,
		ListOptions: github.ListOptions{PerPage: 50},
	})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, is := range issues {
		if is.IsPullRequest() {
			continue
		}
		fmt.Fprintf(&b, "#%d [%s] %s\n", is.GetNumber(), is.GetState(), is.GetTitle())
	}
	return clip(b.String()), nil
}

// GetIssueDetail returns an issue's title, body, and comments.
func (c *Client) GetIssueDetail(ctx context.Context, full string, number int) (string, error) {
	owner, repo, err := splitRepo(full)
	if err != nil {
		return "", err
	}
	is, _, err := c.api.Issues.Get(ctx, owner, repo, number)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "#%d [%s] %s\n%s\n%s\n", is.GetNumber(), is.GetState(), is.GetTitle(), is.GetHTMLURL(), is.GetBody())
	comments, _, err := c.api.Issues.ListComments(ctx, owner, repo, number, &github.IssueListCommentsOptions{
		ListOptions: github.ListOptions{PerPage: 50},
	})
	if err == nil {
		for _, cm := range comments {
			fmt.Fprintf(&b, "\n--- %s ---\n%s\n", cm.GetUser().GetLogin(), cm.GetBody())
		}
	}
	return clip(b.String()), nil
}

// ListPRs lists pull requests for a repo. state is open/closed/all.
func (c *Client) ListPRs(ctx context.Context, full, state string) (string, error) {
	owner, repo, err := splitRepo(full)
	if err != nil {
		return "", err
	}
	if state == "" {
		state = "open"
	}
	prs, _, err := c.api.PullRequests.List(ctx, owner, repo, &github.PullRequestListOptions{
		State:       state,
		ListOptions: github.ListOptions{PerPage: 50},
	})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, pr := range prs {
		fmt.Fprintf(&b, "#%d [%s] %s (%s -> %s)\n", pr.GetNumber(), pr.GetState(), pr.GetTitle(), pr.GetHead().GetRef(), pr.GetBase().GetRef())
	}
	return clip(b.String()), nil
}

// GetPRDetail returns a pull request's title, body, diff stats, and comments.
func (c *Client) GetPRDetail(ctx context.Context, full string, number int) (string, error) {
	owner, repo, err := splitRepo(full)
	if err != nil {
		return "", err
	}
	pr, _, err := c.api.PullRequests.Get(ctx, owner, repo, number)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "#%d [%s] %s\n%s\nhead: %s base: %s\n+%d -%d across %d files\n\n%s\n",
		pr.GetNumber(), pr.GetState(), pr.GetTitle(), pr.GetHTMLURL(),
		pr.GetHead().GetRef(), pr.GetBase().GetRef(), pr.GetAdditions(), pr.GetDeletions(), pr.GetChangedFiles(), pr.GetBody())
	return clip(b.String()), nil
}

// SearchCode runs a GitHub code search query.
func (c *Client) SearchCode(ctx context.Context, query string) (string, error) {
	res, _, err := c.api.Search.Code(ctx, query, &github.SearchOptions{
		ListOptions: github.ListOptions{PerPage: 30},
	})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d total matches\n", res.GetTotal())
	for _, item := range res.CodeResults {
		fmt.Fprintf(&b, "%s : %s\n", item.GetRepository().GetFullName(), item.GetPath())
	}
	return clip(b.String()), nil
}

// SearchRepos runs a GitHub repository search query.
func (c *Client) SearchRepos(ctx context.Context, query string) (string, error) {
	res, _, err := c.api.Search.Repositories(ctx, query, &github.SearchOptions{
		ListOptions: github.ListOptions{PerPage: 30},
	})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d total matches\n", res.GetTotal())
	for _, r := range res.Repositories {
		fmt.Fprintf(&b, "%s\t%s\n", r.GetFullName(), r.GetDescription())
	}
	return clip(b.String()), nil
}
