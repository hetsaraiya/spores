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

// Model for memory updates while memory is empty; once populated, the good model curates.
const defaultSmallModel = "gpt-5.4-mini"

const memoryUpdateTimeout = 3 * time.Minute

const memoryUpdatePrompt = `You maintain the long-term memory of a GitHub coding bot. You get the current memory files and the conversation of a session that just ended. Keep memory correct and compact with the FEWEST edits; no update is the normal outcome.

Files — put each fact in the RIGHT scope, and move it if it is in the wrong one:
- USER.md (~1500 chars): who the user is and how they like to work — identity, role, standing personal preferences ("prefer squash merges").
- STACK.md (~2000): cross-project stack/tooling choices — clouds, languages, libraries, conventions ("deploy on Fly.io", "pnpm not npm").
- REPOS/<owner>-<repo>.md (~1500 each): facts true for ONE repo — build/test commands, conventions, quirks. Slash becomes dash: acme/web -> REPOS/acme-web.md.
- SKILLS/<topic>.md (~1500 each): one durable skill, workflow, or lesson per file, not tied to a repo.

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

// fireMemoryUpdate runs the memory agent in the background so the reply is never delayed.
func (r *Router) fireMemoryUpdate(ctx context.Context, history []chatMessage) {
	if r.store == nil {
		return
	}
	// Distill the conversation, not tool output: keep user/assistant turns and
	// replay them as real role-separated messages (not a flattened blob).
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
	// Keep the turn's trace but drop its cancellation — this runs after the reply is sent.
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

// Wait blocks until in-flight memory updates finish; call before exiting in CLI mode.
func (r *Router) Wait() { r.wg.Wait() }

func (r *Router) updateMemory(ctx context.Context, convo []chatMessage) (err error) {
	ctx, run := r.tracer.Start(ctx, "memory update", "chain", map[string]any{"turns": len(convo)})
	defer func() { run.End(nil, err) }()

	// Full picture (not the budget-truncated view) so it can consolidate across files.
	current := r.store.FullBlock()
	if current == "" {
		current = "(no memory stored yet)"
	}
	// System = instructions + current memory; then the replayed turns; then a final prompt for the verdict.
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
		// Skip no-op rewrites when the model re-emits unchanged content.
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

// memoryModel uses the small model while memory is empty, else the router's good model.
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
