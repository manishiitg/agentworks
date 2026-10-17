package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// Ask is an adapter onto the website's minimal project-chat service. The
// conversation registry, submission journal and turn queue retain ownership;
// no messaging inbox or separate execution policy is created here.
func (api *StreamingAPI) externalWorkspaceConversation(w http.ResponseWriter, r *http.Request, action string, args map[string]any, project crewProjectBinding, manifest productProjectManifest) {
	claims := GetUserFromContext(r.Context())
	if token := claims.AccessToken; token != nil && manifest.Product != "code" {
		scope := "crews:read"
		if action == "ask" {
			scope = "crews:run"
		}
		if !token.Allows(scope) {
			externalError(w, 403, "insufficient_scope", "This connection does not allow that conversation action.")
			return
		}
	}
	if manifest.Product == "code" && codeProjectMode(r.Context(), project.Binding.WorkspacePath) == "local" {
		externalError(w, 400, "local_project", "This project runs on your computer. Open its chat there; remote use is not available yet.")
		return
	}
	chats, err := api.externalCodeRunChats(r.Context(), claims.UserID, manifest.ID, project.Binding, manifest.Product)
	if err != nil {
		externalFailure(w, err)
		return
	}
	chatID := strings.TrimSpace(externalArg(args, "chat_id"))
	if chatID == "" {
		chatID = externalCallerTabName(r.Context(), claims)
	}
	chat := findCodeChat(chats, chatID)
	if chat == nil && action == "ask" && externalArg(args, "chat_id") == "" {
		id, status, message := api.externalCodeRunOpenTab(r, claims, project.Binding, manifest.ID, chats, chatID, manifest.Product)
		if id == "" {
			externalError(w, status, "chat_unavailable", message)
			return
		}
		chats, err = api.externalCodeRunChats(r.Context(), claims.UserID, manifest.ID, project.Binding, manifest.Product)
		if err != nil {
			externalFailure(w, err)
			return
		}
		chat = findCodeChat(chats, id)
	}
	if chat == nil {
		externalError(w, 404, "chat_not_found", "Choose one of your chats with action=chats, or ask without a chat_id to open your caller's own tab.")
		return
	}
	after := externalInt(args, "after", 0)
	out := map[string]any{"workspace_id": manifest.Product + ":" + manifest.ID, "chat_id": chat.ID}
	queryID := ""
	if action == "ask" {
		message := strings.TrimSpace(externalArg(args, "message"))
		if message == "" || len([]rune(message)) > codeChatMessageMaxRune {
			externalError(w, 400, "invalid_arguments", "Give a message within the chat size limit; put longer detail in a project file.")
			return
		}
		_, after, _ = codeChatTranscript(r.Context(), claims.UserID, chat.SessionID, project.Binding.WorkspacePath, 0, 0)
		body, _ := json.Marshal(AgentProfileChatRequest{Message: message, ConversationKey: chat.Key})
		req := httptest.NewRequest(http.MethodPost, "/api/agent-profiles/"+manifest.Product+"/query", bytes.NewReader(body)).WithContext(r.Context())
		req = mux.SetURLVars(req, map[string]string{"id": manifest.Product})
		req.Header.Set("Content-Type", "application/json")
		if submission := externalArg(args, "submission_id"); submission != "" {
			req.Header.Set("Idempotency-Key", "workspace:"+manifest.Product+":"+manifest.ID+":"+submission)
		}
		recorder := httptest.NewRecorder()
		api.handleAgentProfileChatQuery(recorder, req)
		if recorder.Code >= 300 {
			externalScheduleRespond(w, recorder.Code, recorder.Body.Bytes())
			return
		}
		var receipt map[string]any
		if json.Unmarshal(recorder.Body.Bytes(), &receipt) == nil {
			for key, value := range receipt {
				out[key] = value
			}
			queryID, _ = receipt["query_id"].(string)
		}
	}
	deadline := time.Now().Add(externalCrewWait(args))
	for {
		items, last, readErr := codeChatTranscript(r.Context(), claims.UserID, chat.SessionID, project.Binding.WorkspacePath, after, 100)
		execution := api.externalWorkspaceTurnExecution(chat.SessionID, queryID)
		working := api.sessionTurnRunning(chat.SessionID) || execution != nil && execution.Status == trackedExecutionStatusRunning
		if !working {
			if failure := execution; failure != nil && failure.Status == trackedExecutionStatusFailed {
				out["status"], out["working"] = "failed", false
				out["error"] = map[string]any{"code": "turn_failed", "message": "The conversation could not complete. Try again, or ask a Builder to check the run logs."}
				out["query_id"], out["messages"], out["next_after"] = failure.ExecutionID, items, max(after, last)
				externalJSON(w, out)
				return
			}
		}
		if readErr != nil && action != "ask" {
			externalFailure(w, readErr)
			return
		}
		answered := false
		// Native providers use their own turn IDs. The accepted query's
		// completed execution connects its saved reply to this ask.
		completed := execution != nil && execution.Status == trackedExecutionStatusCompleted && !working
		for _, item := range items {
			if item.Role == "assistant" && (queryID == "" || item.Turn == queryID || completed) {
				answered = true
			}
		}
		if answered || !time.Now().Before(deadline) || r.Context().Err() != nil || action == "read_chat" && !working {
			out["messages"], out["next_after"], out["working"] = items, max(after, last), working
			if action == "ask" && answered {
				out["status"] = "answered"
			}
			out["next"] = "Read more with workspace action=read_chat, this workspace_id and chat_id, after=next_after and wait_seconds=25."
			externalJSON(w, out)
			return
		}
		select {
		case <-r.Context().Done():
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// A rejected launch may have no transcript. Use the authoritative execution
// record so an empty chat is not reported as a successful completed turn.
func (api *StreamingAPI) externalWorkspaceTurnExecution(sessionID, queryID string) *TrackedWorkflowExecution {
	api.trackedWorkflowExecutionsMux.Lock()
	defer api.trackedWorkflowExecutionsMux.Unlock()
	var latest *TrackedWorkflowExecution
	for _, execution := range api.trackedWorkflowExecutions {
		if execution != nil && execution.SessionID == sessionID && (queryID == "" || execution.ExecutionID == queryID) && (latest == nil || execution.StartedAt.After(latest.StartedAt)) {
			latest = execution
		}
	}
	return cloneTrackedExecution(latest)
}
