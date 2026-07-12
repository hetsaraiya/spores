// Package tools exposes application capabilities to the LLM.
package tools

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hetsaraiya/spores/internal/github"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"
)

func GitHubDefinitions() []openai.ChatCompletionToolUnionParam {
	return []openai.ChatCompletionToolUnionParam{
		definition("github_get_file", "Read a file from a GitHub repository.", properties("repo", "path", "ref"), []string{"repo", "path"}),
		definition("github_list_dir", "List a directory in a GitHub repository.", properties("repo", "path", "ref"), []string{"repo"}),
		definition("github_tree", "Get the recursive file tree of a GitHub repository.", properties("repo", "ref"), []string{"repo"}),
		definition("github_get_repo", "Get metadata for a GitHub repository.", properties("repo"), []string{"repo"}),
		definition("github_list_repos", "List repositories accessible to the configured GitHub account.", map[string]any{}, nil),
		definition("github_list_branches", "List branches in a GitHub repository.", properties("repo"), []string{"repo"}),
		definition("github_list_issues", "List non-pull-request issues in a repository.", properties("repo", "state"), []string{"repo"}),
		definition("github_get_issue", "Get an issue and its comments.", properties("repo", "number"), []string{"repo", "number"}),
		definition("github_list_prs", "List pull requests in a repository.", properties("repo", "state"), []string{"repo"}),
		definition("github_get_pr", "Get pull request details.", properties("repo", "number"), []string{"repo", "number"}),
		definition("github_search_code", "Search code on GitHub. Use GitHub code-search query syntax.", properties("query"), []string{"query"}),
		definition("github_search_repos", "Search GitHub repositories.", properties("query"), []string{"query"}),
	}
}

func definition(name, description string, props map[string]any, required []string) openai.ChatCompletionToolUnionParam {
	return openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
		Name: name, Description: openai.String(description),
		Parameters: shared.FunctionParameters{"type": "object", "properties": props, "required": required},
	})
}

func properties(names ...string) map[string]any {
	props := make(map[string]any, len(names))
	for _, name := range names {
		kind := "string"
		if name == "number" {
			kind = "integer"
		}
		props[name] = map[string]any{"type": kind}
	}
	return props
}

// RunGitHub executes a github_* tool. known is false for tool names outside
// this package, allowing the caller to combine independent tool modules.
func RunGitHub(ctx context.Context, client *github.Client, name string, args map[string]any) (result string, known bool, err error) {
	text := func(key string) string { value, _ := args[key].(string); return value }
	number := func() (int, error) {
		value, ok := args["number"]
		if !ok {
			return 0, fmt.Errorf("number is required")
		}
		switch value := value.(type) {
		case float64:
			return int(value), nil
		case string:
			return strconv.Atoi(value)
		default:
			return 0, fmt.Errorf("number must be an integer")
		}
	}

	switch name {
	case "github_get_file":
		result, err = client.GetFileContent(ctx, text("repo"), text("path"), text("ref"))
	case "github_list_dir":
		result, err = client.ListDir(ctx, text("repo"), text("path"), text("ref"))
	case "github_tree":
		result, err = client.GetTree(ctx, text("repo"), text("ref"))
	case "github_get_repo":
		result, err = client.GetRepo(ctx, text("repo"))
	case "github_list_repos":
		result, err = client.ListRepos(ctx)
	case "github_list_branches":
		result, err = client.ListBranches(ctx, text("repo"))
	case "github_list_issues":
		result, err = client.ListIssues(ctx, text("repo"), text("state"))
	case "github_get_issue":
		var n int
		n, err = number()
		if err == nil {
			result, err = client.GetIssueDetail(ctx, text("repo"), n)
		}
	case "github_list_prs":
		result, err = client.ListPRs(ctx, text("repo"), text("state"))
	case "github_get_pr":
		var n int
		n, err = number()
		if err == nil {
			result, err = client.GetPRDetail(ctx, text("repo"), n)
		}
	case "github_search_code":
		result, err = client.SearchCode(ctx, text("query"))
	case "github_search_repos":
		result, err = client.SearchRepos(ctx, text("query"))
	default:
		return "", false, nil
	}
	return result, true, err
}
