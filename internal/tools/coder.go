package tools

import (
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"
)

const DelegateToCoder = "delegate_to_coder"

// DelegateDefinition declares the write-capable coding-agent handoff.
func DelegateDefinition() openai.ChatCompletionToolUnionParam {
	return openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
		Name:        DelegateToCoder,
		Description: openai.String("Hand off a substantial task to a full agent in an isolated sandbox. The agent can work with a repository when one is specified and GitHub access is configured, or research without a repository."),
		Parameters: shared.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{"type": "string", "description": "Complete agent brief. Include the target owner/repo and explicit PR/issue instructions when repository work is required; otherwise include the research question, desired sources, and output format."},
			},
			"required": []string{"task"},
		},
	})
}
