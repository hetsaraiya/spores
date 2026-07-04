# Dead code report

Audit target: `main` at `9046cfab1ad38d8100a3bcb3ff15668ba7c3e569`.

## Method and scope

The audit inspected every Go source file and searched the repository for exact
symbol references. A symbol was not treated as dead merely because it lacked a
direct static call. Test entry points and test-only helpers, OpenAI tool
registrations and dispatch targets, Slack callbacks, interface implementations,
JSON-decoded fields, and other runtime wiring were treated as live.

The sandbox did not contain a Go toolchain, so `go test`, `go vet`, and a
whole-program `golang.org/x/tools` dead-code analysis could not be run. The
findings below are therefore based on full source inspection and repository-wide
reference searches.

## High-confidence dead code

None found. In particular, there were no demonstrably unreachable branches,
wholly unused files, or unused unexported production functions, types,
constants, or variables.

## Candidates requiring manual verification

These exported symbols have no references in this repository beyond their own
declarations or the paired type/method noted below. They are not classified as
dead because another module could import and call them.

| Path | Symbol | Evidence | False-positive risk |
| --- | --- | --- | --- |
| `githubclient/github.go` | `PRRequest`, `(*Client).CreatePR` | `PRRequest` is used only as the method parameter; `CreatePR` has no in-repository caller. The current delegated workflow asks the coding agent to create pull requests itself. | External consumers may construct `PRRequest` and call this exported method. |
| `githubclient/github.go` | `Issue`, `(*Client).GetIssue` | `Issue` is used only as this method's return type; `GetIssue` has no in-repository caller. The router calls `GetIssueDetail` instead. | External consumers may depend on this exported structured API. |
| `githubclient/github.go` | `(*Client).CloneURL` | No in-repository call sites. The current coding-agent workflow handles cloning directly. | External consumers may call this exported convenience method. |
| `githubclient/github.go` | `(*Client).DefaultBranch` | No in-repository call sites. The active read path obtains repository metadata through `GetRepo`, while delegated work resolves the branch itself. | External consumers may call this exported method. |
| `sandbox/sandbox.go` | `(*Sandbox).ListFiles` | No in-repository call sites; active paths use `RunCommand` and `RunCodex`. | External consumers may use this exported convenience method. |

Before removing any candidate, check downstream consumers or first deprecate it
through the repository's public API policy. No production code should be removed
based on this audit alone.

## Explicitly excluded examples

- `agent.issueURL` and `agent.slug` are exercised by tests and are excluded by
  the audit scope even though production code does not currently call them.
- Router tool handlers are reachable through model-produced tool names and the
  `dispatch` switch; they are runtime registration paths, not dead code.
- Slack event handlers and `userTransport.RoundTrip` are callbacks/interface
  wiring and were treated as live.
- Files and functions under tests, including opt-in live tests, were treated as
  test entry points rather than dead production code.
