package llmtypes

import "context"

// WebSearchModel is an optional interface for models that support native web search.
// It is intentionally separate from Model to avoid forcing every adapter to implement it.
type WebSearchModel interface {
	SearchWeb(ctx context.Context, query string, options ...CallOption) (string, error)
}
