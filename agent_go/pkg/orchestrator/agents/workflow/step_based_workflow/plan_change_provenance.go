package step_based_workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	mcpexecutor "github.com/manishiitg/mcpagent/executor"
)

type planChangeOriginContextKey struct{}

type PlanChangeOrigin struct {
	Type         string   `json:"type"`
	AgentName    string   `json:"agent_name,omitempty"`
	SessionID    string   `json:"session_id,omitempty"`
	UserID       string   `json:"user_id,omitempty"`
	Username     string   `json:"username,omitempty"`
	OperationID  string   `json:"operation_id,omitempty"`
	ViaToken     string   `json:"via_token,omitempty"`
	PulseRunID   string   `json:"pulse_run_id,omitempty"`
	IssueIDs     []string `json:"issue_ids,omitempty"`
	FixAttemptID string   `json:"fix_attempt_id,omitempty"`
	HumanInputID string   `json:"human_input_id,omitempty"`
}

type planChangeOriginRegistrar struct {
	DefinitionToolRegistrar
	agentName string
}

func (r planChangeOriginRegistrar) decorate(execute func(context.Context, map[string]interface{}) (string, error)) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		return execute(withPlanChangeOrigin(ctx, r.agentName), args)
	}
}

func (r planChangeOriginRegistrar) RegisterCustomTool(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), displayGroup string) error {
	return r.DefinitionToolRegistrar.RegisterCustomTool(name, description, parameters, r.decorate(execute), displayGroup)
}

func (r planChangeOriginRegistrar) RegisterCustomToolWithTimeout(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, displayGroup string) error {
	return r.DefinitionToolRegistrar.RegisterCustomToolWithTimeout(name, description, parameters, r.decorate(execute), timeout, displayGroup)
}

func withPlanChangeOrigin(ctx context.Context, agentName string) context.Context {
	if existing, ok := ctx.Value(planChangeOriginContextKey{}).(PlanChangeOrigin); ok && existing.Type == "external_builder" {
		return ctx
	}
	agentName = strings.TrimSpace(agentName)
	sessionID := strings.TrimSpace(mcpexecutor.SessionIDFromContext(ctx))
	if sessionID == "" {
		sessionID, _ = ctx.Value(common.ChatSessionIDKey).(string)
		sessionID = strings.TrimSpace(sessionID)
	}
	originType := "other"
	userID, _ := ctx.Value(common.UserIDKey).(string)
	username, _ := ctx.Value(common.UsernameKey).(string)
	switch strings.ToLower(agentName) {
	case "workflow-builder":
		originType = "user_chat"
	case "planning agent", "plan improvement agent":
		originType = "planner"
	case "background-task":
		originType = "background_agent"
	}
	return context.WithValue(ctx, planChangeOriginContextKey{}, PlanChangeOrigin{
		Type: originType, AgentName: agentName, SessionID: sessionID,
		UserID: strings.TrimSpace(userID), Username: strings.TrimSpace(username),
	})
}

// WithExternalBuilderPlanOrigin is called only after the server validates the
// persisted operation grant. The normal workflow-builder decorator preserves
// this stronger provenance in the plan changelog.
func WithExternalBuilderPlanOrigin(ctx context.Context, operationID, grantID, userID, username, sessionID string) context.Context {
	return context.WithValue(ctx, planChangeOriginContextKey{}, PlanChangeOrigin{
		Type: "external_builder", AgentName: "workflow-builder", SessionID: sessionID,
		UserID: userID, Username: username, OperationID: operationID, ViaToken: "token:" + grantID,
	})
}

func planChangeOriginFromContext(ctx context.Context, workspacePath string) PlanChangeOrigin {
	origin, _ := ctx.Value(planChangeOriginContextKey{}).(PlanChangeOrigin)
	if strings.TrimSpace(origin.Type) == "" {
		origin.Type = "other"
	}
	if origin.SessionID == "" {
		origin.SessionID = strings.TrimSpace(mcpexecutor.SessionIDFromContext(ctx))
	}
	// A Pulse run records its gate mode before any Fixer mutation. That durable
	// row is stronger evidence than the generic background-agent name.
	if origin.SessionID != "" && pulseRunExistsForChange(ctx, workspacePath, origin.SessionID) {
		origin.Type = "pulse_fixer"
		origin.PulseRunID = origin.SessionID
	}
	return origin
}

func pulseRunExistsForChange(ctx context.Context, workspacePath, sessionID string) bool {
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return false
	}
	defer db.Close()
	var exists int
	err = db.QueryRowContext(ctx, `SELECT 1 FROM pulse_run_mode WHERE pulse_run_id=? LIMIT 1`, strings.TrimSpace(sessionID)).Scan(&exists)
	return err == nil && exists == 1
}

func pulseChangeReferences(ctx context.Context, workspacePath, pulseRunID string) (issueIDs []string, attemptID, humanInputID string) {
	if strings.TrimSpace(pulseRunID) == "" {
		return nil, "", ""
	}
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return nil, "", ""
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT af.finding_id, a.attempt_id,
		COALESCE((SELECT json_extract(e.metadata_json, '$.human_input_id')
			FROM pulse_finding_events e
			WHERE e.fingerprint=af.fingerprint AND e.event_type='awaiting_user'
			ORDER BY e.recorded_at DESC, e._id DESC LIMIT 1), '')
		FROM pulse_fix_attempts a JOIN pulse_fix_attempt_findings af ON af.attempt_id=a.attempt_id
		WHERE a.pulse_run_id=? ORDER BY a.started_at DESC`, strings.TrimSpace(pulseRunID))
	if err != nil {
		return nil, "", ""
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var issueID, candidate, decisionID string
		if rows.Scan(&issueID, &candidate, &decisionID) != nil {
			continue
		}
		issueID = strings.TrimSpace(issueID)
		if issueID != "" && !seen[issueID] {
			seen[issueID] = true
			issueIDs = append(issueIDs, issueID)
		}
		if attemptID == "" {
			attemptID = strings.TrimSpace(candidate)
		}
		if humanInputID == "" {
			humanInputID = strings.TrimSpace(decisionID)
		}
	}
	return issueIDs, attemptID, humanInputID
}

var requiredPlanDependencySurfaces = []string{
	"downstream_steps",
	"validation",
	"evaluation",
	"reporting",
	"database",
	"learnings_and_knowledge",
}

func planDependencySurfaceReviewSchema() map[string]interface{} {
	properties := make(map[string]interface{}, len(requiredPlanDependencySurfaces))
	for _, surface := range requiredPlanDependencySurfaces {
		properties[surface] = map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"disposition": map[string]interface{}{
					"type": "string",
					"enum": []interface{}{"updated", "already_compatible", "not_applicable", "blocked", "broken"},
				},
				"evidence": map[string]interface{}{
					"type": "array", "minItems": 1,
					"items": map[string]interface{}{"type": "string", "minLength": 1},
				},
				"issue_ids": map[string]interface{}{
					"type": "array", "minItems": 1,
					"items":       map[string]interface{}{"type": "string", "minLength": 1},
					"description": "Required for blocked or broken dispositions; durable Pulse issue ids that own the unresolved repair.",
				},
			},
			"required": []string{"disposition", "evidence"},
		}
	}
	return map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"description":          "Disposition and concrete evidence for every dependency surface. Required except for legacy cursor-backfill.",
		"properties":           properties,
		"required":             append([]string(nil), requiredPlanDependencySurfaces...),
	}
}

func parsePlanDependencySurfaceReviews(raw interface{}) (map[string]PlanDependencySurfaceReview, error) {
	object, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("surface_reviews must be an object")
	}
	reviews := make(map[string]PlanDependencySurfaceReview, len(requiredPlanDependencySurfaces))
	for _, surface := range requiredPlanDependencySurfaces {
		rawReview, exists := object[surface]
		if !exists {
			return nil, fmt.Errorf("surface_reviews.%s is required", surface)
		}
		reviewObject, ok := rawReview.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("surface_reviews.%s must be an object", surface)
		}
		disposition := strings.TrimSpace(asString(reviewObject["disposition"]))
		switch disposition {
		case "updated", "already_compatible", "not_applicable", "blocked", "broken":
		default:
			return nil, fmt.Errorf("surface_reviews.%s.disposition is invalid", surface)
		}
		rawEvidence, ok := reviewObject["evidence"].([]interface{})
		if !ok || len(rawEvidence) == 0 {
			return nil, fmt.Errorf("surface_reviews.%s.evidence requires at least one item", surface)
		}
		evidence := make([]string, 0, len(rawEvidence))
		for _, item := range rawEvidence {
			value := strings.TrimSpace(asString(item))
			if value == "" {
				return nil, fmt.Errorf("surface_reviews.%s.evidence contains an empty item", surface)
			}
			evidence = append(evidence, value)
		}
		var issueIDs []string
		if rawIssueIDs, ok := reviewObject["issue_ids"].([]interface{}); ok {
			for _, rawIssueID := range rawIssueIDs {
				issueIDs = append(issueIDs, asString(rawIssueID))
			}
		}
		issueIDs = normalizedLifecycleStrings(issueIDs)
		if (disposition == "blocked" || disposition == "broken") && len(issueIDs) == 0 {
			return nil, fmt.Errorf("surface_reviews.%s.issue_ids requires at least one durable Pulse issue for %s", surface, disposition)
		}
		reviews[surface] = PlanDependencySurfaceReview{Disposition: disposition, Evidence: evidence, IssueIDs: issueIDs}
	}
	for surface := range object {
		known := false
		for _, required := range requiredPlanDependencySurfaces {
			if surface == required {
				known = true
				break
			}
		}
		if !known {
			return nil, fmt.Errorf("surface_reviews contains unknown surface %q", surface)
		}
	}
	return reviews, nil
}

func cursorBackfillSurfaceReviews() map[string]PlanDependencySurfaceReview {
	reviews := make(map[string]PlanDependencySurfaceReview, len(requiredPlanDependencySurfaces))
	for _, surface := range requiredPlanDependencySurfaces {
		reviews[surface] = PlanDependencySurfaceReview{
			Disposition: "not_applicable",
			Evidence:    []string{"legacy cursor backfill; no structured dependency review existed when this change was recorded"},
		}
	}
	return reviews
}
