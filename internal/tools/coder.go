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
		Description: openai.String("Hand off coding work or substantial pure research to a full agent in an isolated sandbox. Coding tasks require a target owner/repo and may make changes. Research tasks need no repository, can search the web and analyze documentation, and must only return findings."),
		Parameters: shared.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{"type": "string", "description": "Complete agent brief. For coding, include the target owner/repo and explicit PR/issue instructions. For research, include the question, desired sources, and output format."},
				"mode": map[string]any{"type": "string", "enum": []string{"coding", "research"}, "description": "Use coding for repository-backed changes and research for read-only investigation without a repository."},
			},
			"required": []string{"task", "mode"},
		},
	})
}
