package agent

import "fmt"

const issuePrompt = `You are a GitHub Issue Creator Agent.
Convert the user's message into a GitHub issue. Extract repo from the message.
If the target repository is unclear, set repo to an empty string.
Respond ONLY with valid JSON, no markdown fences:
{
  "repo": "target GitHub repository as owner/repo",
  "title": "short issue title max 80 chars",
  "body": "detailed markdown body using this structure:\n## Description\n[what needs to be done]\n\n## Acceptance Criteria\n- [ ] criteria one\n- [ ] criteria two\n\n## Technical Notes\n[implementation hints if any]",
  "labels": ["bug" or "enhancement" or "documentation" or "question"],
  "implementation_hint": "one sentence describing what code change is likely needed"
}`

func summaryPrompt(ctx runSummaryContext, gitStatus, diffStat, lastCommit string) string {
	return fmt.Sprintf(`You are the coding agent that just finished working inside an E2B sandbox.
The sandbox is about to close. Write a concise Slack-friendly final report for the user.
Use the original user request plus all implementation context below.
Explain what was done and how it was done. Mention important files/areas when known.
Do not claim anything not supported by the context. Do not include markdown code fences.

Original user message / delegated task:
%s

Issue #%d: %s
Issue URL: %s
Issue body:
%s
Implementation hint: %s

Branch: %s
Pull Request: %s

Codex implementation output:
%s

Git status:
%s

Diff stat:
%s

Last commit:
%s

Return this structure:
*What changed*
- ...

*How it was done*
- ...

*Links*
- Issue: ...
- PR: ...`, ctx.OriginalMessage, ctx.IssueNumber, ctx.Issue.Title, ctx.IssueURL, ctx.Issue.Body, ctx.Issue.Hint, ctx.Branch, ctx.PRURL, changeOutputs(ctx.Changes), gitStatus, diffStat, lastCommit)
}

func codePrompt(issue issueDraft, tree, priorErr string) string {
	prompt := fmt.Sprintf(`You are a senior software engineer.
You have been given a GitHub issue to implement.
Edit this repository directly. It may use any language or toolchain.
Inspect the relevant files and make the smallest coherent implementation.
Do not commit, push, or open a pull request.

Issue Title: %s
Issue Body: %s
Implementation Hint: %s

Repo file tree:
%s

Leave the working tree with the implementation applied.
Finish with a concise summary of the changes.`, issue.Title, issue.Body, issue.Hint, tree)
	if priorErr != "" {
		prompt += "\n\nA previous implementation attempt failed validation:\n" + priorErr
	}
	return prompt
}
