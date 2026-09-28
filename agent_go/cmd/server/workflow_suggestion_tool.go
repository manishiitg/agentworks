package server

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Suggestions append to the existing durable decision queue. The target and
// decision authority are fixed by the server, never supplied by the model.
func (api *StreamingAPI) registerWorkflowSuggestionTool(registrar definitionToolRegistrar, session, workspace string) error {
	return registrar.RegisterCustomTool("submit_workflow_suggestion", "Leave a suggestion for this workflow's owner to review in the human decisions panel. Available in Run mode and to read-only users. Does not change the workflow or authorize implementation. Submit only the user's requested suggestion.", map[string]interface{}{
		"type": "object", "additionalProperties": false,
		"properties": map[string]interface{}{
			"suggestion": map[string]interface{}{"type": "string", "description": "The requested improvement in plain words.", "maxLength": 4000},
			"reason":     map[string]interface{}{"type": "string", "description": "Optional short reason or example.", "maxLength": 4000},
			"step_id":    map[string]interface{}{"type": "string", "description": "Optional related workflow step ID.", "maxLength": 200},
		}, "required": []string{"suggestion"},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		claims := GetUserFromContext(ctx)
		if claims == nil || claims.UserID == "" {
			return "", fmt.Errorf("an authenticated workflow user is required to leave a suggestion")
		}
		level, manifest := workflowAccessForWorkspacePath(ctx, claims, workspace)
		if manifest == nil || level == WorkflowAccessNone || !userAllowedWorkflowID(claims, manifest.ID) {
			return "", fmt.Errorf("workflow access is required to leave a suggestion")
		}
		suggestion, _ := args["suggestion"].(string)
		reason, _ := args["reason"].(string)
		step, _ := args["step_id"].(string)
		suggestion = strings.TrimSpace(suggestion)
		if suggestion == "" || utf8.RuneCountInString(suggestion) > 4000 || utf8.RuneCountInString(reason) > 4000 || utf8.RuneCountInString(step) > 200 {
			return "", fmt.Errorf("provide a suggestion of up to 4000 characters, an optional reason up to 4000 characters, and a step ID up to 200 characters")
		}
		actor := claims.UserID
		if claims.ExecutionPrincipal != nil && claims.ExecutionPrincipal.AuditActor != "" {
			actor = claims.ExecutionPrincipal.AuditActor
		}
		input, err := createReportHumanInput(ctx, workspace, ReportHumanInputCreateRequest{
			Source: "user_suggestion", Priority: "medium", Question: "Review suggestion: " + suggestion,
			Context: strings.TrimSpace(reason), Evidence: strings.TrimSpace(step), CreatedBy: actor,
			CreatedByKind: "user", CreatedVia: "suggestion_tool", SessionID: session,
			Options: []ReportHumanInputOption{{ID: "approve", Title: "Accept suggestion", Description: "The owner can implement this in Builder."}, {ID: "reject", Title: "Decline suggestion"}}, AllowFreeText: true,
			// Suggestions carry no machine-generated implementation authority.
			ApplyContract: ReportHumanInputApplyContract{Mode: "no_change"},
		})
		if err != nil {
			return "", err
		}
		return marshalReportHumanInputToolResult("submitted_for_owner_review", input)
	}, "human_tools")
}

func requireSuggestionOwner(ctx context.Context, workspace string, input *ReportHumanInput) error {
	if input.Source != "user_suggestion" {
		return nil
	}
	claims := GetUserFromContext(ctx)
	// A Crew's suggestions are reviewed by the Crew's owner.
	if ref, ok := resolveCrewPath(ctx, GetUserIDFromContext(ctx), workspace); ok {
		if claims == nil || claims.Provider == "bot_route" || crewAccessFor(claims, ref) != crewAccessOwner {
			return fmt.Errorf("only the Crew's owner can review a user suggestion")
		}
		return nil
	}
	level, manifest := workflowAccessForWorkspacePath(ctx, claims, workspace)
	if claims == nil || claims.Provider == "bot_route" || manifest == nil || level != WorkflowAccessOwner || !userAllowedWorkflowID(claims, manifest.ID) {
		return fmt.Errorf("only a workflow owner can review a user suggestion")
	}
	return nil
}
