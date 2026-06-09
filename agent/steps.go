package agent

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"spore/githubclient"
	"spore/sandbox"
)

func (a *Agent) parseIssue(sb *sandbox.Sandbox, msg string) (issueDraft, error) {
	var issue issueDraft
	err := a.codexJSON(sb, issuePrompt+"\n\nUser message:\n"+msg, &issue)
	return issue, err
}

func (a *Agent) existingIssue(ctx context.Context, msg string) (issueDraft, int, string, bool, error) {
	repo, number, ok := issueURL(msg)
	if !ok {
		return issueDraft{}, 0, "", false, nil
	}
	issue, err := a.github.GetIssue(ctx, repo, number)
	if err != nil {
		return issueDraft{}, 0, "", true, err
	}
	return issueDraft{Repo: repo, Title: issue.Title, Body: issue.Body}, issue.Number, issue.URL, true, nil
}

func (a *Agent) createIssue(ctx context.Context, issue issueDraft) (int, string, error) {
	return a.github.CreateIssue(ctx, githubclient.IssueRequest{
		Repo: issue.Repo, Title: issue.Title, Body: issue.Body, Labels: issue.Labels,
	})
}

func (a *Agent) spinSandbox(ctx context.Context) (*sandbox.Sandbox, error) {
	return sandbox.New(ctx, a.e2bKey, os.Stdout)
}

func (a *Agent) cloneRepo(sb *sandbox.Sandbox, repo string, issue int, title string) (string, error) {
	branch := "fix/" + strconv.Itoa(issue) + "-" + slug(title)
	cmd := "git clone " + sandbox.Quote(a.github.CloneURL(repo)) + " /home/user/repo"
	if _, _, err := sb.RunCommand(cmd); err != nil {
		return "", err
	}
	_, _, err := sb.RunCommand("cd /home/user/repo && git checkout -b " + sandbox.Quote(branch))
	return branch, err
}

func (a *Agent) implementFix(sb *sandbox.Sandbox, issue issueDraft, priorErr string) ([]change, error) {
	tree, err := sb.ListFiles("/home/user/repo")
	if err != nil {
		return nil, err
	}
	out, err := sb.RunCodex("/home/user/repo", a.codexModel, codePrompt(issue, tree, priorErr))
	return []change{{Output: out}}, err
}

func (a *Agent) applyChanges(sb *sandbox.Sandbox, changes []change) error {
	if len(changes) == 0 {
		return fmt.Errorf("Codex returned no implementation result")
	}
	out, stderr, err := sb.RunCommand("cd /home/user/repo && git status --porcelain")
	if err != nil {
		return fmt.Errorf("%w\n%s%s", err, out, stderr)
	}
	if strings.TrimSpace(out) == "" {
		return fmt.Errorf("Codex made no repository changes: %s", changes[0].Output)
	}
	return nil
}

func (a *Agent) summarizeRun(sb *sandbox.Sandbox, summaryCtx runSummaryContext) (string, error) {
	gitStatus, _, _ := sb.RunCommand("cd /home/user/repo && git status --short")
	diffStat, _, _ := sb.RunCommand("cd /home/user/repo && git show --stat --oneline --decorate --no-renames HEAD")
	lastCommit, _, _ := sb.RunCommand("cd /home/user/repo && git log -1 --pretty=fuller --stat")
	return sb.RunCodex("/home/user/repo", a.codexModel, summaryPrompt(summaryCtx, gitStatus, diffStat, lastCommit))
}

func changeOutputs(changes []change) string {
	var parts []string
	for _, ch := range changes {
		out := strings.TrimSpace(ch.Output)
		if out != "" {
			parts = append(parts, out)
		}
	}
	if len(parts) == 0 {
		return "(no Codex output captured)"
	}
	return strings.Join(parts, "\n\n---\n\n")
}

func (a *Agent) commitPush(sb *sandbox.Sandbox, branch string, issue int, title string) error {
	cmds := []string{
		`cd /home/user/repo && git config user.email "bot@agent.dev"`,
		`cd /home/user/repo && git config user.name "Slack Agent"`,
		"cd /home/user/repo && git add .",
		"cd /home/user/repo && git commit -m " + sandbox.Quote(fmt.Sprintf("fix: %s (closes #%d)", title, issue)),
		"cd /home/user/repo && git push origin " + sandbox.Quote(branch),
	}
	for _, cmd := range cmds {
		if _, _, err := sb.RunCommand(cmd); err != nil {
			return err
		}
	}
	return nil
}
