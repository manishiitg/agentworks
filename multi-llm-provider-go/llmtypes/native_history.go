package llmtypes

import (
	"context"
	"strings"
	"sync/atomic"
)

// NativeHistoryItem is one entry of an earlier conversation the host hands a
// coding agent that starts without its own memory (an agent switch, a restart
// that cannot resume). Role is "user" or "assistant". An agent that accepts
// history natively adds the items to its thread as real conversation entries;
// any other agent gets NativeHistoryText in front of the user's message.
type NativeHistoryItem struct {
	Role string
	Text string
}

type nativeHistoryScope struct {
	sessionID string
	items     []NativeHistoryItem
	claimed   *atomic.Bool
}
type nativeHistoryKey struct{}

// WithNativeHistory attaches the history for one delivery of sessionID.
func WithNativeHistory(ctx context.Context, sessionID string, items []NativeHistoryItem) context.Context {
	return context.WithValue(ctx, nativeHistoryKey{}, nativeHistoryScope{sessionID: sessionID, items: append([]NativeHistoryItem(nil), items...), claimed: &atomic.Bool{}})
}

// ClaimNativeHistoryForSession returns the history once: a retried request
// on the same delivery context must not add it to the thread twice.
func ClaimNativeHistoryForSession(ctx context.Context, sessionID string) []NativeHistoryItem {
	scope, ok := ctx.Value(nativeHistoryKey{}).(nativeHistoryScope)
	if !ok || sessionID == "" || scope.sessionID != sessionID || len(scope.items) == 0 || !scope.claimed.CompareAndSwap(false, true) {
		return nil
	}
	return append([]NativeHistoryItem(nil), scope.items...)
}

// NativeHistoryText renders the items as text, for agents (or failures) that
// cannot take them natively.
func NativeHistoryText(items []NativeHistoryItem) string {
	var parts []string
	for _, item := range items {
		if text := strings.TrimSpace(item.Text); text != "" {
			if item.Role == "assistant" {
				text = "assistant: " + text
			} else if len(parts) > 0 {
				text = "user: " + text
			}
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}
