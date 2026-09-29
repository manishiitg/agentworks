package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

// Function-call feedback uses the server-stamped call ID, never a caller-
// supplied session ID. A call can run in a continuing chat and launch child
// sessions, so neither a session prefix nor a session-only filter is safe.
func addFunctionCallPending(out map[string]interface{}, call *crewFunctionCall) {
	call.mu.Lock()
	active := !call.closed || call.acceptsLateLocked()
	id := call.ID
	call.mu.Unlock()
	pending := []map[string]interface{}{}
	if active {
		for _, input := range virtualtools.GetHumanFeedbackStore().PendingForOperation(id, time.Now()) {
			pending = append(pending, map[string]interface{}{
				"request_id":     input.UniqueID,
				"message":        input.MessageForUser,
				"options":        append([]string{}, input.Options...),
				"allow_feedback": input.AllowFeedback,
				"expires_at":     input.ExpiresAt,
			})
		}
	}
	out["pending_inputs"] = pending
}

func submitFunctionCallInput(call *crewFunctionCall, requestID, response string) error {
	// Every reply path (Crew and workflow, MCP and internal) normalizes here,
	// so an exact choice matches the same way whichever tool answered.
	requestID, response = strings.TrimSpace(requestID), strings.TrimSpace(response)
	call.mu.Lock()
	defer call.mu.Unlock()
	active := !call.closed || call.acceptsLateLocked()
	if !active {
		return fmt.Errorf("function call is no longer active")
	}
	return virtualtools.GetHumanFeedbackStore().SubmitResponseForOperationID(call.ID, requestID, response, time.Now())
}

func replyFunctionCallInput(w http.ResponseWriter, call *crewFunctionCall, requestID, response string) {
	err := submitFunctionCallInput(call, requestID, response)
	if err != nil {
		externalError(w, http.StatusConflict, "input_not_pending", err.Error())
		return
	}
	externalJSON(w, map[string]string{"call_id": call.ID, "request_id": requestID, "status": "submitted"})
}
