package tools

import (
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"
)

const UpdateMemory = "update_memory"
const SearchMemory = "search_memory"

// MemoryDefinition declares the only tool available during the curation phase.
func MemoryDefinition() openai.ChatCompletionToolUnionParam {
	return openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
		Name:        UpdateMemory,
		Description: openai.String("Overwrite one long-term memory file with its full new content. Call once per changed file; empty content deletes the file. Do NOT call it if nothing durable changed."),
		Parameters: shared.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"file":    map[string]any{"type": "string", "description": "Markdown file to overwrite at the root or one category deep"},
				"content": map[string]any{"type": "string", "description": "The FULL replacement content for the file (empty deletes it)"},
			},
			"required": []string{"file", "content"},
		},
	})
}

// MemorySearchDefinition exposes read-only, bounded retrieval to the router.
func MemorySearchDefinition() openai.ChatCompletionToolUnionParam {
	return openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
		Name:        SearchMemory,
		Description: openai.String("Search all long-term memory files for personal profile, projects, infrastructure, relationships, security notes, preferences, or reusable knowledge. Returns only small ranked excerpts. Use focused keywords and call again with a narrower query when needed."),
		Parameters: shared.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Focused search terms, for example: Zaher DigitalYou, Mac Mini kernel, or UI preferences"},
				"limit": map[string]any{"type": "integer", "description": "Maximum excerpts to return, from 1 to 5"},
			},
			"required": []string{"query"},
		},
	})
}
