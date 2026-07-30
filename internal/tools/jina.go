package tools

import (
	"context"

	"github.com/hetsaraiya/spores/internal/jina"
	"github.com/openai/openai-go/v3"
)

const (
	JinaReadURL   = "jina_read_url"
	JinaWebSearch = "jina_web_search"
)

func JinaDefinitions() []openai.ChatCompletionToolUnionParam {
	return []openai.ChatCompletionToolUnionParam{
		definition(JinaReadURL, "Extract clean, LLM-friendly content from a URL using Jina Reader.", properties(argURL), []string{argURL}),
		definition(JinaWebSearch, "Search the web using Jina Search and return LLM-friendly results.", properties(argQuery), []string{argQuery}),
	}
}

var jinaHandlers = map[string]func(context.Context, *jina.Client, arguments) (string, error){
	JinaReadURL: func(ctx context.Context, client *jina.Client, args arguments) (string, error) {
		return client.Read(ctx, args.text(argURL))
	},
	JinaWebSearch: func(ctx context.Context, client *jina.Client, args arguments) (string, error) {
		return client.Search(ctx, args.text(argQuery))
	},
}

func RunJina(ctx context.Context, client *jina.Client, name string, args map[string]any) (string, bool, error) {
	run, known := jinaHandlers[name]
	if !known {
		return "", false, nil
	}
	result, err := run(ctx, client, arguments(args))
	return result, true, err
}
