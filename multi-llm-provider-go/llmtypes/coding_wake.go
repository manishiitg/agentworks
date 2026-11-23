package llmtypes

import "context"

// A coding CLI can start a turn on its own after the reply ended, for example
// Claude when its background work finishes. The host shows that turn in the
// chat by running a turn that adopts it: the adapter sends no person message
// and streams the CLI's own turn as this turn's output (PLAT-746-899).

type codingProviderAdoptWakeKey struct{}

// WithCodingProviderAdoptWake marks a call as adopting the turn the CLI started
// on its own; the call's message is not sent to the CLI.
func WithCodingProviderAdoptWake(ctx context.Context) context.Context {
	return context.WithValue(ctx, codingProviderAdoptWakeKey{}, true)
}

// CodingProviderAdoptWakeFromContext reports whether the call adopts such a turn.
func CodingProviderAdoptWakeFromContext(ctx context.Context) bool {
	adopt, _ := ctx.Value(codingProviderAdoptWakeKey{}).(bool)
	return adopt
}
