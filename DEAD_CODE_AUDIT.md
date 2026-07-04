# Dead code audit

Audited the Go packages under `agent/`, `githubclient/`, `memorystore/`,
`router/`, `sandbox/`, and `slackhandler/`, plus `main.go`. The review covered
imports, declarations, repository-wide references, tests, and whole-program
reachability from `main`.

## Findings

The following exported functions and methods have no callers in production or
tests and are reported as unreachable by Go's `deadcode` analyzer when test
executables are included:

| File | Symbol | Assessment |
| --- | --- | --- |
| `githubclient/github.go` | `(*Client).CreatePR` | Superseded by the sandbox/Codex workflow, which creates pull requests through `gh`; safe to remove for this application. |
| `githubclient/github.go` | `(*Client).GetIssue` | Superseded by `GetIssueDetail`, used by the router's `github_get_issue` tool; safe to remove. |
| `githubclient/github.go` | `(*Client).CloneURL` | Cloning is delegated to the sandbox/Codex workflow; safe to remove. |
| `githubclient/github.go` | `(*Client).DefaultBranch` | Repository metadata is obtained through `GetRepo`; safe to remove. |
| `memorystore/store.go` | `(*Store).Dir` | Unused accessor; safe to remove. |
| `sandbox/sandbox.go` | `(*Sandbox).ListFiles` | Unused wrapper around `RunCommand`; safe to remove. |

Removing `CreatePR` also makes `PRRequest` dead, and removing `GetIssue` makes
`Issue` dead. Those two types can be removed with their methods. `IssueRequest`
remains live through `router.createIssue` and must stay.

No unused imports, unreachable unexported functions, or additional clearly
dead branches were found. Conditional entry paths in `main.go` (`SANDBOX_PROBE`
and `AGENT_PROMPT`) are environment-selected modes, not dead code. Live tests
guarded by `SPORE_LIVE=1` are intentionally opt-in and are also not dead code.

Because the identified symbols are exported, deleting them could break an
unknown external consumer of the module. Within this repository, however,
there are no references or interface requirements, so removal is safe for the
application itself. This audit intentionally documents the candidates without
changing runtime code.

## Verification

- `go test ./...` passes with Go 1.25.0.
- `go vet ./...` passes.
- `deadcode -test ./...` reports only the six functions and methods above.
- Repository-wide reference searches confirm each symbol appears only at its
  declaration (apart from similarly named, live replacement methods).
