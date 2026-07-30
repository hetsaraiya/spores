// Package tools exposes application capabilities to the LLM.
package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hetsaraiya/spores/internal/github"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"
)

// Declared once so the definitions and the dispatch switch cannot drift apart.
const (
	GitHubGetFile      = "github_get_file"
	GitHubListDir      = "github_list_dir"
	GitHubTree         = "github_tree"
	GitHubGetRepo      = "github_get_repo"
	GitHubListRepos    = "github_list_repos"
	GitHubListBranches = "github_list_branches"
	GitHubListIssues   = "github_list_issues"
	GitHubGetIssue     = "github_get_issue"
	GitHubListPRs      = "github_list_prs"
	GitHubGetPR        = "github_get_pr"
	GitHubSearchCode   = "github_search_code"
	GitHubSearchRepos  = "github_search_repos"
)

// Types are looked up rather than inferred from the name, so a new argument
// cannot silently get the wrong type because of what it is called.
const (
	argRepo   = "repo"
	argPath   = "path"
	argRef    = "ref"
	argState  = "state"
	argQuery  = "query"
	argNumber = "number"

	typeString  = "string"
	typeInteger = "integer"
)

var argTypes = map[string]string{
	argRepo:   typeString,
	argPath:   typeString,
	argRef:    typeString,
	argState:  typeString,
	argQuery:  typeString,
	argNumber: typeInteger,
}

func GitHubDefinitions() []openai.ChatCompletionToolUnionParam {
	return []openai.ChatCompletionToolUnionParam{
		definition(GitHubGetFile, "Read a file from a GitHub repository.", properties(argRepo, argPath, argRef), []string{argRepo, argPath}),
		definition(GitHubListDir, "List a directory in a GitHub repository.", properties(argRepo, argPath, argRef), []string{argRepo}),
		definition(GitHubTree, "Get the recursive file tree of a GitHub repository.", properties(argRepo, argRef), []string{argRepo}),
		definition(GitHubGetRepo, "Get metadata for a GitHub repository.", properties(argRepo), []string{argRepo}),
		definition(GitHubListRepos, "List repositories accessible to the configured GitHub account.", map[string]any{}, nil),
		definition(GitHubListBranches, "List branches in a GitHub repository.", properties(argRepo), []string{argRepo}),
		definition(GitHubListIssues, "List non-pull-request issues in a repository.", properties(argRepo, argState), []string{argRepo}),
		definition(GitHubGetIssue, "Get an issue and its comments.", properties(argRepo, argNumber), []string{argRepo, argNumber}),
		definition(GitHubListPRs, "List pull requests in a repository.", properties(argRepo, argState), []string{argRepo}),
		definition(GitHubGetPR, "Get pull request details.", properties(argRepo, argNumber), []string{argRepo, argNumber}),
		definition(GitHubSearchCode, "Search code on GitHub. Use GitHub code-search query syntax.", properties(argQuery), []string{argQuery}),
		definition(GitHubSearchRepos, "Search GitHub repositories.", properties(argQuery), []string{argQuery}),
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
		kind, known := argTypes[name]
		if !known {
			panic("tools: no declared JSON-schema type for argument " + name)
		}
		props[name] = map[string]any{"type": kind}
	}
	return props
}

// IntArg coerces a JSON tool argument to an int; models send numbers as floats
// and sometimes as strings.
func IntArg(value any) (int, bool) {
	switch value := value.(type) {
	case float64:
		return int(value), true
	case int:
		return value, true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

type arguments map[string]any

func (a arguments) text(key string) string { value, _ := a[key].(string); return value }

func (a arguments) number() (int, error) {
	value, present := a[argNumber]
	if !present {
		return 0, fmt.Errorf("%s is required", argNumber)
	}
	parsed, ok := IntArg(value)
	if !ok {
		return 0, fmt.Errorf("%s must be an integer", argNumber)
	}
	return parsed, nil
}

// githubHandlers is keyed by tool name, so the dispatch table can be checked
// against GitHubDefinitions without invoking anything.
var githubHandlers = map[string]func(context.Context, *github.Client, arguments) (string, error){
	GitHubGetFile: func(ctx context.Context, c *github.Client, a arguments) (string, error) {
		return c.GetFileContent(ctx, a.text(argRepo), a.text(argPath), a.text(argRef))
	},
	GitHubListDir: func(ctx context.Context, c *github.Client, a arguments) (string, error) {
		return c.ListDir(ctx, a.text(argRepo), a.text(argPath), a.text(argRef))
	},
	GitHubTree: func(ctx context.Context, c *github.Client, a arguments) (string, error) {
		return c.GetTree(ctx, a.text(argRepo), a.text(argRef))
	},
	GitHubGetRepo: func(ctx context.Context, c *github.Client, a arguments) (string, error) {
		return c.GetRepo(ctx, a.text(argRepo))
	},
	GitHubListRepos: func(ctx context.Context, c *github.Client, _ arguments) (string, error) {
		return c.ListRepos(ctx)
	},
	GitHubListBranches: func(ctx context.Context, c *github.Client, a arguments) (string, error) {
		return c.ListBranches(ctx, a.text(argRepo))
	},
	GitHubListIssues: func(ctx context.Context, c *github.Client, a arguments) (string, error) {
		return c.ListIssues(ctx, a.text(argRepo), a.text(argState))
	},
	GitHubGetIssue: func(ctx context.Context, c *github.Client, a arguments) (string, error) {
		number, err := a.number()
		if err != nil {
			return "", err
		}
		return c.GetIssueDetail(ctx, a.text(argRepo), number)
	},
	GitHubListPRs: func(ctx context.Context, c *github.Client, a arguments) (string, error) {
		return c.ListPRs(ctx, a.text(argRepo), a.text(argState))
	},
	GitHubGetPR: func(ctx context.Context, c *github.Client, a arguments) (string, error) {
		number, err := a.number()
		if err != nil {
			return "", err
		}
		return c.GetPRDetail(ctx, a.text(argRepo), number)
	},
	GitHubSearchCode: func(ctx context.Context, c *github.Client, a arguments) (string, error) {
		return c.SearchCode(ctx, a.text(argQuery))
	},
	GitHubSearchRepos: func(ctx context.Context, c *github.Client, a arguments) (string, error) {
		return c.SearchRepos(ctx, a.text(argQuery))
	},
}

// RunGitHub executes a github_* tool. known is false for tool names outside
// this package, allowing the caller to combine independent tool modules.
func RunGitHub(ctx context.Context, client *github.Client, name string, args map[string]any) (string, bool, error) {
	run, known := githubHandlers[name]
	if !known {
		return "", false, nil
	}
	result, err := run(ctx, client, arguments(args))
	return result, true, err
}
