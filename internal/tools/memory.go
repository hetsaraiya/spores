package tools

import (
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"
)

const UpdateMemory = "update_memory"

// MemoryDefinition declares the only tool available during the curation phase.
func MemoryDefinition() openai.ChatCompletionToolUnionParam {
	return openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
		Name:        UpdateMemory,
		Description: openai.String("Overwrite one long-term memory file with its full new content. Call once per changed file; empty content deletes the file. Do NOT call it if nothing durable changed."),
		Parameters: shared.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"file":    map[string]any{"type": "string", "description": "Memory file to overwrite: USER.md, STACK.md, or SKILLS/<topic>.md"},
				"content": map[string]any{"type": "string", "description": "The FULL replacement content for the file (empty deletes it)"},
			},
			"required": []string{"file", "content"},
		},
	})
}
