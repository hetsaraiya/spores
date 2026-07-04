# Dead-code audit

Scope: Go source on `main` at commit `9046cfa`, covering `main.go` and the
`agent`, `githubclient`, `memorystore`, `router`, `sandbox`, and `slackhandler`
packages.

The audit uses repository-wide declaration/reference searches plus Git history.
An item is not classified as dead merely because the main execution path does
not call it. Registered tools, callbacks, interface methods, serialization
types, environment-selected paths, and test entry points are treated as live.

## Findings

| File / symbol | Evidence | Confidence | Safe to remove? |
| --- | --- | --- | --- |
| `agent/support.go`: `issueURL`; `agent/support_test.go`: `TestIssueURL` | `issueURL` has no production caller; its only caller is its own unit test. Before refactor `9046cfa`, `existingIssue` used it. That refactor deleted `existingIssue` and moved issue discovery into the single Codex session, leaving the helper and its now-obsolete test behind. | High | Yes, remove the helper and its test together. |
| `agent/support.go`: `slug`; `agent/support_test.go`: `TestSlug` | `slug` has no production caller; its only caller is its own unit test. Before `9046cfa`, `cloneRepo` used it to form branch names. The refactor deleted `cloneRepo` and delegated branch creation to Codex. The phrase `short-slug` in a prompt is descriptive text, not a reference to this function. | High | Yes, remove the helper and its test together. |
| `githubclient/github.go`: `PRRequest`, `(*Client).CreatePR` | No caller exists in the repository. Before `9046cfa`, `agent.openPR` constructed `PRRequest` and called `CreatePR`; that refactor deleted `openPR` and deliberately moved PR creation to Codex using `gh` or REST. | High | Yes within this application. Because these are exported, first check for out-of-repository consumers if the module is being used as a library. |
| `githubclient/github.go`: `Issue`, `(*Client).GetIssue` | No caller exists in the repository; the type is only used as this method's return value. Before `9046cfa`, `agent.existingIssue` called it, and that function was deleted. The active registered read tool calls the separate `GetIssueDetail` method. | High | Yes within this application, subject to the exported-API caveat above. |
| `githubclient/github.go`: `(*Client).CloneURL` | No reference exists beyond its declaration. Its former caller, `agent.cloneRepo`, was deleted by `9046cfa`; repository cloning is now performed by the delegated Codex session. | High | Yes within this application, subject to the exported-API caveat above. |
| `githubclient/github.go`: `(*Client).DefaultBranch` | No reference exists beyond its declaration. Its former caller, `agent.openPR`, was deleted by `9046cfa`; the current task prompt directs Codex to target the repository default branch. | High | Yes within this application, subject to the exported-API caveat above. |
| `sandbox/sandbox.go`: `(*Sandbox).ListFiles` | No reference exists beyond its declaration. Its former caller, `agent.implementFix`, was deleted by `9046cfa`; the current agent hands repository inspection to Codex and uses `RunCodex`/`RunCommand`. | High | Yes within this application, subject to the exported-API caveat above. |
| `memorystore/store.go`: `(*Store).Dir` | No reference exists beyond its declaration, including tests. It has had no in-repository caller since its introduction in `6325dac`; it does not satisfy an interface and is not registered or accessed reflectively. | Medium-high | Yes within this application, subject to the exported-API caveat above. |

No demonstrably unreachable branches, permanently disabled implementations,
unused imports, or wholly obsolete files were found.

## Explicit exclusions

- The GitHub read/search methods in `githubclient/read.go` and the corresponding
  router functions are live: `router.tools()` exposes them by tool name and
  `dispatch` invokes them through a string-based registry/switch.
- `router.createIssue` and `router.delegate` are likewise reached through tool
  dispatch, even though ordinary call-site searches are easy to misread.
- Slack handlers are event callbacks invoked from Socket Mode events.
- `userTransport.RoundTrip` is live through the `http.RoundTripper` interface.
- OpenAI request/response fields and types are used through JSON encoding and
  decoding, so field-level textual references are not required.
- Go test functions, including opt-in live tests, are test-runner entry points.
- `SANDBOX_PROBE`, `AGENT_PROMPT`, and `SPORE_LIVE` branches are selectable via
  environment variables and are not unreachable.
- `agent.ExtractJSON` remains live through memory-update parsing; store methods
  such as `IsEmpty` are also called.

## Verification limitation

The audit environment did not contain a Go executable, so `go test`, `go vet`,
and compiler/SSA dead-code analyzers could not be run. The findings above are
therefore based on exhaustive source reference searches and confirming Git
history, not toolchain output.
