package githubclient

import (
	"context"
	"strconv"
)

// RunGitHubTool runs a named github_* tool. ok is false for unknown tools.
func (c *Client) RunGitHubTool(ctx context.Context, name string, args map[string]string) (result string, ok bool, err error) {
	num := func(key string) int {
		n, _ := strconv.Atoi(args[key])
		return n
	}
	switch name {
	case "github_get_file":
		result, err = c.GetFileContent(ctx, args["repo"], args["path"], args["ref"])
	case "github_list_dir":
		result, err = c.ListDir(ctx, args["repo"], args["path"], args["ref"])
	case "github_tree":
		result, err = c.GetTree(ctx, args["repo"], args["ref"])
	case "github_get_repo":
		result, err = c.GetRepo(ctx, args["repo"])
	case "github_list_repos":
		result, err = c.ListRepos(ctx)
	case "github_list_branches":
		result, err = c.ListBranches(ctx, args["repo"])
	case "github_list_issues":
		result, err = c.ListIssues(ctx, args["repo"], args["state"])
	case "github_get_issue":
		result, err = c.GetIssueDetail(ctx, args["repo"], num("number"))
	case "github_list_prs":
		result, err = c.ListPRs(ctx, args["repo"], args["state"])
	case "github_get_pr":
		result, err = c.GetPRDetail(ctx, args["repo"], num("number"))
	case "github_search_code":
		result, err = c.SearchCode(ctx, args["query"])
	case "github_search_repos":
		result, err = c.SearchRepos(ctx, args["query"])
	default:
		return "", false, nil
	}
	return result, true, err
}
