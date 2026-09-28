package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	baseevents "github.com/manishiitg/mcpagent/events"
)

// humanFeedbackWait is how long a workflow waits for an answer. The stored
// request expires at the same moment, so no answer is refused as expired
// while the workflow still waits.
const humanFeedbackWait = 10 * time.Minute

// registerHumanFeedback records the question in the authoritative store with
// the session it belongs to, its real choices and its free-text policy (not
// the metadata-less CreateRequestWithoutNotification). The session binding is
// what lets the owner see it in run_status.pending_inputs and answer it with
// run_reply_input over MCP or the CLI; without it those APIs never saw the
// question and refused the answer (PLAT-365).
func registerHumanFeedback(store *virtualtools.HumanFeedbackStore, requestID, question, contextMsg, sessionID string, options []string, allowFeedback bool) error {
	return store.CreatePendingRequest(requestID, question, contextMsg, sessionID, options, allowFeedback, humanFeedbackWait)
}

// RequestHumanFeedback is a common function for requesting human feedback with blocking behavior
// Returns: (approved bool, feedback string, error)
func (bo *BaseOrchestrator) RequestHumanFeedback(
	ctx context.Context,
	requestID string,
	question string,
	context string,
	sessionID string,
	workflowID string,
) (bool, string, error) {
	// Removed verbose logging

	// Use HumanFeedbackStore to wait for response
	feedbackStore := virtualtools.GetHumanFeedbackStore()

	if err := registerHumanFeedback(feedbackStore, requestID, question, context, sessionID, []string{"Approve & Continue", "Reject"}, true); err != nil {
		return false, "", fmt.Errorf("failed to create feedback request: %w", err)
	}

	// Human input is answered directly through AgentWorks. The Workflow Builder
	// is not used as a relay, and notification connectors are intentionally not
	// used for blocking feedback requests.

	feedbackEvent := &events.BlockingHumanFeedbackEvent{
		BaseEventData: baseevents.BaseEventData{Timestamp: time.Now()},
		Question:      question,
		AllowFeedback: true,
		Context:       context,
		SessionID:     sessionID,
		WorkflowID:    workflowID,
		RequestID:     requestID,
		YesNoOnly:     false,
		YesLabel:      "Approve & Continue",
		NoLabel:       "Reject",
	}

	// Use the context-aware bridge to emit the event
	bridge := bo.GetContextAwareBridge()
	if bridge == nil {
		bo.GetLogger().Error("❌ Context-aware bridge is nil, cannot emit blocking human feedback event", fmt.Errorf("context-aware bridge is nil"))
		return false, "", fmt.Errorf("context-aware bridge is nil")
	}
	if err := bridge.HandleEvent(ctx, &baseevents.AgentEvent{
		Type:      events.BlockingHumanFeedback,
		Timestamp: time.Now(),
		Data:      feedbackEvent,
	}); err != nil {
		bo.GetLogger().Error(fmt.Sprintf("❌ Failed to emit human feedback event: %v", err), err)
		return false, "", fmt.Errorf("failed to emit event: %w", err)
	}

	// Removed verbose logging

	// BLOCKING CALL - waits here until response or timeout
	response, err := feedbackStore.WaitForResponseCtx(ctx, requestID, humanFeedbackWait)
	bo.emitHumanFeedbackResolved(ctx, requestID, sessionID, err)
	if err != nil {
		return false, "", fmt.Errorf("timeout waiting for human feedback: %w", err)
	}

	// Removed verbose logging

	// Parse response
	// Expected format: "Approve & Continue", "Approve", or feedback text for revision
	responseTrimmed := strings.TrimSpace(response)
	if responseTrimmed == "Approve & Continue" || responseTrimmed == "Approve" {
		return true, "", nil
	}

	// Default: treat as feedback for revision
	return false, response, nil
}

// RequestYesNoFeedback requests simple yes/no feedback from user with Approve/Reject buttons
// Returns: (approved bool, error)
func (bo *BaseOrchestrator) RequestYesNoFeedback(
	ctx context.Context,
	requestID string,
	question string,
	yesLabel string,
	noLabel string,
	context string,
	sessionID string,
	workflowID string,
) (bool, error) {
	// Removed verbose logging

	// Set default labels if not provided
	if yesLabel == "" {
		yesLabel = "Approve"
	}
	if noLabel == "" {
		noLabel = "Reject"
	}

	// Wait for response
	feedbackStore := virtualtools.GetHumanFeedbackStore()
	if err := registerHumanFeedback(feedbackStore, requestID, question, context, sessionID, []string{yesLabel, noLabel}, false); err != nil {
		return false, fmt.Errorf("failed to create feedback request: %w", err)
	}

	feedbackEventYN := &events.BlockingHumanFeedbackEvent{
		BaseEventData: baseevents.BaseEventData{Timestamp: time.Now()},
		Question:      question,
		AllowFeedback: false,
		YesNoOnly:     true,
		YesLabel:      yesLabel,
		NoLabel:       noLabel,
		Context:       context,
		SessionID:     sessionID,
		WorkflowID:    workflowID,
		RequestID:     requestID,
	}

	if err := bo.GetContextAwareBridge().HandleEvent(ctx, &baseevents.AgentEvent{
		Type:      events.BlockingHumanFeedback,
		Timestamp: time.Now(),
		Data:      feedbackEventYN,
	}); err != nil {
		bo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to emit yes/no feedback event: %v", err))
	}

	// Removed verbose logging

	response, err := feedbackStore.WaitForResponseCtx(ctx, requestID, humanFeedbackWait)
	bo.emitHumanFeedbackResolved(ctx, requestID, sessionID, err)
	if err != nil {
		return false, fmt.Errorf("timeout waiting for feedback: %w", err)
	}

	// Removed verbose logging

	// Parse response: "Approve" or the custom yesLabel means Yes; natural
	// yes/true variants remain accepted for direct text input.
	trimmed := strings.TrimSpace(response)
	if trimmed == "Approve" || strings.EqualFold(trimmed, yesLabel) {
		return true, nil
	}
	switch strings.ToLower(trimmed) {
	case "yes", "y", "true", "approve":
		return true, nil
	}
	return false, nil
}

// RequestMultipleChoiceFeedback requests multiple-choice feedback from user
// Returns: (choice string, error) where choice is "option0", "option1", "option2", etc. (0-based index)
func (bo *BaseOrchestrator) RequestMultipleChoiceFeedback(
	ctx context.Context,
	requestID string,
	question string,
	options []string,
	context string,
	sessionID string,
	workflowID string,
) (string, error) {
	// Removed verbose logging

	if len(options) == 0 {
		return "", fmt.Errorf("at least one option is required")
	}

	// Wait for response
	feedbackStore := virtualtools.GetHumanFeedbackStore()
	if err := registerHumanFeedback(feedbackStore, requestID, question, context, sessionID, options, false); err != nil {
		return "", fmt.Errorf("failed to create feedback request: %w", err)
	}

	feedbackEventMC := &events.BlockingHumanFeedbackEvent{
		BaseEventData: baseevents.BaseEventData{Timestamp: time.Now()},
		Question:      question,
		AllowFeedback: false,
		Options:       options,
		Context:       context,
		SessionID:     sessionID,
		WorkflowID:    workflowID,
		RequestID:     requestID,
	}

	if err := bo.GetContextAwareBridge().HandleEvent(ctx, &baseevents.AgentEvent{
		Type:      events.BlockingHumanFeedback,
		Timestamp: time.Now(),
		Data:      feedbackEventMC,
	}); err != nil {
		bo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to emit multiple-choice feedback event: %v", err))
	}

	// Removed verbose logging

	response, err := feedbackStore.WaitForResponseCtx(ctx, requestID, humanFeedbackWait)
	bo.emitHumanFeedbackResolved(ctx, requestID, sessionID, err)
	if err != nil {
		return "", fmt.Errorf("timeout waiting for feedback: %w", err)
	}

	// Removed verbose logging

	// Parse response: should be "option0", "option1", "option2", etc. (0-based)
	response = strings.TrimSpace(response)

	// Validate response format (option0, option1, option2, etc.)
	if strings.HasPrefix(response, "option") {
		// Extract index from "option0", "option1", etc.
		indexStr := strings.TrimPrefix(response, "option")
		index, err := strconv.Atoi(indexStr)
		if err == nil && index >= 0 && index < len(options) {
			return response, nil
		}
	}

	if index, err := strconv.Atoi(response); err == nil {
		if index > 0 && index <= len(options) {
			return fmt.Sprintf("option%d", index-1), nil
		}
		if index == 0 && len(options) > 0 {
			return "option0", nil
		}
	}

	// Fallback: match by option text (case-insensitive), which is what the direct
	// UI submits for button choices.
	for i, opt := range options {
		if strings.EqualFold(strings.TrimSpace(opt), response) {
			return fmt.Sprintf("option%d", i), nil
		}
	}

	// Default to option0 if response is unclear
	bo.GetLogger().Warn(fmt.Sprintf("⚠️ Unexpected response format: %s, defaulting to option0", response))
	return "option0", nil
}

// emitHumanFeedbackResolved records the durable answered/expired marker for a
// blocking request once its wait ends, from whichever surface answered it.
func (bo *BaseOrchestrator) emitHumanFeedbackResolved(ctx context.Context, requestID, sessionID string, waitErr error) {
	bridge := bo.GetContextAwareBridge()
	if bridge == nil || requestID == "" {
		return
	}
	outcome := "answered"
	if errors.Is(waitErr, virtualtools.ErrFeedbackCancelled) {
		outcome = "cancelled"
	} else if waitErr != nil {
		outcome = "expired"
	}
	now := time.Now()
	if err := bridge.HandleEvent(ctx, &baseevents.AgentEvent{
		Type:      events.HumanFeedbackResolved,
		Timestamp: now,
		Data: &events.HumanFeedbackResolvedEvent{
			BaseEventData: baseevents.BaseEventData{Timestamp: now},
			RequestID:     requestID,
			Outcome:       outcome,
			SessionID:     sessionID,
		},
	}); err != nil {
		bo.GetLogger().Warn(fmt.Sprintf("failed to emit human_feedback_resolved for %s: %v", requestID, err))
	}
}
