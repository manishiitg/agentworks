package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

// Workflow ask: free text for a workflow goes to its assistant — the
// workflow's own Run-mode chat, which knows the plan, routes and variables —
// never straight into a run. The assistant answers questions ("what can you
// do?", "what did the last run do?") and, when asked to run something,
// starts the right route with the right variables itself and reports the
// outcome. Shared-owner projects use the caller's visible main workflow chat;
// other callers keep a separate continuing conversation. (Sending
// free text straight into a run is what let the PR-review gate ignore the
// requested PR and fall back to a saved PR_NUMBER.)

const workflowAskCreatedBy = "platform (workflow assistant)"

// workflowAskTurn, when set (tests), replaces the real assistant turn.
var workflowAskTurn func(api *StreamingAPI, ctx context.Context, reqMap map[string]interface{}, sessionID, userID string) (internalSessionTurnResult, error)

func workflowAskFunction() crewFunction {
	return crewFunction{
		Name:        crewFunctionAskName,
		Description: "Ask this workflow's assistant anything in free text: what it can do, what its runs did, or to run something (it picks the route and variables itself and reports the outcome). The answer is its final reply. Prefer a typed function when one fits.",
		InputSchema: map[string]interface{}{"type": "object", "required": []interface{}{"message"}, "properties": map[string]interface{}{
			"message": map[string]interface{}{"type": "string", "description": "Self-contained question or request; include every value a run needs (e.g. repo and PR number). The assistant does not see this conversation."},
		}},
		ResultSchema: map[string]interface{}{"type": "object", "required": []interface{}{"answer"}, "properties": map[string]interface{}{
			"answer": map[string]interface{}{"type": "string"},
		}},
		CreatedBy: workflowAskCreatedBy,
	}
}

func isWorkflowAsk(target triggerTarget, fn crewFunction) bool {
	return target.Kind == triggerCallerWorkflow && fn.Name == crewFunctionAskName && fn.CreatedBy == workflowAskCreatedBy
}

// workflowAskSessionID is the caller's continuing conversation with the
// workflow's assistant: stable per workflow and caller.
func workflowAskSessionID(workflowID string, caller triggerCaller, userID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(workflowID) + "\x1f" + strings.ToLower(strings.TrimSpace(caller.Type)) + "\x1f" + strings.TrimSpace(caller.ProfileID) + "\x1f" + strings.TrimSpace(caller.ID) + "\x1f" + strings.TrimSpace(userID)))
	return "wfask-" + hex.EncodeToString(sum[:])[:24]
}

func workflowAskMessage(caller triggerLinkCaller, message string) string {
	kind := "Crew"
	switch caller.Stamp.Type {
	case triggerCallerWorkflow:
		kind = "workflow"
	case triggerCallerUser:
		kind = "external connection"
	}
	return fmt.Sprintf("[Asked by the %s %q through `ask`. This conversation is yours and that caller's; answer in a clear, self-contained final reply, which is returned to it. If it asks you to run something, start the right route with the values it needs (run_full_workflow variables for declared variables, human_inputs otherwise) (never rely on a saved value for per-run data such as a PR number), wait for the outcome and report it, including any step that skipped and why. If a required value is missing, say which instead of running. You cannot change the workflow here: if the caller reports a problem or asks for a change, record it for the owner with submit_workflow_suggestion and say you did.]\n\n%s", kind, caller.Label, strings.TrimSpace(message))
}

// runWorkflowAsk sends the question to the workflow assistant and settles the
// call with its final reply. It runs in the background; the call's done
// channel carries the result.
//
// As with a Crew call, the timeout counts from the assistant's last sign of
// life and only releases the caller: the turn keeps running (up to
// crewFunctionHardCap) and its reply is delivered as a late answer.
func (api *StreamingAPI) runWorkflowAsk(call *crewFunctionCall, target triggerTarget, caller triggerLinkCaller, message string, timeout time.Duration) {
	hardCap := crewFunctionHardCap(timeout)
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: call.UserID}), hardCap)
	defer cancel()
	ctx = virtualtools.WithFeedbackOperation(ctx, call.ID)
	manifest, exists, readErr := ReadWorkflowManifest(ctx, target.Path)
	if readErr != nil || !exists || manifest == nil || target.Manifest == nil || manifest.ID != target.Manifest.ID {
		call.finish("failed", nil, "workflow is unavailable or access denied")
		return
	}
	access := workflowAccessForManifest(GetUserFromContext(ctx), manifest)
	if access != WorkflowAccessOwner && access != WorkflowAccessWrite {
		call.finish("failed", nil, "workflow is unavailable or access denied")
		return
	}
	sessionID := workflowAskSessionID(manifest.ID, caller.Stamp, call.UserID)
	sharedOwner := projectsShareOwner(api.productSchedules.projectCallerOwners(ctx, call.UserID, caller), manifest.effectiveOwners())
	if sharedOwner {
		// Workflow chats are private to the executing user. Never restore
		// another owner's transcript or substitute that owner as principal.
		sessionID = api.userWorkflowChat(ctx, call.UserID, services.ChannelRoute{WorkflowID: manifest.ID, WorkspacePath: target.Path})
		if sessionID == "" {
			sum := sha256.Sum256([]byte(manifest.ID + "\x1f" + call.UserID))
			sessionID = "wfmain-" + hex.EncodeToString(sum[:])[:24]
		}
	}
	query := QueryRequest{
		Query: workflowAskMessage(caller, message), AgentMode: "workflow_phase", PhaseID: "workflow-builder",
		PresetQueryID: manifest.ID, SelectedFolder: target.Path,
		PinRunMode: true, TriggeredBy: "external", TriggeredByLabel: "Asked by " + caller.Label, SessionTitle: "Asked by " + caller.Label,
		ExecutionOptions: &ExecutionOptions{WorkshopMode: "run"},
	}
	if api.workflowAskSessionExists(sessionID, target.Path) {
		query.RestoredConversationSessionID = sessionID
	}
	reqMap, err := queryRequestToMap(query)
	if err != nil {
		call.finish("failed", nil, "cannot build the question: "+err.Error())
		return
	}
	call.mu.Lock()
	call.Status = "running"
	call.RunID, call.RunIDs = sessionID, []string{sessionID}
	call.mu.Unlock()
	call.persist()
	turnDone := make(chan struct{})
	go api.watchAskActivity(call, sessionID, fmt.Sprintf("the workflow assistant of %q", call.TargetLabel), timeout, turnDone)
	var result internalSessionTurnResult
	if workflowAskTurn != nil {
		result, err = workflowAskTurn(api, ctx, reqMap, sessionID, call.UserID)
	} else {
		result, err = api.startSessionInternalWithResult(ctx, reqMap, sessionID, call.UserID, nil)
	}
	close(turnDone)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			call.settle("failed", nil, fmt.Sprintf("the workflow assistant was still not done after %s", hardCap))
			return
		}
		call.settle("failed", nil, fmt.Sprintf("the workflow assistant did not answer: %v", err))
		return
	}
	answer := strings.TrimSpace(result.FinalResponse)
	if answer == "" {
		call.settle("failed", nil, "the workflow assistant finished without a reply")
		return
	}
	call.settle("completed", map[string]interface{}{"answer": truncateTriggerTargetResult(answer)}, "")
}

// watchAskActivity releases the caller once who (the workflow assistant, a
// Crew) shows no sign of life (progress or session events) for timeout. The
// turn goes on.
func (api *StreamingAPI) watchAskActivity(call *crewFunctionCall, sessionID, who string, timeout time.Duration, turnDone <-chan struct{}) {
	lastSign := time.Now()
	ticker := time.NewTicker(call.poll)
	defer ticker.Stop()
	for {
		select {
		case <-turnDone:
			return
		case <-call.done:
			return
		case <-ticker.C:
		}
		call.mu.Lock()
		if call.UpdatedAt.After(lastSign) {
			lastSign = call.UpdatedAt
		}
		call.mu.Unlock()
		if at := api.crewFunctionLastEventAt(sessionID); at.After(lastSign) {
			lastSign = at
		}
		if time.Since(lastSign) > timeout {
			call.timeOut(fmt.Sprintf("no activity from %s for %s; stopped waiting. It is still working: its answer is sent to you automatically", who, timeout))
			return
		}
	}
}

// workflowAskSessionExists reports whether the caller's assistant
// conversation already exists (live or in this workflow's saved history), so
// the next ask resumes it.
func (api *StreamingAPI) workflowAskSessionExists(sessionID, workspacePath string) bool {
	if _, ok := api.getActiveSession(sessionID); ok {
		return true
	}
	if _, exists, err := readWorkflowScopedChatHistoryConversationDirect(sessionID, workspacePath); err == nil && exists {
		return true
	}
	_, exists, err := readWorkflowScopedChatHistoryConversationFromWorkspace(sessionID, workspacePath)
	return err == nil && exists
}
