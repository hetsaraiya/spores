package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/hetsaraiya/spores/internal/coder"
	"github.com/hetsaraiya/spores/internal/github"
	"github.com/hetsaraiya/spores/internal/jina"
	"github.com/hetsaraiya/spores/internal/memory"
	"github.com/hetsaraiya/spores/internal/tools"
	"github.com/openai/openai-go/v3"
)

const (
	// Without a ceiling a model that keeps requesting reads spins forever.
	maxToolTurns = 12

	// completionTimeout bounds one model call; a gateway may stall rather than
	// refuse. requestTimeout must exceed the coding sandbox's own budget.
	completionTimeout = 2 * time.Minute
	requestTimeout    = 20 * time.Minute
)

const (
	repeatDelegationNotice = "A delegated task has already run for this request. Evaluate its report and do not delegate another task."
	turnBudgetNotice       = "You have used the tool budget for this request. Answer now from what you already have; no further tool calls are available."
	memoryOwnerNotice      = "memory search is available only to the configured owner"
)

const systemPrompt = "You are a GitHub workflow assistant. User messages may be prefixed with a Slack display name; treat that prefix as speaker metadata. Use search_memory when a request may depend on detailed personal profile, projects, infrastructure, relationships, security notes, preferences, or prior reusable knowledge that is not present in the always-on memory. Search with focused keywords and refine the query when needed; do not assume a missing search result means the fact is false. Use github_* tools for read-only repository questions. Use jina_read_url to extract clean content from a URL and jina_web_search when current web information is needed. Use delegate_to_coder when the user explicitly asks to write or edit code, create an issue, open a pull request, or delegate substantial pure research to the coding agent. Its complete brief must describe the task and stopping point, include the target owner/repo and explicit issue/PR instructions when repository work is required, or request findings rather than changes for repository-free research. Answer ordinary read-only questions yourself with the available read tools. After delegate_to_coder returns, evaluate its report yourself. For repository work, you may use only github_* tools to verify it. Then give a clear, concise final assessment. Do not make, request, or delegate any further changes if the result is incorrect; explain what is incorrect instead."

const slackFormattingPrompt = "Format the final response for Slack using Slack's message markup. Use *bold*, _italics_, ~strikethrough~, `inline code`, triple-backtick code blocks, and simple bulleted or numbered lists where useful. Do not use Markdown headings (#), Markdown bold (**text**), Markdown links ([label](URL)), or tables. Use short bold lead-ins instead of headings, and replace tabular data with readable lists. Keep formatting simple enough to render directly in a Slack message."

type ResponseFormat string

const ResponseFormatSlack ResponseFormat = "slack"

type Request struct {
	Speaker string
	// SpeakerID is the stable Slack user ID, used to gate owner-only memory.
	SpeakerID      string
	Message        string
	Images         []string
	History        []Turn
	ResponseFormat ResponseFormat
}

type Turn struct {
	Speaker     string
	Message     string
	Images      []string
	IsAssistant bool
}

// completionFunc is the model call, injectable so the tool loop can be tested
// without a network round trip.
type completionFunc func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error)

type Agent struct {
	completions completionFunc
	github      *github.Client
	jina        *jina.Client
	codingAgent *coder.Delegate
	memory      *memory.Store
	curator     *memory.Curator
	model       string
	owner       string
	tools       []openai.ChatCompletionToolUnionParam
}

func New(client openai.Client, githubClient *github.Client, jinaClient *jina.Client, codingAgent *coder.Delegate, store *memory.Store, curator *memory.Curator, model, owner string) *Agent {
	return &Agent{
		completions: func(ctx context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
			return client.Chat.Completions.New(ctx, params)
		},
		github:      githubClient,
		jina:        jinaClient,
		codingAgent: codingAgent,
		memory:      store,
		curator:     curator,
		model:       model,
		owner:       owner,
		tools:       append(append(tools.GitHubDefinitions(), tools.JinaDefinitions()...), tools.DelegateDefinition(), tools.MemorySearchDefinition()),
	}
}

func (a *Agent) Run(ctx context.Context, request Request) (string, error) {
	// Applied here so Slack and the CLI both get a deadline.
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	messages := []openai.ChatCompletionMessageParamUnion{openai.SystemMessage(a.systemMessage(request.ResponseFormat))}
	for _, turn := range request.History {
		if turn.IsAssistant {
			messages = append(messages, openai.AssistantMessage(turn.Message))
			continue
		}
		messages = append(messages, userMessage(turn.Speaker, turn.Message, turn.Images))
	}
	messages = append(messages, userMessage(request.Speaker, request.Message, request.Images))

	delegated := false
	for range maxToolTurns {
		choice, err := a.complete(ctx, messages, a.tools)
		if err != nil {
			return "", err
		}
		if len(choice.Message.ToolCalls) == 0 {
			return a.finish(request, messages, choice), nil
		}

		messages = append(messages, choice.Message.ToParam())
		for _, call := range choice.Message.ToolCalls {
			result := ""
			if call.Function.Name == tools.DelegateToCoder && delegated {
				result = repeatDelegationNotice
			} else {
				result = a.executeTool(ctx, request.SpeakerID, call.Function.Name, call.Function.Arguments)
				if call.Function.Name == tools.DelegateToCoder {
					delegated = true
				}
			}
			messages = append(messages, openai.ToolMessage(result, call.ID))
		}
	}

	// Ask once more with no tools, so the user gets an answer rather than an
	// error about the bot's internal limits.
	log.Printf("agent: tool budget of %d turns exhausted, forcing a final answer", maxToolTurns)
	messages = append(messages, openai.UserMessage(turnBudgetNotice))
	choice, err := a.complete(ctx, messages, nil)
	if err != nil {
		return "", err
	}
	return a.finish(request, messages, choice), nil
}

// complete performs one model call under its own deadline.
func (a *Agent) complete(ctx context.Context, messages []openai.ChatCompletionMessageParamUnion, toolset []openai.ChatCompletionToolUnionParam) (openai.ChatCompletionChoice, error) {
	ctx, cancel := context.WithTimeout(ctx, completionTimeout)
	defer cancel()

	completion, err := a.completions(ctx, openai.ChatCompletionNewParams{Messages: messages, Model: a.model, Tools: toolset})
	if err != nil {
		return openai.ChatCompletionChoice{}, err
	}
	if len(completion.Choices) == 0 {
		return openai.ChatCompletionChoice{}, fmt.Errorf("model returned no choices")
	}
	return completion.Choices[0], nil
}

// finish queues curation off the completed session and returns the reply.
func (a *Agent) finish(request Request, messages []openai.ChatCompletionMessageParamUnion, choice openai.ChatCompletionChoice) string {
	a.curator.Enqueue(memory.Job{SpeakerID: request.SpeakerID, Messages: append(messages, choice.Message.ToParam())})
	return choice.Message.Content
}

// systemMessage is the base prompt plus whatever long-term memory fits the
// prompt budget. Memory is delimited and marked as background so it never
// outranks what the user just said.
func (a *Agent) systemMessage(format ResponseFormat) string {
	prompt := systemPrompt
	if format == ResponseFormatSlack {
		prompt += "\n\n" + slackFormattingPrompt
	}
	section := a.memory.PromptSection()
	if section == "" {
		return prompt
	}
	return prompt + "\n\n" + section
}

func speakerMessage(speaker, message string) string {
	if strings.TrimSpace(speaker) == "" {
		return message
	}
	return speaker + ": " + message
}

func userMessage(speaker, message string, images []string) openai.ChatCompletionMessageParamUnion {
	text := speakerMessage(speaker, message)
	if len(images) == 0 {
		return openai.UserMessage(text)
	}

	parts := []openai.ChatCompletionContentPartUnionParam{openai.TextContentPart(text)}
	for _, image := range images {
		parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
			URL:    image,
			Detail: "auto",
		}))
	}
	return openai.UserMessage(parts)
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
			return "delegation error: " + err.Error()
		}
		return result
	}
	if name == tools.SearchMemory {
		if a.owner == "" || speakerID != a.owner {
			return memoryOwnerNotice
		}
		query, _ := args["query"].(string)
		limit, _ := tools.IntArg(args["limit"])
		results, err := a.memory.Search(query, limit)
		if err != nil {
			return "memory search error: " + err.Error()
		}
		return memory.FormatSearchResults(results)
	}
	result, known, err := tools.RunGitHub(ctx, a.github, name, args)
	if known {
		if err != nil {
			return "GitHub tool error: " + err.Error()
		}
		return result
	}
	result, known, err = tools.RunJina(ctx, a.jina, name, args)
	if !known {
		return "unknown tool: " + name
	}
	if err != nil {
		return "Jina tool error: " + err.Error()
	}
	return result
}
