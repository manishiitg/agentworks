package llmproviders

import (
	"context"
	"fmt"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// SearchWeb calls a model's native web search capability when available.
func SearchWeb(ctx context.Context, model llmtypes.Model, query string, options ...llmtypes.CallOption) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}

	if searchModel, ok := model.(llmtypes.WebSearchModel); ok {
		return searchModel.SearchWeb(ctx, query, options...)
	}

	if wrapped, ok := model.(*ProviderAwareLLM); ok {
		if searchModel, ok := wrapped.Model.(llmtypes.WebSearchModel); ok {
			return searchModel.SearchWeb(ctx, query, options...)
		}
	}

	return "", fmt.Errorf("model %T does not support native web search", model)
}
