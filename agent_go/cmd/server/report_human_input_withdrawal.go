package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Withdrawal records Builder's explanation, not an owner answer or a verified
// outcome. Evidence references are agent-cited; Go does not judge obsolescence.
type ReportHumanInputWithdrawal struct {
	Reason      string   `json:"reason"`
	Evidence    []string `json:"evidence"`
	ActorID     string   `json:"actor_id"`
	SessionID   string   `json:"session_id"`
	WithdrawnAt string   `json:"withdrawn_at"`
}

func createHumanInputWithdrawalTool() llmtypes.Tool {
	return llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name:        "withdraw_human_input_request",
		Description: "Builder may withdraw an obsolete, still-unanswered agent-issued proposal in its own workflow. First read the request and verify why it is no longer needed: for completed work, cite the actual repair and the authority that allowed it. Supply a reason and evidence references. Preserves the question, options, approval contract and history as withdrawn; never answers, approves or authorizes work. Do not withdraw a still-needed approval to bypass it. Human-created requests, suggestions, answered/claimed requests and other workflows are refused. Pulse should discuss withdrawal with Builder.",
		Parameters: llmtypes.NewParameters(map[string]interface{}{
			"type": "object", "additionalProperties": false,
			"properties": map[string]interface{}{
				"workspace_path": map[string]interface{}{"type": "string"},
				"input_id":       map[string]interface{}{"type": "string"},
				"reason":         map[string]interface{}{"type": "string", "description": "Why this proposal no longer needs an owner decision; maximum 500 characters."},
				"evidence":       map[string]interface{}{"type": "array", "minItems": 1, "maxItems": 8, "items": map[string]interface{}{"type": "string"}, "description": "References to completed work, current authority or superseding decisions, not an invented owner answer."},
			},
			"required": []string{"workspace_path", "input_id", "reason", "evidence"},
		}),
	}}
}

func withdrawHumanInputFromToolArgs(ctx context.Context, args map[string]interface{}) (string, error) {
	sessionID := strings.TrimSpace(executor.SessionIDFromContext(ctx))
	if sessionID == "" {
		sessionID, _ = ctx.Value(common.ChatSessionIDKey).(string)
		sessionID = strings.TrimSpace(sessionID)
	}
	_, background := step.LookupWorkshopToolSession(sessionID)
	if sessionID == "" || background || isScheduledSession(sessionID) || isGoalLeadSessionID(sessionID) {
		return "", fmt.Errorf("only the workflow's Builder chat may withdraw its agent-issued proposals; discuss this with Builder")
	}
	ws, claims, err := pulsePlatformAPI.pulseToolScope(ctx, stringToolArg(args, "workspace_path"), true)
	if err != nil {
		return "", err
	}
	record := ReportHumanInputWithdrawal{
		Reason: strings.TrimSpace(stringToolArg(args, "reason")), Evidence: stringSliceFromToolArg(args["evidence"]),
		ActorID: claims.UserID, SessionID: sessionID,
	}
	input, err := withdrawReportHumanInput(ctx, ws, stringToolArg(args, "input_id"), record)
	if err != nil {
		return "", err
	}
	return marshalReportHumanInputToolResult("withdrawn", input)
}

func withdrawReportHumanInput(ctx context.Context, workspacePath, inputID string, withdrawal ReportHumanInputWithdrawal) (*ReportHumanInput, error) {
	inputID = strings.TrimSpace(inputID)
	if inputID == "" || withdrawal.Reason == "" || len(withdrawal.Reason) > 500 || len(withdrawal.Evidence) == 0 || len(withdrawal.Evidence) > 8 {
		return nil, fmt.Errorf("input_id, a reason (1-500 characters) and 1-8 evidence references are required")
	}
	for i, ref := range withdrawal.Evidence {
		withdrawal.Evidence[i] = strings.TrimSpace(ref)
		if withdrawal.Evidence[i] == "" || len(withdrawal.Evidence[i]) > 500 {
			return nil, fmt.Errorf("each evidence reference must contain 1-500 characters")
		}
	}
	defer publishHumanInputsChanged(workspacePath)
	reportHumanInputStoreMu.Lock()
	defer reportHumanInputStoreMu.Unlock()
	normalized, db, err := openReportHumanInputDB(ctx, workspacePath, false)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, fmt.Errorf("workflow decision database not found")
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// Original creation provenance, not a model-provided created_by label or a
	// later refresh, establishes that this was the workflow agents' proposal.
	// Pending and unanswered are rechecked atomically against concurrent answers.
	withdrawal.WithdrawnAt = time.Now().UTC().Format(time.RFC3339)
	details, err := json.Marshal(withdrawal)
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE report_human_inputs
		SET status='withdrawn', withdrawal_json=?, updated_at=?
		WHERE workspace_path=? AND id=? AND status='pending' AND source<>'user_suggestion'
		AND selected_option_id='' AND note='' AND answered_at='' AND answered_by='' AND claim_token=''
		AND (SELECT actor_kind FROM report_human_input_events
		     WHERE workspace_path=? AND input_id=? AND event_type='created' ORDER BY id LIMIT 1)='agent'`,
		string(details), withdrawal.WithdrawnAt, normalized, inputID, normalized, inputID)
	if err != nil {
		return nil, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return nil, err
	} else if n != 1 {
		return nil, fmt.Errorf("request must be pending and unanswered with original agent creation provenance; human requests, suggestions and changed decisions cannot be withdrawn")
	}
	if err := writeReportHumanInputEvent(ctx, tx, normalized, reportHumanInputEvent{
		InputID: inputID, EventType: "withdrawn", Status: "withdrawn", ActorID: withdrawal.ActorID,
		ActorKind: "agent", Channel: "builder_tool", SessionID: withdrawal.SessionID,
		Details: string(details), CreatedAt: withdrawal.WithdrawnAt,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return getReportHumanInputByID(ctx, db, normalized, inputID)
}
