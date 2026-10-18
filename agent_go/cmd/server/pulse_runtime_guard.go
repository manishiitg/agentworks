package server

import (
	"context"
	"fmt"
	"log"
	"strings"

	mcpexecutor "github.com/manishiitg/mcpagent/executor"
)

// pulseRunIDForSession resolves the only convenience value exposed to agents.
// A Pulse run is the active conversation, so its durable correlation id is the
// session id. It is not a lease, capability, or separately granted identity.
func pulseRunIDForSession(ctx context.Context, requestedRunID string) string {
	requestedRunID = strings.TrimSpace(requestedRunID)
	if requestedRunID == "current" {
		resolved := strings.TrimSpace(mcpexecutor.SessionIDFromContext(ctx))
		// Diagnostic for PLAT-196: "current" must resolve to the caller's own
		// MCP session id via ctx. If this ever logs empty, the ctx this tool
		// call executed under never had a session id attached to it — record
		// the resolution so a recurrence can be compared against whichever
		// session id the background/receipt-check side expected.
		if resolved == "" {
			log.Printf("[PULSE] pulseRunIDForSession: requested %q resolved to EMPTY session id from ctx", requestedRunID)
		} else {
			log.Printf("[PULSE] pulseRunIDForSession: requested %q resolved to session id %q", requestedRunID, resolved)
		}
		return resolved
	}
	return requestedRunID
}

// validatePulseToolRunID validates only that the call has a durable correlation
// id. Workflow conversation exclusivity is the writer boundary; Pulse itself
// owns no extra lease, capability, or session authorization layer.
func validatePulseToolRunID(ctx context.Context, requestedRunID string) error {
	requestedRunID = pulseRunIDForSession(ctx, requestedRunID)
	if requestedRunID == "" {
		return fmt.Errorf("pulse_run_id is required; use \"current\" inside a conversation")
	}
	return nil
}

// pulseReviewRunIDForSession separates the reviewer identity from the parent
// Pulse run correlation. A background Technical Maintenance sequence writes
// findings and its receipt under its exact child tool session while retaining
// the scheduler's pulse_run_id on those rows. In a normal foreground review
// both values naturally resolve to the same conversation session.
func pulseReviewRunIDForSession(ctx context.Context, pulseRunID string) string {
	if sessionID := strings.TrimSpace(mcpexecutor.SessionIDFromContext(ctx)); sessionID != "" {
		return sessionID
	}
	return strings.TrimSpace(pulseRunID)
}
