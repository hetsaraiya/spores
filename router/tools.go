package router

import (
	"context"
	"encoding/json"
	"fmt"

	"spore/githubclient"
)

// toolArgs is the decoded JSON arguments object from a tool call.
type toolArgs map[string]any

func (a toolArgs) str(key string) string {
	if v, ok := a[key].(string); ok {
		return v
	}
	return ""
}

func (a toolArgs) intVal(key string) int {
	switch v := a[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// schema helpers keep the JSON-Schema tool definitions readable.
func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

// tools returns the GitHub read tools plus the delegate tool.
func (r *Router) tools() []toolDef {
	repo := strProp("Target repository as owner/repo")
	ref := strProp("Branch, tag, or commit SHA (optional; defaults to the default branch)")
	return []toolDef{
		spec("github_get_file", "Read the contents of a single file in a repo.",
			obj(map[string]any{"repo": repo, "path": strProp("File path within the repo"), "ref": ref}, "repo", "path")),
		spec("github_list_dir", "List the entries of a directory in a repo.",
			obj(map[string]any{"repo": repo, "path": strProp("Directory path ('' for repo root)"), "ref": ref}, "repo", "path")),
		spec("github_tree", "List the full recursive file tree of a repo at a ref.",
			obj(map[string]any{"repo": repo, "ref": ref}, "repo")),
		spec("github_get_repo", "Get metadata about a repo (default branch, language, stars, description).",
			obj(map[string]any{"repo": repo}, "repo")),
		spec("github_list_repos", "List repositories the token can access.", obj(map[string]any{})),
		spec("github_list_branches", "List branch names of a repo.", obj(map[string]any{"repo": repo}, "repo")),
		spec("github_list_issues", "List issues in a repo.",
			obj(map[string]any{"repo": repo, "state": strProp("open, closed, or all")}, "repo")),
		spec("github_get_issue", "Get an issue's title, body, and comments.",
			obj(map[string]any{"repo": repo, "number": intProp("Issue number")}, "repo", "number")),
		spec("github_list_prs", "List pull requests in a repo.",
			obj(map[string]any{"repo": repo, "state": strProp("open, closed, or all")}, "repo")),
		spec("github_get_pr", "Get a pull request's title, body, and diff stats.",
			obj(map[string]any{"repo": repo, "number": intProp("PR number")}, "repo", "number")),
		spec("github_search_code", "Search code across repos the token can access.",
			obj(map[string]any{"query": strProp("GitHub code search query")}, "query")),
		spec("github_search_repos", "Search repositories.",
			obj(map[string]any{"query": strProp("GitHub repository search query")}, "query")),
		spec("create_github_issue", "Open a GitHub issue directly (no sandbox, no code changes, fast). Use this when the user only wants an issue created — to track, plan, or report work — rather than code written. Gather any needed details (e.g. a package list) with the github_* read tools first, then write a clear, well-structured issue.",
			obj(map[string]any{
				"repo":   repo,
				"title":  strProp("Concise issue title"),
				"body":   strProp("Markdown issue body"),
				"labels": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional labels, e.g. enhancement"},
			}, "repo", "title", "body")),
		spec("delegate_to_coder", "Hand off to the full coding agent (sandbox + Codex) to write, edit, or fix code. Use ONLY when the user explicitly asks for code changes. The agent does ONLY what your brief says and by default will NOT open a pull request or create an issue — you must say so explicitly when the user wants them.",
			obj(map[string]any{
				"task": strProp("The coding agent's complete brief: the target repo (owner/repo), exactly what to change, whether to open a pull request (only if the user asked; otherwise say not to), whether to create/reuse an issue (only if asked; otherwise say not to), and any stopping point."),
			}, "task")),
	}
}

// createIssue opens a GitHub issue directly via the read/write client, with no
// sandbox or Codex. The result is fed back to the model so it can confirm to
// the user in natural language.
func (r *Router) createIssue(ctx context.Context, args toolArgs) (string, error) {
	var labels []string
	if raw, ok := args["labels"].([]any); ok {
		for _, l := range raw {
			if s, ok := l.(string); ok {
				labels = append(labels, s)
			}
		}
	}
	num, issueURL, err := r.github.CreateIssue(ctx, githubclient.IssueRequest{
		Repo:   args.str("repo"),
		Title:  args.str("title"),
		Body:   args.str("body"),
		Labels: labels,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Created issue #%d: %s", num, issueURL), nil
}

func spec(name, desc string, params map[string]any) toolDef {
	return toolDef{Type: "function", Function: funcSchema{Name: name, Description: desc, Parameters: params}}
}

// dispatch runs one tool call and returns its textual result. The bool reports
// whether this was a delegation (which ends the router loop).
func (r *Router) dispatch(ctx context.Context, name, rawArgs, contextSummary string) (result string, delegated bool, err error) {
	ctx, run := r.tracer.Start(ctx, "tool: "+name, "tool", map[string]any{"arguments": rawArgs})
	defer func() { run.End(map[string]any{"result": result}, err) }()

	var args toolArgs
	if rawArgs != "" {
		if e := json.Unmarshal([]byte(rawArgs), &args); e != nil {
			return fmt.Sprintf("invalid arguments: %v", e), false, nil
		}
	}

	gh := r.github
	switch name {
	case "github_get_file":
		result, err = gh.GetFileContent(ctx, args.str("repo"), args.str("path"), args.str("ref"))
	case "github_list_dir":
		result, err = gh.ListDir(ctx, args.str("repo"), args.str("path"), args.str("ref"))
	case "github_tree":
		result, err = gh.GetTree(ctx, args.str("repo"), args.str("ref"))
	case "github_get_repo":
		result, err = gh.GetRepo(ctx, args.str("repo"))
	case "github_list_repos":
		result, err = gh.ListRepos(ctx)
	case "github_list_branches":
		result, err = gh.ListBranches(ctx, args.str("repo"))
	case "github_list_issues":
		result, err = gh.ListIssues(ctx, args.str("repo"), args.str("state"))
	case "github_get_issue":
		result, err = gh.GetIssueDetail(ctx, args.str("repo"), args.intVal("number"))
	case "github_list_prs":
		result, err = gh.ListPRs(ctx, args.str("repo"), args.str("state"))
	case "github_get_pr":
		result, err = gh.GetPRDetail(ctx, args.str("repo"), args.intVal("number"))
	case "github_search_code":
		result, err = gh.SearchCode(ctx, args.str("query"))
	case "github_search_repos":
		result, err = gh.SearchRepos(ctx, args.str("query"))
	case "create_github_issue":
		result, err = r.createIssue(ctx, args)
	case "delegate_to_coder":
		return r.delegate(ctx, args.str("task"), contextSummary), true, nil
	default:
		return fmt.Sprintf("unknown tool %q", name), false, nil
	}

	if err != nil {
		// Feed the error back to the model as a tool result instead of aborting;
		// it can adjust (e.g. wrong path) or report the failure to the user.
		return "error: " + err.Error(), false, nil
	}
	if result == "" {
		result = "(empty result)"
	}
	return result, false, nil
}
