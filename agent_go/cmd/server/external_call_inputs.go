package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

// externalCallQuestion retains the trusted session only on the server. The
// external caller sees a call ID and request ID, never an assistant session ID.
type externalCallQuestion struct {
	request virtualtools.HumanFeedbackRequest
}

func (api *StreamingAPI) externalCallQuestions(ctx context.Context, call *crewFunctionCall) []externalCallQuestion {
	call.mu.Lock()
	if call.terminalLocked() && !call.acceptsLateLocked() {
		call.mu.Unlock()
		return nil
	}
	createdAt := call.CreatedAt
	call.mu.Unlock()

	sessions := api.externalCallSessionIDs(ctx, call)
	questions := []externalCallQuestion{}
	now := time.Now()
	store := virtualtools.GetHumanFeedbackStore()
	for sessionID := range sessions {
		for _, request := range store.PendingForSession(sessionID, now) {
			if !createdAt.IsZero() && request.CreatedAt.Before(createdAt) {
				continue
			}
			// A shared conversation can have multiple calls. If another call
			// could have created this request, refuse to claim it for either.
			if api.externalCallQuestionAmbiguous(ctx, call, sessionID, request.CreatedAt) {
				continue
			}
			questions = append(questions, externalCallQuestion{request: request})
		}
	}
	return questions
}

func (api *StreamingAPI) externalCallSessionIDs(ctx context.Context, call *crewFunctionCall) map[string]bool {
	call.mu.Lock()
	runIDs := append([]string(nil), call.RunIDs...)
	if len(runIDs) == 0 && call.RunID != "" {
		runIDs = append(runIDs, call.RunID)
	}
	// Workflow Ask has its own built-in function marker, so FreeText alone
	// does not identify every call whose RunID is already a chat session.
	askSession := call.FreeText || (call.TargetKind == triggerCallerWorkflow && call.Function == crewFunctionAskName && call.TriggerID == "")
	userID, caller, target, triggerID := call.UserID, call.caller, call.target, call.TriggerID
	call.mu.Unlock()

	sessions := map[string]bool{}
	for _, runID := range runIDs {
		if askSession {
			sessions[runID] = true
			continue
		}
		// A trigger run ID is not a chat session ID. Resolve it through the
		// trusted run record; never accept a session supplied by the caller.
		state, err := api.readTriggerTargetRun(ctx, userID, caller, target, triggerID, runID)
		if err == nil && strings.TrimSpace(state.SessionID) != "" {
			sessions[state.SessionID] = true
		}
	}
	return sessions
}

func (api *StreamingAPI) externalCallQuestionAmbiguous(ctx context.Context, current *crewFunctionCall, sessionID string, questionAt time.Time) bool {
	current.mu.Lock()
	userID, targetKind, targetID := current.UserID, current.TargetKind, current.TargetID
	current.mu.Unlock()
	crewFunctionCalls.Lock()
	others := make([]*crewFunctionCall, 0, len(crewFunctionCalls.m))
	for _, candidate := range crewFunctionCalls.m {
		if candidate != current {
			others = append(others, candidate)
		}
	}
	crewFunctionCalls.Unlock()
	for _, candidate := range others {
		candidate.mu.Lock()
		couldOwn := candidate.UserID == userID && candidate.TargetKind == targetKind && candidate.TargetID == targetID && !candidate.CreatedAt.After(questionAt) && (!candidate.terminalLocked() || candidate.acceptsLateLocked() || !candidate.UpdatedAt.Before(questionAt))
		candidate.mu.Unlock()
		if couldOwn && api.externalCallSessionIDs(ctx, candidate)[sessionID] {
			return true
		}
	}
	return false
}

func (api *StreamingAPI) addExternalCallPendingInputs(ctx context.Context, call *crewFunctionCall, out map[string]interface{}) {
	call.mu.Lock()
	targetKind, targetID := call.TargetKind, call.TargetID
	call.mu.Unlock()
	var questions []externalCallQuestion
	if api.externalReplyCallTargetAllowed(ctx, GetUserFromContext(ctx), targetKind, targetID) {
		questions = api.externalCallQuestions(ctx, call)
	}
	items := make([]map[string]interface{}, 0, len(questions))
	for _, question := range questions {
		r := question.request
		items = append(items, map[string]interface{}{
			"request_id": r.UniqueID, "message": r.MessageForUser,
			"context": r.Context, "options": r.Options,
			"allow_feedback": r.AllowFeedback, "expires_at": r.ExpiresAt,
		})
	}
	out["pending_inputs"] = items
	out["needs_user_input"] = len(items) > 0
	if len(items) > 0 {
		out["next"] = "Ask the user to answer a pending_input, then call reply_function_call_input with this call_id, its request_id, and the response. Poll this call again afterward."
	}
}

func (api *StreamingAPI) externalReplyFunctionCallInput(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	callID, _ := args["call_id"].(string)
	requestID, _ := args["request_id"].(string)
	response, _ := args["response"].(string)
	call := lookupCrewFunctionCall(strings.TrimSpace(callID))
	if call == nil {
		externalError(w, http.StatusNotFound, "not_found", "Function call not found.")
		return
	}
	call.mu.Lock()
	owned := call.UserID == claims.UserID && call.CallerKind == triggerCallerUser
	targetKind, targetID := call.TargetKind, call.TargetID
	call.mu.Unlock()
	if !owned || !api.externalReplyCallTargetAllowed(r.Context(), claims, targetKind, targetID) {
		externalError(w, http.StatusNotFound, "not_found", "Function call not found.")
		return
	}
	for _, question := range api.externalCallQuestions(r.Context(), call) {
		if question.request.UniqueID != requestID {
			continue
		}
		err := virtualtools.GetHumanFeedbackStore().SubmitResponseForSessionCreatedAt(question.request.SessionID, requestID, response, time.Now(), question.request.CreatedAt)
		if errors.Is(err, virtualtools.ErrFeedbackInvalidChoice) {
			externalError(w, http.StatusBadRequest, "invalid_input_choice", err.Error())
			return
		}
		if err != nil {
			externalError(w, http.StatusConflict, "input_not_pending", "Input request is no longer pending for this call.")
			return
		}
		externalJSON(w, map[string]string{"call_id": call.ID, "request_id": requestID, "status": "submitted"})
		return
	}
	externalError(w, http.StatusConflict, "input_not_pending", "Input request is not pending for this call.")
}

func (api *StreamingAPI) externalReplyCallTargetAllowed(ctx context.Context, claims *UserClaims, kind, targetID string) bool {
	if claims == nil {
		return false
	}
	token := claims.AccessToken
	switch kind {
	case triggerCallerCrew:
		if token != nil && (!token.Allows("crews:run") || !token.AllowsCrew(targetID)) {
			return false
		}
		_, _, _, ok := api.externalCrewResolve(ctx, claims, targetID)
		return ok
	case triggerCallerWorkflow:
		if token != nil && (!token.Allows("runs:execute") || !token.AllowsWorkflow(targetID)) {
			return false
		}
		discovered, err := DiscoverWorkflowManifests(ctx)
		if err != nil {
			return false
		}
		for _, workflow := range filterWorkflowManifestsForUser(claims, discovered) {
			if workflow.Manifest != nil && workflow.Manifest.ID == targetID {
				access := workflowAccessForManifest(claims, workflow.Manifest)
				return access == WorkflowAccessOwner || access == WorkflowAccessWrite
			}
		}
	}
	return false
}
