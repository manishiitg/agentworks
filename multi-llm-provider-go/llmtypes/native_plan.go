package llmtypes

import (
	"context"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

type nativePlanModeKey struct{}

// WithNativePlanMode marks one turn of one chat to run in the coding program's
// own plan mode (the person typed /plan). The host sets it only for an
// authenticated person's own message; the mode is never read from prompt text.
func WithNativePlanMode(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, nativePlanModeKey{}, sessionID)
}

// NativePlanModeForSession reports whether this turn of sessionID plans first.
// A parent's plan turn never becomes a delegated child's mode.
func NativePlanModeForSession(ctx context.Context, sessionID string) bool {
	owner, _ := ctx.Value(nativePlanModeKey{}).(string)
	return sessionID != "" && owner == sessionID
}

type NativePlanUnavailableError struct{ Program string }

func (e *NativePlanUnavailableError) Error() string {
	return "Plan mode is not available for " + e.Program + ". Send the message without /plan."
}

// NativePlanUnavailable refuses a /plan turn a transport cannot run in plan
// mode, rather than running the task without planning first.
func NativePlanUnavailable(program string) error {
	return &llmerrors.Error{Kind: llmerrors.KindInvalidRequest, Provider: program, Err: &NativePlanUnavailableError{Program: program}}
}
