package server

import (
	"net/http"
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

func replyFunctionCallInput(w http.ResponseWriter, call *crewFunctionCall, requestID, response string) {
	call.mu.Lock()
	active := !call.closed || call.acceptsLateLocked()
	id := call.ID
	if !active {
		call.mu.Unlock()
		externalError(w, http.StatusConflict, "input_not_pending", "Function call is no longer active.")
		return
	}
	err := virtualtools.GetHumanFeedbackStore().SubmitResponseForOperationID(id, requestID, response, time.Now())
	call.mu.Unlock()
	if err != nil {
		externalError(w, http.StatusConflict, "input_not_pending", err.Error())
		return
	}
	externalJSON(w, map[string]string{"call_id": id, "request_id": requestID, "status": "submitted"})
}
