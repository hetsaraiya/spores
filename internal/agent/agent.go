package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hetsaraiya/spores/internal/coder"
	"github.com/hetsaraiya/spores/internal/github"
	"github.com/hetsaraiya/spores/internal/memory"
	"github.com/hetsaraiya/spores/internal/tools"
	"github.com/openai/openai-go/v3"
)

const systemPrompt = "You are a GitHub workflow assistant. User messages may be prefixed with a Slack display name; treat that prefix as speaker metadata. Use search_memory when a request may depend on detailed personal profile, projects, infrastructure, relationships, security notes, preferences, or prior reusable knowledge that is not present in the always-on memory. Search with focused keywords and refine the query when needed; do not assume a missing search result means the fact is false. Use github_* tools for read-only repository questions. Use delegate_to_coder only when the user explicitly asks to write or edit code, create an issue, or open a pull request. The delegation task must be a complete brief: target owner/repo, precise work, explicit issue/PR instructions, and stopping point. Do not delegate read-only questions. After delegate_to_coder returns, evaluate its report yourself. You may use only github_* tools to verify it, then give a clear final assessment (like you are a human/ a human won't write too big messages and document long summaris of single task). Do not make, request, or delegate any further changes if the result is incorrect; explain what is incorrect instead."

type Request struct {
	Speaker string
	// SpeakerID is the stable Slack user ID, used to gate owner-only memory.
	SpeakerID string
	Message   string
	History   []Turn
}

type Turn struct {
	Speaker     string
	Message     string
	IsAssistant bool
}

type Agent struct {
	client      openai.Client
	github      *github.Client
	codingAgent *coder.Delegate
	memory      *memory.Store
	curator     *memory.Curator
	model       string
	owner       string
	tools       []openai.ChatCompletionToolUnionParam
}

func New(client openai.Client, githubClient *github.Client, codingAgent *coder.Delegate, store *memory.Store, curator *memory.Curator, model, owner string) *Agent {
	return &Agent{
		client:      client,
		github:      githubClient,
		codingAgent: codingAgent,
		memory:      store,
		curator:     curator,
		model:       model,
		owner:       owner,
		tools:       append(tools.GitHubDefinitions(), tools.DelegateDefinition(), tools.MemorySearchDefinition()),
	}
}

func (a *Agent) Run(ctx context.Context, request Request) (string, error) {
	messages := []openai.ChatCompletionMessageParamUnion{openai.SystemMessage(a.systemMessage())}
	for _, turn := range request.History {
		if turn.IsAssistant {
			messages = append(messages, openai.AssistantMessage(turn.Message))
			continue
		}
		messages = append(messages, openai.UserMessage(speakerMessage(turn.Speaker, turn.Message)))
	}
	messages = append(messages, openai.UserMessage(speakerMessage(request.Speaker, request.Message)))
	delegated := false
	for {
		completion, err := a.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{Messages: messages, Model: a.model, Tools: a.tools})
		if err != nil {
			return "", err
		}
		if len(completion.Choices) == 0 {
			return "", fmt.Errorf("model returned no choices")
		}
		choice := completion.Choices[0]
		if len(choice.Message.ToolCalls) == 0 {
			// Curation runs off the finished session, after the reply is out.
			a.curator.Enqueue(memory.Job{SpeakerID: request.SpeakerID, Messages: append(messages, choice.Message.ToParam())})
			return choice.Message.Content, nil
		}

		messages = append(messages, choice.Message.ToParam())
		for _, call := range choice.Message.ToolCalls {
			result := ""
			if call.Function.Name == tools.DelegateToCoder && delegated {
				result = "A coding task has already run for this request. Evaluate its report and use only read-only github_* tools if verification is needed; do not delegate another change."
			} else {
				result = a.executeTool(ctx, request.SpeakerID, call.Function.Name, call.Function.Arguments)
				if call.Function.Name == tools.DelegateToCoder {
					delegated = true
				}
			}
			messages = append(messages, openai.ToolMessage(result, call.ID))
		}
	}
}

// systemMessage is the base prompt plus whatever long-term memory fits the
// prompt budget. Memory is delimited and marked as background so it never
// outranks what the user just said.
func (a *Agent) systemMessage() string {
	section := a.memory.PromptSection()
	if section == "" {
		return systemPrompt
	}
	return systemPrompt + "\n\n" + section
}

func speakerMessage(speaker, message string) string {
	if strings.TrimSpace(speaker) == "" {
		return message
	}
	return speaker + ": " + message
}

func (a *Agent) executeTool(ctx context.Context, speakerID, name, rawArgs string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return "invalid tool arguments: " + err.Error()
	}
	if name == tools.DelegateToCoder {
		task, _ := args["task"].(string)
		result, err := a.codingAgent.Run(ctx, task)
		if err != nil {
			return "coding-agent error: " + err.Error()
		}
		return result
	}
	if name == tools.SearchMemory {
		if a.owner == "" || speakerID != a.owner {
			return "memory search is available only to the configured owner"
		}
		query, _ := args["query"].(string)
		limit := integerArg(args["limit"])
		results, err := a.memory.Search(query, limit)
		if err != nil {
			return "memory search error: " + err.Error()
		}
		return memory.FormatSearchResults(results)
	}
	result, known, err := tools.RunGitHub(ctx, a.github, name, args)
	if !known {
		return "unknown tool: " + name
	}
	if err != nil {
		return "GitHub tool error: " + err.Error()
	}
	return result
}

func integerArg(value any) int {
	switch value := value.(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		return 0
	}
}
