package github

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

type repository struct {
	FullName        string `json:"full_name"`
	DefaultBranch   string `json:"default_branch"`
	Language        string `json:"language"`
	StargazersCount int    `json:"stargazers_count"`
	OpenIssuesCount int    `json:"open_issues_count"`
	Description     string `json:"description"`
	HTMLURL         string `json:"html_url"`
}

func (c *Client) GetRepo(ctx context.Context, full string) (string, error) {
	endpoint, err := repoPath(full)
	if err != nil {
		return "", err
	}
	var repo repository
	if err := c.get(ctx, endpoint, &repo); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s\ndefault_branch: %s\nlanguage: %s\nstars: %d\nopen_issues: %d\ndescription: %s\nurl: %s",
		repo.FullName, repo.DefaultBranch, repo.Language, repo.StargazersCount, repo.OpenIssuesCount, repo.Description, repo.HTMLURL), nil
}

func (c *Client) ListRepos(ctx context.Context) (string, error) {
	var repos []repository
	if err := c.get(ctx, "/user/repos?sort=updated&per_page=100", &repos); err != nil {
		return "", err
	}
	var out strings.Builder
	for _, repo := range repos {
		fmt.Fprintf(&out, "%s\t%s\n", repo.FullName, repo.Description)
	}
	return clip(out.String()), nil
}

func (c *Client) SearchCode(ctx context.Context, query string) (string, error) {
	var result struct {
		TotalCount int `json:"total_count"`
		Items      []struct {
			Path       string `json:"path"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
		} `json:"items"`
	}
	if err := c.get(ctx, "/search/code?q="+url.QueryEscape(query)+"&per_page=30", &result); err != nil {
		return "", err
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%d total matches\n", result.TotalCount)
	for _, item := range result.Items {
		fmt.Fprintf(&out, "%s : %s\n", item.Repository.FullName, item.Path)
	}
	return clip(out.String()), nil
}

func (c *Client) SearchRepos(ctx context.Context, query string) (string, error) {
	var result struct {
		TotalCount int          `json:"total_count"`
		Items      []repository `json:"items"`
	}
	if err := c.get(ctx, "/search/repositories?q="+url.QueryEscape(query)+"&per_page=30", &result); err != nil {
		return "", err
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%d total matches\n", result.TotalCount)
	for _, repo := range result.Items {
		fmt.Fprintf(&out, "%s\t%s\n", repo.FullName, repo.Description)
	}
	return clip(out.String()), nil
}
