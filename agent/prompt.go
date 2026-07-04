package agent

import "fmt"

// taskPrompt drives the entire pipeline in a single Codex session: parse the
// request, open (or reuse) the issue, clone, implement, commit, push, and open
// the PR, then report back. The sandbox is already authenticated for git, the
// gh CLI, and (as a fallback) raw GitHub REST calls, and git identity is set,
// so the agent can do all of this itself without further help from the host.
func taskPrompt(message string) string {
	return fmt.Sprintf(`You are an autonomous senior software engineer working inside an ephemeral E2B sandbox.
Take the single user request below and carry it all the way from GitHub issue to an open pull request, then report back. Do not stop for confirmation.

The sandbox is already prepared for you:
- git is authenticated over https, so clone and push work with no extra credentials.
- git user.name and user.email are configured, so commits succeed.
- The GitHub CLI (gh) is authenticated for github.com — prefer it for issue and PR operations.
- If gh is not available, a GitHub token is in /home/user/.gh_token; use it as a Bearer token against https://api.github.com instead.

User request:
%s

Do all of the following, in order:
1. Determine the target repository (owner/repo) from the request. If it is genuinely impossible to tell, stop and report exactly what is missing.
2. Issue:
   - If the request references an existing GitHub issue URL, use that issue.
   - Otherwise create a new issue with a clear title (<= 80 chars), a suitable label (bug, enhancement, documentation, or question), and a markdown body using this structure:
     ## Description
     [what needs to be done]

     ## Acceptance Criteria
     - [ ] criteria one
     - [ ] criteria two

     ## Technical Notes
     [implementation hints if any]
3. Clone the repository into /home/user/repo and create a branch named fix/<issue-number>-<short-slug>.
4. Implement the smallest coherent change that satisfies the issue. Inspect the real files first and match the existing language, style, and toolchain. If an obvious build or test command exists, run it and make sure it passes.
5. Commit every change with a message like "fix: <title> (closes #<issue-number>)", then push the branch to origin. Do not leave the working tree dirty or the branch unpushed.
6. Open a pull request from your branch into the repository's default branch. The PR body must contain "Closes #<issue-number>".

When finished, output ONLY a concise, Slack-friendly final report (no markdown code fences) covering:
- What changed and how, mentioning the key files or areas.
- The issue URL.
- The pull request URL.

Do not claim anything you did not actually do. If a step failed, say which one and why, and still include whatever issue or PR links you managed to create.`, message)
}
