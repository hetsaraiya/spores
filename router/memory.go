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
const defaultSmallModel = "gpt-4o-mini"

// memoryUpdateTimeout bounds the post-run memory update call.
const memoryUpdateTimeout = 3 * time.Minute

const memoryUpdatePrompt = `You are a memory maintenance agent for a GitHub workflow bot.
You receive the bot's current long-term memory files and the transcript of a session that just finished.
Decide what (if anything) in memory should be created or updated so future sessions know it.

Memory files and their purpose:
- COMPANY.md: what the user's company is and does
- PRODUCT.md: what the product is
- STACK.md: what stack the user uses and prefers (clouds, providers, libraries)
- SKILLS/<topic>.md: one file per durable skill, preference, or fact worth remembering

Rules:
- Only store durable facts and preferences, never transient session details.
- Return the FULL replacement content for any file you change (it overwrites the file).
- Return empty content for a file to delete it.
- If nothing is worth remembering, return no updates.

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
