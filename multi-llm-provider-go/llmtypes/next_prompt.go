package llmtypes

import (
	"context"
	"strings"
	"sync"
	"unicode/utf8"
)

// NextPromptScope is supplied by the host for one chat turn. It is never an
// instruction to send a message, and cannot be inherited by another owner or
// provider. Programs without native cached sampling simply never produce one.
type NextPromptScope struct {
	StillCurrent            func(hostTurn string) bool
	Owner, Provider, TurnID string
	Eligible                bool
	Complete                func(NextPromptResult)
}
type NextPromptResult struct {
	HostTurn                 string
	Text, Reason             string
	NativeThread, NativeTurn string
	Usage                    *NextPromptUsage
}

// NextPromptUsage is optional native sampler accounting, not prediction text.
// Input includes CachedInput. Missing native counts are never invented.
type NextPromptUsage struct {
	ResponseID, Model          string
	Input, CachedInput, Output int64
}
type nextPromptKey struct{}

func WithNextPrompt(ctx context.Context, scope NextPromptScope) context.Context {
	if scope.Complete != nil {
		original := scope.Complete
		var once sync.Once
		scope.Complete = func(result NextPromptResult) { once.Do(func() { original(result) }) }
	}
	return context.WithValue(ctx, nextPromptKey{}, scope)
}
func NextPromptFor(ctx context.Context, provider, owner string) NextPromptScope {
	scope, _ := ctx.Value(nextPromptKey{}).(NextPromptScope)
	if scope.Owner != owner || scope.Provider != provider || scope.TurnID == "" || scope.Complete == nil {
		return NextPromptScope{}
	}
	return scope
}
func (scope NextPromptScope) Finish(result NextPromptResult) {
	if scope.Complete == nil {
		return
	}
	result.HostTurn = scope.TurnID
	result.Text = strings.TrimSpace(result.Text)
	if !scope.Eligible {
		result.Text = ""
		if result.Reason == "" {
			result.Reason = "ineligible_turn"
		}
	}
	if result.Text != "" && !scope.Current() {
		result.Text = ""
		result.Reason = "newer_input_or_stopped"
	}
	// A suggestion is short plain text, not a generated reply or tool request.
	if utf8.RuneCountInString(result.Text) > 500 {
		result.Text = ""
		result.Reason = "too_long"
	}
	if result.Text == "" && result.Reason == "" {
		result.Reason = "empty"
	}
	scope.Complete(result)
}
func NextPromptNearLimit(windows []RateLimitWindow) bool {
	for _, window := range windows {
		if window.UsedPercent >= 90 {
			return true
		}
	}
	return false
}

// Current is a host-side cancellation fence, independent of a normal turn's
// context cancellation after completion. Native thread identity is checked too.
func (scope NextPromptScope) Current() bool {
	return scope.Complete != nil && (scope.StillCurrent == nil || scope.StillCurrent(scope.TurnID))
}
