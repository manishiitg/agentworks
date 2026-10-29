package step_based_workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	mcpexecutor "github.com/manishiitg/mcpagent/executor"
)

// PlanMutationContext attaches a server-owned read receipt to each native mutation.
// It never serializes a whole conversation.
var PlanMutationContext func(context.Context, map[string]interface{}) context.Context

type planChangeOriginContextKey struct{}

type PlanChangeOrigin struct {
	Type         string   `json:"type"`
	AgentName    string   `json:"agent_name,omitempty"`
	SessionID    string   `json:"session_id,omitempty"`
	UserID       string   `json:"user_id,omitempty"`
	Username     string   `json:"username,omitempty"`
	OperationID  string   `json:"operation_id,omitempty"`
	ViaToken     string   `json:"via_token,omitempty"`
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
		if PlanMutationContext != nil {
			ctx = PlanMutationContext(ctx, args)
			copyArgs := make(map[string]interface{}, len(args))
			for k, v := range args {
				if k != "expected_plan_revision" {
					copyArgs[k] = v
				}
			}
			args = copyArgs
		}
		return execute(withPlanChangeOrigin(ctx, r.agentName), args)
	}
}

func (r planChangeOriginRegistrar) RegisterCustomTool(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), displayGroup string) error {
	return r.DefinitionToolRegistrar.RegisterCustomTool(name, description, planRevisionParameters(parameters), r.decorate(execute), displayGroup)
}

func (r planChangeOriginRegistrar) RegisterCustomToolWithTimeout(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, displayGroup string) error {
	return r.DefinitionToolRegistrar.RegisterCustomToolWithTimeout(name, description, planRevisionParameters(parameters), r.decorate(execute), timeout, displayGroup)
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

// WithWorkflowSettingsOrigin attributes a change (a typed settings save, a
// knowledge base edit, a team source edit) to its trusted authenticated actor.
// Every workflow change log entry must say who made it; an empty userID leaves
// the context unchanged so a system write is never attributed to nobody-as-user.
func WithWorkflowSettingsOrigin(ctx context.Context, userID, username string) context.Context {
	userID, username = strings.TrimSpace(userID), strings.TrimSpace(username)
	if userID == "" {
		return ctx
	}
	origin, _ := ctx.Value(planChangeOriginContextKey{}).(PlanChangeOrigin)
	if origin.UserID != userID {
		origin.Username = ""
	}
	origin.UserID = userID
	if username != "" {
		origin.Username = username
	}
	if origin.Type == "" || origin.Type == "other" {
		origin.Type = "user_action"
	}
	return context.WithValue(ctx, planChangeOriginContextKey{}, origin)
}

func planChangeOriginFromContext(ctx context.Context, workspacePath string) PlanChangeOrigin {
	origin, _ := ctx.Value(planChangeOriginContextKey{}).(PlanChangeOrigin)
	if strings.TrimSpace(origin.Type) == "" {
		origin.Type = "other"
	}
	// Tools that write the change log without the origin decorator (the
	// Builder's update_step_config, automatic fixers running as the owner)
	// still run under the authenticated user; record that person.
	if origin.UserID == "" {
		if userID, _ := ctx.Value(common.UserIDKey).(string); strings.TrimSpace(userID) != "" {
			origin.UserID = strings.TrimSpace(userID)
			if username, _ := ctx.Value(common.UsernameKey).(string); strings.TrimSpace(username) != "" && strings.TrimSpace(username) != "-" {
				origin.Username = strings.TrimSpace(username)
			}
		}
	}
	if origin.SessionID == "" {
		origin.SessionID = strings.TrimSpace(mcpexecutor.SessionIDFromContext(ctx))
	}

	return origin
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
		reviews[surface] = PlanDependencySurfaceReview{Disposition: disposition, Evidence: evidence}
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

func planRevisionParameters(parameters map[string]interface{}) map[string]interface{} {
	copy := make(map[string]interface{}, len(parameters))
	for k, v := range parameters {
		copy[k] = v
	}
	props := map[string]interface{}{}
	if existing, ok := parameters["properties"].(map[string]interface{}); ok {
		for k, v := range existing {
			props[k] = v
		}
	}
	props["expected_plan_revision"] = map[string]interface{}{"type": "string", "description": "Required for team workflows: plan_revision returned by get_workflow_team before proposing this change. A stale revision fails; read the latest plan and reconcile. Omit for non-team workflows."}
	copy["properties"] = props
	return copy
}
