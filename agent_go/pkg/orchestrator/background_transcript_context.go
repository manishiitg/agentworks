package orchestrator

import "context"

type backgroundTranscriptSessionIDKey struct{}

// WithBackgroundTranscriptSession binds durable capture to the owning chat,
// independently of the provider's runtime/MCP session. It does not alter the
// provider session or its security scope; execution owners remain context-local.
func WithBackgroundTranscriptSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, backgroundTranscriptSessionIDKey{}, sessionID)
}
