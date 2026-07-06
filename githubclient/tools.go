package githubclient

import "context"

type githubTool struct {
	Name string
	Run  func(ctx context.Context, c *Client, args map[string]string) (string, error)
}

func githubTools() []githubTool {
	return []githubTool{
		{Name: "github_get_file", Run: func(ctx context.Context, c *Client, a map[string]string) (string, error) {
			return c.GetFileContent(ctx, a["repo"], a["path"], a["ref"])
		}},
		{Name: "github_list_dir", Run: func(ctx context.Context, c *Client, a map[string]string) (string, error) {
			return c.ListDir(ctx, a["repo"], a["path"], a["ref"])
		}},
		{Name: "github_tree", Run: func(ctx context.Context, c *Client, a map[string]string) (string, error) {
			return c.GetTree(ctx, a["repo"], a["ref"])
		}},
		{Name: "github_get_repo", Run: func(ctx context.Context, c *Client, a map[string]string) (string, error) {
			return c.GetRepo(ctx, a["repo"])
		}},
		{Name: "github_list_repos", Run: func(ctx context.Context, c *Client, _ map[string]string) (string, error) {
			return c.ListRepos(ctx)
		}},
		{Name: "github_list_branches", Run: func(ctx context.Context, c *Client, a map[string]string) (string, error) {
			return c.ListBranches(ctx, a["repo"])
		}},
		{Name: "github_list_issues", Run: func(ctx context.Context, c *Client, a map[string]string) (string, error) {
			return c.ListIssues(ctx, a["repo"], a["state"])
		}},
		{Name: "github_get_issue", Run: func(ctx context.Context, c *Client, a map[string]string) (string, error) {
			return c.GetIssueDetail(ctx, a["repo"], atoi(a["number"]))
		}},
		{Name: "github_list_prs", Run: func(ctx context.Context, c *Client, a map[string]string) (string, error) {
			return c.ListPRs(ctx, a["repo"], a["state"])
		}},
		{Name: "github_get_pr", Run: func(ctx context.Context, c *Client, a map[string]string) (string, error) {
			return c.GetPRDetail(ctx, a["repo"], atoi(a["number"]))
		}},
		{Name: "github_search_code", Run: func(ctx context.Context, c *Client, a map[string]string) (string, error) {
			return c.SearchCode(ctx, a["query"])
		}},
		{Name: "github_search_repos", Run: func(ctx context.Context, c *Client, a map[string]string) (string, error) {
			return c.SearchRepos(ctx, a["query"])
		}},
	}
}

func atoi(s string) int {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// RunGitHubTool runs a named github_* tool. ok is false for unknown tools.
func (c *Client) RunGitHubTool(ctx context.Context, name string, args map[string]string) (result string, ok bool, err error) {
	for _, t := range githubTools() {
		if t.Name != name {
			continue
		}
		result, err = t.Run(ctx, c, args)
		return result, true, err
	}
	return "", false, nil
}