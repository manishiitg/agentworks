package llmtypes

import "context"

type codingAgentAutoReviewKey struct{}

// WithCodingAgentAutoReview marks one turn of one chat to run under the coding
// program's own approval reviewer (Claude Code's "auto" permission mode,
// Cursor's Auto-review) instead of approving every action itself. The host sets
// it only from the chat's saved access mode, chosen by its person; it is never
// read from prompt text.
func WithCodingAgentAutoReview(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, codingAgentAutoReviewKey{}, sessionID)
}

// CodingAgentAutoReviewForSession reports whether this turn of sessionID runs
// under the program's own reviewer. A parent's mode never becomes a delegated
// child's: the key names one session.
func CodingAgentAutoReviewForSession(ctx context.Context, sessionID string) bool {
	owner, _ := ctx.Value(codingAgentAutoReviewKey{}).(string)
	return sessionID != "" && owner == sessionID
}
