package router

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"spore/langsmith"
)

// Model for memory updates while memory is empty; once populated, the good model curates.
const defaultSmallModel = "gpt-5.4-mini"

const memoryUpdateTimeout = 3 * time.Minute

// Bounds the memory phase's own tool-calling loop (one turn to decide, a few to write files).
const maxMemoryTurns = 5

// memoryPrompt is injected as the final user turn once the reply is out. It runs
// against the SAME session (system + long-term memory + full transcript), so the
// curator already has every piece of context the router saw — no re-summarizing.
const memoryPrompt = `You've just replied to the user; that conversation is over. Now maintain your own long-term memory. Keep it correct and compact with the FEWEST edits — no update is the normal outcome.

Files — put each fact in the RIGHT scope, and move it if it is in the wrong one:
- USER.md (~1500 chars): who the user is and how they like to work — identity, role, standing personal preferences ("prefer squash merges").
- STACK.md (~2000): cross-project stack/tooling choices — clouds, languages, libraries, conventions ("deploy on Fly.io", "pnpm not npm").
- REPOS/<owner>-<repo>.md (~1500 each): facts true for ONE repo — build/test commands, conventions, quirks. Slash becomes dash: acme/web -> REPOS/acme-web.md.
- SKILLS/<topic>.md (~1500 each): one durable skill, workflow, or lesson per file, not tied to a repo.

Call update_memory only when this session shows:
- a NEW durable fact or preference not yet stored;
- a CORRECTION or contradiction — the newer statement wins: replace the stale entry, never keep both;
- a fact filed in the WRONG scope — move it to the right file.

Do NOT update for:
- one-off task details, temporary paths, or anything only relevant to this session;
- facts easily re-discovered by reading the repo;
- code, logs, or data dumps;
- anything memory already captures, or a mere rephrasing/reordering of it.

When a file nears its budget, consolidate: merge related entries into one dense entry and drop the least useful.

update_memory takes the FULL replacement content for one file (it overwrites the file); empty content deletes it. Call it once per changed file. If nothing durable changed, DO NOT call it — just reply with one short line saying memory is unchanged, which ends the session.`

// fireMemoryUpdate curates long-term memory in the background so the reply is never
// delayed. It reuses the full router session (session already carries the system
// prompt, injected memory, replayed history, and every turn incl. tool calls), and
// lets the same model decide — via the update_memory tool — whether anything durable
// should be written, or end the session with no tool call.
func (r *Router) fireMemoryUpdate(ctx context.Context, session []chatMessage) {
	if r.store == nil {
		return
	}
	// Snapshot the session so the background goroutine isn't racing later appends.
	snapshot := append([]chatMessage(nil), session...)
	// Keep the turn's trace but drop its cancellation — this runs after the reply is sent.
	parent := langsmith.Detach(ctx)
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		c, cancel := context.WithTimeout(parent, memoryUpdateTimeout)
		defer cancel()
		if err := r.curateMemory(c, snapshot); err != nil {
			log.Printf("memory update failed: %v", err)
		}
	}()
}

// Wait blocks until in-flight memory updates finish; call before exiting in CLI mode.
func (r *Router) Wait() { r.wg.Wait() }

// curateMemory continues the session: it appends the current (uncapped) memory and
// the maintenance prompt, then runs a small tool-calling loop. Each update_memory
// call is applied immediately; the loop ends when the model stops calling tools.
func (r *Router) curateMemory(ctx context.Context, session []chatMessage) (err error) {
	ctx, run := r.tracer.Start(ctx, "memory update", "chain", map[string]any{"turns": len(session)})
	defer func() { run.End(nil, err) }()

	// Full picture (not the budget-truncated view injected into the system prompt) so it can consolidate across files.
	current := r.store.FullBlock()
	if current == "" {
		current = "(no memory stored yet)"
	}
	messages := append([]chatMessage(nil), session...)
	messages = append(messages, chatMessage{
		Role:    "user",
		Content: memoryPrompt + "\n\n# Current long-term memory (full, uncapped)\n" + current,
	})
	tools := memoryTools()

	for turn := 0; turn < maxMemoryTurns; turn++ {
		reply, err := r.llm.completeWithModel(ctx, r.memoryModel(), messages, tools)
		if err != nil {
			return err
		}
		messages = append(messages, reply)
		if len(reply.ToolCalls) == 0 {
			return nil // model decided nothing more to store — session ends
		}
		for _, call := range reply.ToolCalls {
			messages = append(messages, chatMessage{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    r.applyMemoryCall(call.Function.Name, call.Function.Arguments),
			})
		}
	}
	return nil
}

// memoryTools is the single tool available during the memory phase.
func memoryTools() []toolDef {
	return []toolDef{
		spec("update_memory", "Overwrite one long-term memory file with its full new content. Call once per changed file; empty content deletes the file. Do NOT call it if nothing durable changed.",
			obj(map[string]any{
				"file":    strProp("Memory file to overwrite: USER.md, STACK.md, SKILLS/<topic>.md, or REPOS/<owner>-<repo>.md"),
				"content": strProp("The FULL replacement content for the file (empty deletes it)"),
			}, "file", "content")),
	}
}

// applyMemoryCall writes one update_memory call, guarding no-op rewrites, and returns
// a short result the model reads before deciding whether to write more or stop.
func (r *Router) applyMemoryCall(name, rawArgs string) string {
	if name != "update_memory" {
		return fmt.Sprintf("unknown tool %q", name)
	}
	var args toolArgs
	if rawArgs != "" {
		if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
			return fmt.Sprintf("invalid arguments: %v", err)
		}
	}
	file := args.str("file")
	content := args.str("content")
	if !r.store.Changed(file, content) {
		log.Printf("memory unchanged, skipped: %s", file)
		return "No change: " + file + " already reflects that. Nothing written."
	}
	if err := r.store.Write(file, content); err != nil {
		log.Printf("memory update skipped for %q: %v", file, err)
		return "error: " + err.Error()
	}
	log.Printf("memory updated: %s", file)
	return "Saved " + file + "."
}

// memoryModel uses the small model while memory is empty, else the router's good model.
func (r *Router) memoryModel() string {
	if r.store != nil && r.store.IsEmpty() {
		return r.smallModel
	}
	return r.llm.model
}
