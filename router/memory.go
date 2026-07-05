package router

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"spore/agent"
	"spore/langsmith"
)

// defaultSmallModel runs memory updates when no memory exists yet; once
// memory is populated the router's good model curates it instead.
const defaultSmallModel = "gpt-5.4-mini"

// memoryUpdateTimeout bounds the post-run memory update call.
const memoryUpdateTimeout = 3 * time.Minute

const memoryUpdatePrompt = `You maintain the long-term memory of a GitHub coding bot. You get the current memory files and the conversation of a session that just ended. Keep memory correct and compact with the FEWEST edits; no update is the normal outcome.

Files — put each fact in the RIGHT scope, and move it if it is in the wrong one:
- USER.md (~1500 chars): who the user is and how they like to work — identity, role, standing personal preferences ("prefer squash merges").
- STACK.md (~2000): cross-project stack/tooling choices — clouds, languages, libraries, conventions ("deploy on Fly.io", "pnpm not npm").
- REPOS/<owner>-<repo>.md (~1500 each): facts true for ONE repo — build/test commands, conventions, quirks. Slash becomes dash: acme/web -> REPOS/acme-web.md.
- SKILLS/<topic>.md (~1500 each): one durable skill, workflow, or lesson per file, not tied to a repo.
- COMPANY.md / PRODUCT.md: only if the user genuinely has one — never invent these.

UPDATE a file only when the session shows:
- a NEW durable fact or preference not yet stored;
- a CORRECTION or contradiction — the newer statement wins: replace the stale entry, never keep both;
- a fact filed in the WRONG scope — move it to the right file.

Do NOT update for:
- one-off task details, temporary paths, or anything only relevant to this session;
- facts easily re-discovered by reading the repo;
- code, logs, or data dumps;
- anything memory already captures, or a mere rephrasing/reordering of it.

When a file nears its budget, consolidate: merge related entries into one dense entry and drop the least useful. Write compact, information-dense entries.

Return the FULL replacement content for each changed file (it overwrites the file); empty content deletes it.
Respond ONLY with valid JSON, no markdown fences:
{"updates": [{"file": "USER.md", "content": "full new file content"}]}`

type memoryUpdate struct {
	File    string `json:"file"`
	Content string `json:"content"`
}

// fireMemoryUpdate launches the memory-update agent in the background after
// a session returns, per the architecture: the main response is never
// delayed by memory maintenance.
func (r *Router) fireMemoryUpdate(ctx context.Context, history []chatMessage) {
	if r.store == nil {
		return
	}
	// Memory distills the CONVERSATION, not raw tool output. Keep only the
	// user/assistant exchange — drop system/tool turns and tool-call plumbing —
	// and replay it to the memory agent as real role-separated messages, not a
	// flattened "USER:/ASSISTANT:" blob (which also renders as one giant user
	// bubble in LangSmith).
	var convo []chatMessage
	for _, m := range history {
		text := strings.TrimSpace(m.Content)
		if text == "" || (m.Role != "user" && m.Role != "assistant") {
			continue
		}
		convo = append(convo, chatMessage{Role: m.Role, Content: clipMemory(text)})
	}
	if len(convo) == 0 {
		return
	}
	// Keep the turn's trace (so this nests under it as one trace) but drop the
	// request's cancellation, since this runs after the response is sent.
	parent := langsmith.Detach(ctx)
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		c, cancel := context.WithTimeout(parent, memoryUpdateTimeout)
		defer cancel()
		if err := r.updateMemory(c, convo); err != nil {
			log.Printf("memory update failed: %v", err)
		}
	}()
}

// Wait blocks until all in-flight memory updates finish. Call before exiting
// in one-shot (CLI) mode.
func (r *Router) Wait() { r.wg.Wait() }

func (r *Router) updateMemory(ctx context.Context, convo []chatMessage) (err error) {
	ctx, run := r.tracer.Start(ctx, "memory update", "chain", map[string]any{"turns": len(convo)})
	defer func() { run.End(nil, err) }()

	// The maintenance agent needs the FULL picture (not the budget-truncated
	// prompt view) so it can consolidate and de-conflict across all files.
	current := r.store.FullBlock()
	if current == "" {
		current = "(no memory stored yet)"
	}
	// System holds the instructions plus the current memory state; the session
	// is replayed as its original user/assistant turns; a final user message
	// marks the end and asks for the verdict.
	messages := make([]chatMessage, 0, len(convo)+2)
	messages = append(messages, chatMessage{Role: "system", Content: memoryUpdatePrompt + "\n\n# Current memory files\n" + current})
	messages = append(messages, convo...)
	messages = append(messages, chatMessage{Role: "user", Content: `That was the full session. Respond now with the JSON updates object — {"updates": []} if nothing durable changed.`})
	reply, err := r.llm.completeWithModel(ctx, r.memoryModel(), messages, nil)
	if err != nil {
		return err
	}
	updates, err := parseMemoryUpdates(reply.Content)
	if err != nil {
		return err
	}
	for _, u := range updates {
		// Guard against the model re-emitting content it already stored: only
		// write when the substance actually changes, so unchanged memory is
		// never needlessly rewritten.
		if !r.store.Changed(u.File, u.Content) {
			log.Printf("memory unchanged, skipped: %s", u.File)
			continue
		}
		if err := r.store.Write(u.File, u.Content); err != nil {
			log.Printf("memory update skipped for %q: %v", u.File, err)
			continue
		}
		log.Printf("memory updated: %s", u.File)
	}
	return nil
}

// memoryModel picks the model for memory updates: a small model when no
// memory exists yet, otherwise the router's good model (per the diagram:
// "Small Model! If No memory Else good model").
func (r *Router) memoryModel() string {
	if r.store != nil && r.store.IsEmpty() {
		return r.smallModel
	}
	return r.llm.model
}

func parseMemoryUpdates(text string) ([]memoryUpdate, error) {
	var parsed struct {
		Updates []memoryUpdate `json:"updates"`
	}
	if err := json.Unmarshal([]byte(agent.ExtractJSON(text)), &parsed); err != nil {
		return nil, fmt.Errorf("memory agent returned malformed JSON: %w", err)
	}
	return parsed.Updates, nil
}
