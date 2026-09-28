package server

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const crewSuggestionToolName = "submit_crew_suggestion"

// registerCrewSuggestionTool lets someone using another user's Crew leave a
// change request for its owner, as submit_workflow_suggestion does for a
// workflow in Run mode. It lands in the Crew's decisions store (its own
// db/db.sqlite), which only the owner can read and answer. The Crew and its
// owner come from the session, never from the model, and accepting a
// suggestion changes nothing by itself: the owner makes the change.
// actorID is the non-owner the suggestion is from: the reader in their own
// Crew chat, or the guest caller on a turn working for them.
func (api *StreamingAPI) registerCrewSuggestionTool(registrar definitionToolRegistrar, actorID, sessionID, workspacePath string) error {
	return registrar.RegisterCustomTool(crewSuggestionToolName, "Leave a suggestion for this Crew's owner: something the user wants changed in how the Crew works (its role, instructions, skills, functions, schedules or output). The owner reviews it in the Crew's Suggestions view. It does not change the Crew. Submit only what the user asked to suggest, in their words.", map[string]interface{}{
		"type": "object", "additionalProperties": false,
		"properties": map[string]interface{}{
			"suggestion": map[string]interface{}{"type": "string", "description": "The requested change in plain words.", "maxLength": 4000},
			"reason":     map[string]interface{}{"type": "string", "description": "Optional short reason or example.", "maxLength": 4000},
			"about":      map[string]interface{}{"type": "string", "description": "Optional part of the Crew it concerns, e.g. a function or schedule name.", "maxLength": 200},
		}, "required": []string{"suggestion"},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		// The suggestion is from the non-owner this session works for, never
		// from whoever the turn happens to run as (a guest turn runs as the
		// owner).
		claims := GetUserFromContext(ctx)
		if claims == nil || sanitizeUserIDForPath(claims.UserID) != sanitizeUserIDForPath(actorID) {
			claims = &UserClaims{UserID: actorID}
			if record := directoryUserFor(actorID, "", ""); record != nil {
				claims.Username, claims.Email = record.Username, record.Email
			}
		}
		suggestion, _ := args["suggestion"].(string)
		reason, _ := args["reason"].(string)
		about, _ := args["about"].(string)
		input, err := submitCrewSuggestion(ctx, claims, workspacePath, sessionID, suggestion, reason, about)
		if err != nil {
			return "", err
		}
		return marshalReportHumanInputToolResult("submitted_for_owner_review", input)
	}, "human_tools")
}

// submitCrewSuggestion records a suggestion for a Crew's owner from another
// user of the Crew. The Crew chat tool and the external suggest_crew_change
// tool share it.
func submitCrewSuggestion(ctx context.Context, claims *UserClaims, crewPath, sessionID, suggestion, reason, about string) (*ReportHumanInput, error) {
	if claims == nil || claims.UserID == "" {
		return nil, fmt.Errorf("a signed-in user is required to leave a suggestion")
	}
	ref, ok := resolveCrewPath(ctx, claims.UserID, crewPath)
	if !ok {
		return nil, fmt.Errorf("not a Crew")
	}
	switch crewAccessFor(claims, ref) {
	case crewAccessOwner:
		return nil, fmt.Errorf("you own this Crew: change it directly instead of leaving a suggestion")
	case crewAccessNone:
		return nil, fmt.Errorf("Crew access is required to leave a suggestion")
	}
	suggestion = strings.TrimSpace(suggestion)
	if suggestion == "" || utf8.RuneCountInString(suggestion) > 4000 || utf8.RuneCountInString(reason) > 4000 || utf8.RuneCountInString(about) > 200 {
		return nil, fmt.Errorf("provide a suggestion of up to 4000 characters, an optional reason up to 4000 characters, and an optional topic up to 200 characters")
	}
	actor := claims.UserID
	if claims.ExecutionPrincipal != nil && claims.ExecutionPrincipal.AuditActor != "" {
		actor = claims.ExecutionPrincipal.AuditActor
	}
	return createReportHumanInput(ctx, ref.Root, ReportHumanInputCreateRequest{
		Source: "user_suggestion", Priority: "medium", Question: "Review suggestion: " + suggestion,
		Context: strings.TrimSpace(reason), Evidence: strings.TrimSpace(about), CreatedBy: actor,
		CreatedByKind: "user", CreatedVia: "suggestion_tool", SessionID: sessionID,
		Options: []ReportHumanInputOption{{ID: "approve", Title: "Accept suggestion", Description: "You can make this change in the Crew's builder chat."}, {ID: "reject", Title: "Decline suggestion"}}, AllowFreeText: true,
		ApplyContract: ReportHumanInputApplyContract{Mode: "no_change"},
	})
}

// crewFunctionResultOnlyRegistrar admits only the tools a Crew needs to answer
// a function call it received; a guest turn gets these and no way to call
// other Crews or change functions.
type crewFunctionResultOnlyRegistrar struct{ definitionToolRegistrar }

var crewFunctionResultTools = map[string]bool{"report_function_progress": true, "return_function_result": true}

func (r crewFunctionResultOnlyRegistrar) RegisterCustomTool(name, description string, params map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error), category string) error {
	if !crewFunctionResultTools[name] {
		return nil
	}
	return r.definitionToolRegistrar.RegisterCustomTool(name, description, params, exec, category)
}

func (r crewFunctionResultOnlyRegistrar) RegisterCustomToolWithTimeout(name, description string, params map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, category string) error {
	if !crewFunctionResultTools[name] {
		return nil
	}
	return r.definitionToolRegistrar.RegisterCustomToolWithTimeout(name, description, params, exec, timeout, category)
}
