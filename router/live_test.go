package router

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/joho/godotenv"
)

// TestLiveRouting calls the real OpenAI model to confirm an issue-only request
// routes to create_github_issue (fast, no sandbox) rather than delegate_to_coder
// (which spins a sandbox and is what used to hang without ever replying).
// Gated behind SPORE_LIVE=1; no GitHub side effects — it only inspects the
// model's chosen tool call.
//
//	SPORE_LIVE=1 go test ./router/ -run TestLiveRouting -v
func TestLiveRouting(t *testing.T) {
	if os.Getenv("SPORE_LIVE") != "1" {
		t.Skip("set SPORE_LIVE=1 to run the live routing check")
	}
	_ = godotenv.Load("../.env")

	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Fatal("OPENAI_API_KEY not set")
	}
	model := os.Getenv("ROUTER_MODEL")
	if model == "" {
		model = os.Getenv("OPENAI_MODEL")
	}
	oa := newOAClient(key, os.Getenv("OPENAI_BASE_URL"), model)
	r := &Router{oa: oa}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	msg := "make an issue in hetsaraiya/Portfolio saying we need to upgrade all packages to latest. " +
		"do not do it yourself, fire an e2b sandbox for this, list all packages that need upgrading."
	messages := []oaMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: msg},
	}

	// Run the router loop, but intercept the write tools (create_github_issue /
	// delegate_to_coder) so nothing real happens. Read tools get a canned reply
	// so the model can proceed to its final decision.
	for turn := 0; turn < 8; turn++ {
		reply, err := oa.complete(ctx, messages, r.tools())
		if err != nil {
			t.Fatalf("routing completion failed: %v", err)
		}
		messages = append(messages, reply)
		if len(reply.ToolCalls) == 0 {
			t.Fatalf("model never reached a write tool; final content: %q", reply.Content)
		}
		for _, call := range reply.ToolCalls {
			name := call.Function.Name
			t.Logf("turn %d: model called %s(%s)", turn, name, call.Function.Arguments)
			switch name {
			case "create_github_issue":
				t.Logf("PASS: issue-only request routed to create_github_issue (fast path)")
				return
			case "delegate_to_coder":
				t.Fatalf("issue-only request routed to the slow sandbox path (delegate_to_coder)")
			default:
				messages = append(messages, oaMessage{
					Role:       "tool",
					ToolCallID: call.ID,
					Content:    cannedReadResult(name),
				})
			}
		}
	}
	t.Fatal("model did not settle on a write tool within the turn budget")
}

// cannedReadResult returns plausible read-tool output so the live routing test
// can advance turns without touching GitHub.
func cannedReadResult(tool string) string {
	switch tool {
	case "github_get_repo":
		return `{"default_branch":"main","language":"TypeScript","description":"Next.js portfolio"}`
	case "github_get_file":
		return `{"name":"package.json","dependencies":{"next":"^16.2.6","react":"^19.2.6","motion":"^12.38.0","@sanity/client":"^7.22.0"}}`
	case "github_tree", "github_list_dir":
		return "package.json\npackage-lock.json\nsrc/\nnext.config.js"
	default:
		return "(no additional data)"
	}
}
