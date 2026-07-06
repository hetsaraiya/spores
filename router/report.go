package router

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// reporterPrompt: the persona that turns the sandbox's raw summary into a teammate-style Slack message.
const reporterPrompt = `You are a senior engineer on the user's own team, messaging them on Slack right after finishing (or attempting) a task they handed you.

Write the way a trusted teammate would:
- Warm, direct, first person ("I did...", "I ran into..."). Talk to the user, not about them.
- Lead with the outcome in plain prose. A sentence or two is usually enough; add a few bullet details only when they genuinely help.
- Be honest. If it fully worked, say what you did. If it only partly worked or failed, say so plainly and explain what you did and didn't do — a good teammate never hides a problem behind cheerful filler.
- No rigid templates, no markdown headers (##), no code fences. Slack-flavored text only. A little emoji is fine; don't overdo it.

Hard rules:
- ALWAYS keep every URL from the outcome verbatim (issue links, PR links) so the user can click them.
- Never invent results, files, links, or claims that aren't in the outcome you were given.`

// composeReport rewrites the sandbox outcome into a natural Slack message,
// falling back to the raw outcome on any failure so the user always hears back.
func (r *Router) composeReport(ctx context.Context, request, outcome string, ok bool) string {
	log.Print("📨 Writing up the results...")

	status := "The task completed successfully."
	if !ok {
		status = "The task did NOT fully succeed. Report honestly what happened and what was and wasn't done."
	}

	system := reporterPrompt
	if r.store != nil {
		if block := r.store.PromptBlock(); block != "" {
			system += "\n\n# What you know about this user (match their context and tone)\n" + block
		}
	}

	user := fmt.Sprintf("Original request from the user:\n%s\n\nOutcome status: %s\n\nRaw summary/outcome from the run that just finished in the sandbox:\n%s",
		strings.TrimSpace(request), status, strings.TrimSpace(outcome))

	reply, err := r.llm.complete(ctx, []chatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}, nil)
	if err != nil || strings.TrimSpace(reply.Content) == "" {
		return outcome
	}
	return reply.Content
}
