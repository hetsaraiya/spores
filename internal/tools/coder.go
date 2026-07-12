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
		Description: openai.String("Hand off a code change, bug fix, GitHub issue, or pull request to a full coding agent in an isolated sandbox. Use only when the user explicitly asks to make a change, create an issue, or open a pull request. The task is the agent's complete brief: include the target owner/repo, exact work, whether to open a pull request, whether to create an issue, and the stopping point."),
		Parameters: shared.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{"type": "string", "description": "Complete coding-agent brief. State explicit PR/issue instructions; say not to create them when they are not requested."},
			},
			"required": []string{"task"},
		},
	})
}
