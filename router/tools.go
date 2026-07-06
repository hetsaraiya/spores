package router

import (
	"context"
	"encoding/json"
	"fmt"
)

// toolArgs is the decoded JSON arguments object from a tool call.
type toolArgs map[string]any

func (a toolArgs) str(key string) string {
	if v, ok := a[key].(string); ok {
		return v
	}
	return ""
}

// strings flattens decoded tool args for the GitHub client (numbers as decimal strings).
func (a toolArgs) strings() map[string]string {
	out := make(map[string]string, len(a))
	for k, v := range a {
		switch x := v.(type) {
		case string:
			out[k] = x
		case float64:
			out[k] = fmt.Sprintf("%d", int(x))
		case int:
			out[k] = fmt.Sprintf("%d", x)
		}
	}
	return out
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
		spec("delegate_to_coder", "Hand off to the full coding agent (sandbox + Codex) to write, edit, or fix code, open a pull request, or create a GitHub issue. Use ONLY when the user explicitly asks for one of those. The agent does ONLY what your brief says and by default will NOT open a pull request or create an issue — you must say so explicitly when the user wants them.",
			obj(map[string]any{
				"task": strProp("The coding agent's complete brief: the target repo (owner/repo), exactly what to change, whether to open a pull request (only if the user asked; otherwise say not to), whether to create/reuse an issue (only if asked; otherwise say not to), and any stopping point."),
			}, "task")),
	}
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

	if name == "delegate_to_coder" {
		return r.delegate(ctx, args.str("task"), contextSummary), true, nil
	}
	result, ok, err := r.github.RunGitHubTool(ctx, name, args.strings())
	if !ok {
		return fmt.Sprintf("unknown tool %q", name), false, nil
	}

	if err != nil {
		// Feed errors back as a tool result so the model can adjust or report them.
		return "error: " + err.Error(), false, nil
	}
	if result == "" {
		result = "(empty result)"
	}
	return result, false, nil
}
