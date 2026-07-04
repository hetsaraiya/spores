package router

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"spore/agent"
)

// defaultSmallModel runs memory updates when no memory exists yet; once
// memory is populated the router's good model curates it instead.
const defaultSmallModel = "gpt-5.4-mini"

// memoryUpdateTimeout bounds the post-run memory update call.
const memoryUpdateTimeout = 3 * time.Minute

const memoryUpdatePrompt = `You are a memory maintenance agent for a GitHub workflow bot.
You receive the bot's current long-term memory and the transcript of a session that just finished.
Keep memory correct with the FEWEST possible edits. Making no update is the normal, expected outcome.

Memory files and their purpose:
- STACK.md: the stack the user uses and prefers (clouds, providers, languages, libraries, conventions).
- SKILLS/<topic>.md: one file per durable skill, preference, or fact worth remembering.
- COMPANY.md / PRODUCT.md: OPTIONAL, only for users who actually have a company or product. A solo developer may never need these — do NOT create or invent them, and never fill them with filler.

Rules:
- Only store durable facts and preferences, never transient session details.
- Update a file ONLY when the session reveals a genuinely NEW durable fact, or clearly corrects/contradicts something already stored. If current memory already captures it, make NO update for that file.
- Do NOT rewrite a file just to rephrase, reorder, or expand wording — the substance must actually change.
- When in doubt, make no update. Returning an empty updates list is correct and common.
- When you do change a file, return its FULL replacement content (it overwrites the file).
- Return empty content for a file only to delete something that is now wrong.

Respond ONLY with valid JSON, no markdown fences:
{"updates": [{"file": "STACK.md", "content": "full new file content"}]}`

type memoryUpdate struct {
	File    string `json:"file"`
	Content string `json:"content"`
}

// fireMemoryUpdate launches the memory-update agent in the background after
// a session returns, per the architecture: the main response is never
// delayed by memory maintenance.
func (r *Router) fireMemoryUpdate(history []oaMessage) {
	if r.store == nil {
		return
	}
	transcript := routerContext(history)
	if strings.TrimSpace(transcript) == "" {
		return
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), memoryUpdateTimeout)
		defer cancel()
		if err := r.updateMemory(ctx, transcript); err != nil {
			log.Printf("memory update failed: %v", err)
		}
	}()
}

// Wait blocks until all in-flight memory updates finish. Call before exiting
// in one-shot (CLI) mode.
func (r *Router) Wait() { r.wg.Wait() }

func (r *Router) updateMemory(ctx context.Context, transcript string) error {
	current := r.store.PromptBlock()
	if current == "" {
		current = "(no memory stored yet)"
	}
	messages := []oaMessage{
		{Role: "system", Content: memoryUpdatePrompt},
		{Role: "user", Content: "Current memory files:\n" + current + "\n\nSession transcript:\n" + transcript},
	}
	reply, err := r.oa.completeWithModel(ctx, r.memoryModel(), messages, nil)
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
	return r.oa.model
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
