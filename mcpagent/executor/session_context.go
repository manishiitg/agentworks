package executor

import "context"

type sessionIDContextKey struct{}

// WithSessionID records the trusted execution session on a custom-tool context.
// Tool arguments are model-controlled and must not be used as session identity.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionIDContextKey{}, sessionID)
}

// SessionIDFromContext returns the trusted execution session attached by the
// custom-tool HTTP boundary.
func SessionIDFromContext(ctx context.Context) string {
	sessionID, _ := ctx.Value(sessionIDContextKey{}).(string)
	return sessionID
}
