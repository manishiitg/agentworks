package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

// externalBuilderCall adapts the public tool surface to the existing builder
// runtime. The external router has already authorized workflow access. Session
// ownership and its workflow binding must additionally match, even for admins.
func (api *StreamingAPI) externalBuilderCall(w http.ResponseWriter, r *http.Request, name string, args map[string]interface{}, workflow DiscoveredWorkflow) {
	userID := strings.TrimSpace(GetUserIDFromContext(r.Context()))
	if userID == "" {
		externalError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}
	sessionID, err := externalBuilderString(args, "session_id")
	if err != nil || (sessionID != "" && sanitizeChatHistorySessionID(sessionID) != sessionID) {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "Invalid session_id")
		return
	}
	if name != "builder_chat" && sessionID == "" {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "session_id is required")
		return
	}
	claims := GetUserFromContext(r.Context())
	if claims.AccessToken != nil && sessionID != "" && !strings.HasPrefix(sessionID, accessTokenSessionPrefix(claims)) {
		externalError(w, http.StatusNotFound, "session_not_found", "This access token does not own that Builder session.")
		return
	}
	var active *ActiveSessionInfo
	var persisted bool
	if sessionID != "" {
		active, persisted, err = api.externalBuilderSession(r, workflow.WorkspacePath, sessionID)
		if err != nil {
			externalError(w, http.StatusNotFound, "session_not_found", "Builder session not found")
			return
		}
	}

	switch name {
	case "builder_chat":
		message, msgErr := externalBuilderString(args, "message")
		provider, providerErr := externalBuilderString(args, "provider")
		modelID, modelErr := externalBuilderString(args, "model_id")
		if msgErr != nil || providerErr != nil || modelErr != nil || message == "" {
			externalError(w, http.StatusBadRequest, "invalid_arguments", "message is required; message, provider, and model_id must be strings")
			return
		}
		if workflow.Manifest == nil || workflow.Manifest.ID == "" {
			externalError(w, http.StatusConflict, "workflow_unavailable", "Workflow manifest is unavailable")
			return
		}
		if sessionID == "" && claims.AccessToken == nil {
			sessionID, active, persisted, err = api.externalLatestBuilderSession(r, workflow.WorkspacePath)
			if err != nil {
				externalError(w, http.StatusBadGateway, "history_unavailable", "Unable to load builder conversation history")
				return
			}
		}
		if sessionID == "" {
			sessionID = uuid.NewString()
			if claims.AccessToken != nil {
				sessionID = accessTokenSessionPrefix(claims) + sessionID
			}
		}
		query := QueryRequest{
			Query: message, AgentMode: "workflow_phase", PhaseID: "workflow-builder",
			PresetQueryID: workflow.Manifest.ID, SelectedFolder: workflow.WorkspacePath,
			Provider: provider, ModelID: modelID,
		}
		if persisted {
			query.RestoredConversationSessionID = sessionID
		}
		body, marshalErr := json.Marshal(query)
		if marshalErr != nil {
			externalError(w, http.StatusInternalServerError, "encoding_failed", "Cannot encode builder request")
			return
		}
		forward := r.Clone(r.Context())
		forward.Method = http.MethodPost
		forward.URL.Path = "/api/query"
		forward.URL.RawQuery = ""
		forward.Header.Set("X-Session-ID", sessionID)
		forward.Header.Set("Content-Type", "application/json")
		forward.Body = io.NopCloser(bytes.NewReader(body))
		forward.ContentLength = int64(len(body))
		// handleQuery owns the normal asynchronous turn lifecycle and returns
		// its actual status/error. Do not wrap it in another background job.
		if claims.AccessToken != nil {
			api.watchAccessTokenSession(claims, sessionID, workflow.WorkspacePath, workflowAccessForManifest(claims, workflow.Manifest))
		}
		externalBuilderForward(w, forward, api.handleQuery)
		if claims.AccessToken != nil {
			// Close revocation/expiry races during setup, which may outlive the HTTP auth check.
			checkCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			allowed := accessTokenSessionAllowed(checkCtx, accessTokenSession{claims.AccessToken.ID, sessionID, workflow.WorkspacePath, workflowAccessForManifest(claims, workflow.Manifest)})
			cancel()
			if !allowed {
				api.cancelSessionRuntimeWork(sessionID, "access token no longer authorized", runtimePhaseCanceled)
			}
		}
	case "builder_status":
		since, sinceErr := externalBuilderInt(args, "since_index", -1, -1, int(^uint(0)>>1))
		limit, limitErr := externalBuilderInt(args, "limit", 50, 1, 200)
		if sinceErr != nil || limitErr != nil {
			externalError(w, http.StatusBadRequest, "invalid_arguments", "since_index must be an integer >= -1 and limit an integer between 1 and 200")
			return
		}
		page := storeevents.ForwardEventPage{GetEventsResult: storeevents.GetEventsResult{Events: []storeevents.Event{}, LastProcessedIndex: -1}}
		if api.eventStore != nil {
			page = api.eventStore.GetForwardEventPage(sessionID, since, limit)
		}
		response := map[string]interface{}{
			"session_id": sessionID, "events": page.Events, "has_more": page.HasMore,
			"last_processed_index": page.LastProcessedIndex, "cursor_reset": page.CursorReset,
			"first_available_index": page.FirstAvailableIndex,
			"history_available":     persisted, "events_available": page.Exists,
		}
		pending := virtualtools.GetHumanFeedbackStore().PendingForSession(sessionID, time.Now())
		response["pending_inputs"] = pending
		response["needs_user_input"] = len(pending) > 0
		if active != nil {
			response["session_status"] = active.Status
			response["can_steer"] = api.canSteerSession(sessionID)
			response["busy"] = api.isSessionBusy(sessionID)
			response["has_running_background_agents"] = api.bgAgentRegistry != nil && api.bgAgentRegistry.HasRunningAgents(sessionID)
			// Observe/collect rebuilds the runtime by copying the complete event
			// log. Use the existing coordinator projection for a bounded poll.
			if api.runtimeCoordinator != nil {
				if state, ok := api.runtimeCoordinator.Snapshot(sessionID); ok {
					response["runtime_state"] = state
					response["display_status"] = sessionDisplayStatusFromRuntime(state).Status
				}
			}
			response["needs_user_input"] = len(pending) > 0 || active.NeedsUserInput
			if active.NeedsUserInput {
				response["waiting_event_type"] = active.WaitingEventType
				response["waiting_message"] = active.WaitingMessage
			}
		} else {
			// A durable transcript alone cannot establish the outcome of the
			// last turn after a restart. Do not incorrectly report completion.
			response["session_status"] = "inactive"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	case "builder_reply_input":
		requestID, requestErr := externalBuilderString(args, "request_id")
		response, responseErr := externalBuilderString(args, "response")
		if requestErr != nil || responseErr != nil || requestID == "" || response == "" {
			externalError(w, http.StatusBadRequest, "invalid_arguments", "request_id and response are required strings")
			return
		}
		if err := virtualtools.GetHumanFeedbackStore().SubmitResponseForSession(sessionID, requestID, response, time.Now()); err != nil {
			if errors.Is(err, virtualtools.ErrFeedbackInvalidChoice) {
				externalError(w, http.StatusBadRequest, "invalid_input_choice", err.Error())
				return
			}
			externalError(w, http.StatusConflict, "input_not_pending", "Input request is not pending for this builder session")
			return
		}
		externalJSON(w, map[string]string{"session_id": sessionID, "request_id": requestID, "status": "submitted"})
	case "builder_cancel":
		if active == nil {
			externalError(w, http.StatusConflict, "session_inactive", "Builder session has no active runtime to cancel")
			return
		}
		forward := r.Clone(r.Context())
		forward.Header.Set("X-Session-ID", sessionID)
		externalBuilderForward(w, forward, api.handleCancelCurrentTurn)
	default:
		externalError(w, http.StatusBadRequest, "unknown_tool", "Unknown builder operation")
	}
}

func externalBuilderString(args map[string]interface{}, key string) (string, error) {
	v, exists := args[key]
	if !exists {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return strings.TrimSpace(s), nil
}

func externalBuilderInt(args map[string]interface{}, key string, fallback, min, max int) (int, error) {
	v, exists := args[key]
	if !exists {
		return fallback, nil
	}
	var n float64
	switch value := v.(type) {
	case float64:
		n = value
	case int:
		n = float64(value)
	case json.Number:
		var err error
		n, err = value.Float64()
		if err != nil {
			return 0, err
		}
	default:
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n || n < float64(min) || n >= float64(max)+1 {
		return 0, fmt.Errorf("%s is outside the permitted range", key)
	}
	return int(n), nil
}

func externalActiveBuilderMatches(session *ActiveSessionInfo, userID, workspacePath string) bool {
	return session != nil && session.UserID == userID && userID != "" &&
		normalizeChatHistoryWorkspacePath(session.WorkspacePath) == normalizeChatHistoryWorkspacePath(workspacePath) &&
		session.AgentMode == "workflow_phase" && session.PhaseID == "workflow-builder" &&
		!isScheduledSessionIdentity(session.SessionID, session.TriggeredBy)
}

// A transcript can mention arbitrary other workflows in its contents. Callers
// must read it with the workflow-scoped readers, never the general reader that
// falls back to the user's global history. Validate identity and phase as well
// as runtime metadata when present (ordinary API-model chats have no runtime).
func externalPersistedBuilderMatches(raw []byte, userID, workspacePath, sessionID string) bool {
	var record struct {
		SessionID string                   `json:"session_id"`
		UserID    string                   `json:"user_id"`
		AgentMode string                   `json:"agent_mode"`
		PhaseID   string                   `json:"phase_id"`
		Runtime   *ChatHistoryAgentRuntime `json:"runtime"`
	}
	if json.Unmarshal(raw, &record) != nil || record.SessionID != sessionID ||
		record.UserID != userID || userID == "" || record.PhaseID != "workflow-builder" ||
		isScheduledSessionIdentity(sessionID, "") {
		return false
	}
	return record.Runtime == nil || strings.TrimSpace(record.Runtime.WorkspacePath) == "" ||
		normalizeChatHistoryWorkspacePath(record.Runtime.WorkspacePath) == normalizeChatHistoryWorkspacePath(workspacePath)
}

func (api *StreamingAPI) externalBuilderSession(r *http.Request, workspacePath, sessionID string) (*ActiveSessionInfo, bool, error) {
	userID := strings.TrimSpace(GetUserIDFromContext(r.Context()))
	access, m := workflowAccessForWorkspacePath(r.Context(), GetUserFromContext(r.Context()), workspacePath)
	if m != nil && (access == WorkflowAccessOwner || access == WorkflowAccessWrite) {
		tabs, err := api.goalOwnerTabs(r.Context(), workflowBuilderScope{PresetID: m.ID, Workspace: workspacePath})
		if err != nil {
			return nil, false, err
		}
		for _, tab := range tabs {
			if tab.SessionID == sessionID {
				if !tab.CanWrite {
					return nil, false, fmt.Errorf("another person controls this tab")
				}
				if active, ok := api.getActiveSession(sessionID); ok {
					return active, false, nil
				}
				return nil, true, nil
			}
		}
	}

	if active, ok := api.getActiveSession(sessionID); ok {
		if externalActiveBuilderMatches(active, userID, workspacePath) {
			return active, false, nil
		}
		return nil, false, fmt.Errorf("session not found")
	}
	// Ownership known to the event store remains authoritative if the live
	// session was reaped. Never fall through to a different person's history.
	if api.eventStore != nil {
		if owner := api.eventStore.GetSessionOwner(sessionID); owner != "" && owner != userID {
			return nil, false, fmt.Errorf("session not found")
		}
	}
	// These readers stay inside this workflow's builder conversation folder.
	// ReadChatHistoryConversation also searches global user history and is not
	// appropriate for enforcing the external workflow boundary.
	raw, exists, err := readWorkflowScopedChatHistoryConversationDirect(sessionID, workspacePath)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		raw, exists, err = readWorkflowScopedChatHistoryConversationFromWorkspace(sessionID, workspacePath)
	}
	if err != nil {
		return nil, false, err
	}
	if !exists {
		return nil, false, fmt.Errorf("session not found")
	}
	if !externalPersistedBuilderMatches(raw, userID, workspacePath, sessionID) {
		return nil, false, fmt.Errorf("session not found")
	}
	return nil, true, nil
}

func (api *StreamingAPI) externalLatestBuilderSession(r *http.Request, workspacePath string) (string, *ActiveSessionInfo, bool, error) {
	userID := strings.TrimSpace(GetUserIDFromContext(r.Context()))
	var newest *ActiveSessionInfo
	api.activeSessionsMux.RLock()
	for _, session := range api.activeSessions {
		if externalActiveBuilderMatches(session, userID, workspacePath) && (newest == nil || session.LastActivity.After(newest.LastActivity)) {
			copy := *session
			newest = &copy
		}
	}
	api.activeSessionsMux.RUnlock()
	if newest != nil {
		return newest.SessionID, newest, false, nil
	}
	const pageSize = 100
	for offset := 0; ; offset += pageSize {
		if err := r.Context().Err(); err != nil {
			return "", nil, false, err
		}
		page, err := ListChatHistorySessionsByKind(userID, "chat", pageSize, offset, workspacePath)
		if err != nil {
			return "", nil, false, err
		}
		for _, session := range page {
			if session.UserID != userID {
				continue
			}
			active, persisted, err := api.externalBuilderSession(r, workspacePath, session.SessionID)
			if err == nil {
				return session.SessionID, active, persisted, nil
			}
		}
		if len(page) < pageSize {
			return "", nil, false, nil
		}
	}
}

const externalBuilderErrorLimit = 64 << 10

// Successful query responses pass straight through; only errors are buffered,
// with a fixed cap. This preserves the runtime's actual HTTP status while
// normalizing both its plain-text and JSON errors to the external API contract.
type externalBuilderResponse struct {
	target    http.ResponseWriter
	header    http.Header
	status    int
	body      bytes.Buffer
	truncated bool
}

func (w *externalBuilderResponse) Header() http.Header { return w.header }
func (w *externalBuilderResponse) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	if status >= 400 {
		return
	}
	for key, values := range w.header {
		w.target.Header()[key] = append([]string(nil), values...)
	}
	w.target.WriteHeader(status)
}
func (w *externalBuilderResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.status < 400 {
		return w.target.Write(data)
	}
	n := len(data)
	remaining := externalBuilderErrorLimit - w.body.Len()
	if len(data) > remaining {
		data = data[:remaining]
		w.truncated = true
	}
	_, _ = w.body.Write(data)
	return n, nil
}
func (w *externalBuilderResponse) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.status < 400 {
		if flusher, ok := w.target.(http.Flusher); ok {
			flusher.Flush()
		}
	}
}

func externalBuilderForward(w http.ResponseWriter, r *http.Request, handler http.HandlerFunc) {
	forward := &externalBuilderResponse{target: w, header: make(http.Header)}
	handler(forward, r)
	if forward.status == 0 {
		forward.WriteHeader(http.StatusOK)
	}
	if forward.status < 400 {
		return
	}
	code := "builder_failed"
	switch forward.status {
	case http.StatusBadRequest:
		code = "invalid_arguments"
	case http.StatusUnauthorized:
		code = "unauthorized"
	case http.StatusForbidden:
		code = "forbidden"
	case http.StatusNotFound:
		code = "session_not_found"
	case http.StatusConflict:
		code = "builder_conflict"
	case http.StatusTooManyRequests:
		code = "rate_limited"
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		code = "builder_unavailable"
	}
	message := strings.TrimSpace(forward.body.String())
	var payload struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(forward.body.Bytes(), &payload) == nil {
		var nested struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		var errorText string
		if json.Unmarshal(payload.Error, &nested) == nil && nested.Message != "" {
			message = nested.Message
			if externalBuilderErrorCode(nested.Code) {
				code = nested.Code
			}
		} else if json.Unmarshal(payload.Error, &errorText) == nil && errorText != "" {
			message = errorText
			if externalBuilderErrorCode(errorText) {
				code = errorText
			}
		}
		if payload.Message != "" {
			message = payload.Message
		}
	}
	if message == "" {
		message = http.StatusText(forward.status)
	}
	if forward.truncated {
		message += " (error message truncated)"
	}
	externalError(w, forward.status, code, message)
}

func externalBuilderErrorCode(s string) bool {
	if s == "" || len(s) > 100 {
		return false
	}
	for _, char := range s {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}
