package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	agentEvents "github.com/manishiitg/mcpagent/events"

	storeEvents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

const restoredBuilderConversationMessageLimit = 12
const restoredBuilderStepSummaryLimit = 1600
const workflowBuilderConversationDateLayout = "2006-01-02"

// Automation and cross-owner assistant conversations are not the main
// workflow chat, even when they use the workflow-builder phase.
func separateWorkflowConversation(sessionID string) bool {
	return strings.HasPrefix(sessionID, "wfask-") || strings.HasPrefix(sessionID, "schedule-")
}

type workflowBuilderSessionResponse struct {
	Revision           uint64            `json:"revision,omitempty"`
	Success            bool              `json:"success"`
	Source             string            `json:"source"`
	SessionID          string            `json:"session_id,omitempty"`
	PhaseID            string            `json:"phase_id,omitempty"`
	Status             string            `json:"status"`
	DisplayStatus      string            `json:"display_status,omitempty"`
	PresetQueryID      string            `json:"preset_query_id,omitempty"`
	WorkspacePath      string            `json:"workspace_path,omitempty"`
	WorkflowName       string            `json:"workflow_name,omitempty"`
	UpdatedAt          string            `json:"updated_at,omitempty"`
	ConversationPath   string            `json:"conversation_path,omitempty"`
	Events             []json.RawMessage `json:"events"`
	Total              int               `json:"total"`
	LastProcessedIndex int               `json:"last_processed_index"`
}

type builderConversationLog struct {
	Revision  uint64 `json:"revision,omitempty"`
	SessionID string `json:"session_id"`
	// UserID makes the workflow-scoped builder transcript private to the
	// account that created it. The workflow itself can be shared read-only;
	// that must never imply permission to restore another reader/owner's chat.
	UserID              string                       `json:"user_id,omitempty"`
	PhaseID             string                       `json:"phase_id"`
	UpdatedAt           string                       `json:"updated_at"`
	ConversationHistory []builderConversationMessage `json:"conversation_history"`
}

type builderConversationMessage struct {
	Role  string                    `json:"Role"`
	Parts []builderConversationPart `json:"Parts"`
}

type builderConversationPart struct {
	Text string `json:"Text"`
}

type stepExecutionConversationLog struct {
	ConversationHistory []builderConversationMessage `json:"conversation_history"`
	ToolCalls           []stepExecutionToolCall      `json:"tool_calls"`
}

type stepExecutionToolCall struct {
	ToolName    string `json:"tool_name"`
	Args        string `json:"args"`
	Result      string `json:"result"`
	StepID      string `json:"step_id"`
	Timestamp   string `json:"timestamp"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
}

type restoredStepSummary struct {
	Path      string
	StepID    string
	Status    string
	Message   string
	UpdatedAt time.Time
}

// handleGetWorkflowBuilderSession is the backend source of truth for restoring
// the Workflow Builder chat. It prefers a live EventStore session and only falls
// back to durable builder conversation files when no live session matches.
func (api *StreamingAPI) handleGetWorkflowBuilderSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	presetQueryID := strings.TrimSpace(r.URL.Query().Get("preset_query_id"))
	workspacePath := strings.Trim(strings.TrimSpace(r.URL.Query().Get("workspace_path")), "/")
	if workspacePath == "" && presetQueryID != "" {
		if resolved, err := api.resolveWorkspacePathFromPreset(r.Context(), presetQueryID); err == nil {
			workspacePath = strings.Trim(strings.TrimSpace(resolved), "/")
		}
	}

	if presetQueryID == "" && workspacePath == "" {
		http.Error(w, "preset_query_id or workspace_path is required", http.StatusBadRequest)
		return
	}
	if access, _ := workflowAccessForWorkspacePath(r.Context(), GetUserFromContext(r.Context()), workspacePath); access == WorkflowAccessNone {
		http.Error(w, "workflow access denied", http.StatusForbidden)
		return
	}

	if live := api.findLiveWorkflowBuilderSession(r.Context(), presetQueryID, workspacePath); live != nil {
		_ = json.NewEncoder(w).Encode(live)
		return
	}

	restored, err := api.restoreLatestBuilderConversation(r.Context(), presetQueryID, workspacePath)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to restore builder conversation: %v", err), http.StatusInternalServerError)
		return
	}
	if restored != nil {
		_ = json.NewEncoder(w).Encode(restored)
		return
	}

	_ = json.NewEncoder(w).Encode(workflowBuilderSessionResponse{
		Success:            true,
		Source:             "none",
		Status:             "idle",
		PresetQueryID:      presetQueryID,
		WorkspacePath:      workspacePath,
		WorkflowName:       workflowNameFromWorkspacePath(workspacePath),
		Events:             []json.RawMessage{},
		LastProcessedIndex: -1,
	})
}

func (api *StreamingAPI) findLiveWorkflowBuilderSession(ctx context.Context, presetQueryID, workspacePath string) *workflowBuilderSessionResponse {
	currentUserID := GetUserIDFromContext(ctx)

	var chosen *ActiveSessionInfo
	api.activeSessionsMux.RLock()
	for _, session := range api.activeSessions {
		if session == nil || session.AgentMode != "workflow_phase" || separateWorkflowConversation(session.SessionID) || session.SessionKind != "" || session.ParentSessionID != "" {
			continue
		}
		if session.UserID != "" && session.UserID != currentUserID {
			continue
		}
		if !workflowBuilderSessionMatches(session, presetQueryID, workspacePath) {
			continue
		}
		if chosen == nil || session.LastActivity.After(chosen.LastActivity) {
			copySession := *session
			chosen = &copySession
		}
	}
	api.activeSessionsMux.RUnlock()

	if chosen == nil {
		return nil
	}

	enriched := api.buildActiveSessionInfoSummary(chosen)
	if enriched != nil {
		chosen = enriched
	}

	rawEvents := []json.RawMessage{}
	lastProcessedIndex := -1
	if api.eventStore != nil {
		result := api.eventStore.GetEvents(chosen.SessionID, storeEvents.GetEventsOptions{SinceIndex: -1})
		rawEvents = marshalBuilderEvents(result.Events)
		lastProcessedIndex = result.LastProcessedIndex
	}
	if lastProcessedIndex < 0 && len(rawEvents) > 0 {
		lastProcessedIndex = len(rawEvents) - 1
	}

	return &workflowBuilderSessionResponse{
		Success:            true,
		Source:             "live",
		SessionID:          chosen.SessionID,
		PhaseID:            "workflow-builder",
		Status:             coalesceString(chosen.Status, "running"),
		DisplayStatus:      "busy",
		PresetQueryID:      coalesceString(chosen.PresetQueryID, presetQueryID),
		WorkspacePath:      coalesceString(chosen.WorkspacePath, workspacePath),
		WorkflowName:       coalesceString(chosen.WorkflowName, chosen.WorkflowLabel, chosen.PresetName, workflowNameFromWorkspacePath(coalesceString(chosen.WorkspacePath, workspacePath))),
		UpdatedAt:          chosen.LastActivity.Format(time.RFC3339Nano),
		Events:             rawEvents,
		Total:              len(rawEvents),
		LastProcessedIndex: lastProcessedIndex,
	}
}

func workflowBuilderSessionMatches(session *ActiveSessionInfo, presetQueryID, workspacePath string) bool {
	if presetQueryID != "" && strings.TrimSpace(session.PresetQueryID) == presetQueryID {
		return true
	}
	if workspacePath != "" && strings.Trim(strings.TrimSpace(session.WorkspacePath), "/") == workspacePath {
		return true
	}
	return false
}

func workflowBuilderConversationLogPath(workspacePath, sessionID string, timestamp time.Time) string {
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	cleanWorkspacePath := strings.Trim(strings.TrimSpace(workspacePath), "/")
	return filepath.ToSlash(filepath.Join(
		cleanWorkspacePath,
		"builder",
		"conversation",
		timestamp.Format(workflowBuilderConversationDateLayout),
		fmt.Sprintf("session-%s-conversation.json", sessionID),
	))
}

// workflowBuilderOwnedConversationLogPath keeps Builder transcripts inside the
// workflow (so native coding CLIs can continue them by path) while separating
// each account's private history. The legacy helper above remains for reading
// and migrating pre-partitioned transcripts.
// stableBuilderConversationLogPath keeps a builder chat in one file: the
// session's existing file for this owner when there is one, and a path dated
// today only for a new chat. Writers used today's date whenever they lacked
// the path, so a continued chat started a second file every day it was used
// (RTS: one session had a 56 MB 09-16 file and a 115 MB 09-18 copy).
func stableBuilderConversationLogPath(ctx context.Context, workspacePath, userID, sessionID string) string {
	ownerFolder := "/users/" + sanitizeUserIDForPath(userID) + "/"
	if existing, found, err := findWorkflowBuilderConversationPathForSession(ctx, userID, sessionID, workspacePath); err == nil && found && strings.Contains(filepath.ToSlash(existing), ownerFolder) {
		return filepath.ToSlash(existing)
	}
	return workflowBuilderOwnedConversationLogPath(workspacePath, userID, sessionID, time.Now())
}

func workflowBuilderOwnedConversationLogPath(workspacePath, userID, sessionID string, timestamp time.Time) string {
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	cleanWorkspacePath := strings.Trim(strings.TrimSpace(workspacePath), "/")
	return filepath.ToSlash(filepath.Join(
		cleanWorkspacePath,
		"builder",
		"conversation",
		"users",
		sanitizeUserIDForPath(userID),
		timestamp.Format(workflowBuilderConversationDateLayout),
		fmt.Sprintf("session-%s-conversation.json", sessionID),
	))
}

func effectiveBuilderConversationOwner(requestUserID, existingUserID string) string {
	requestUserID = strings.TrimSpace(requestUserID)
	existingUserID = strings.TrimSpace(existingUserID)
	if requestUserID != "" && requestUserID != "default" {
		return requestUserID
	}
	if existingUserID != "" && existingUserID != "default" {
		return existingUserID
	}
	return "default"
}

func (api *StreamingAPI) restoreLatestBuilderConversation(ctx context.Context, presetQueryID, workspacePath string) (*workflowBuilderSessionResponse, error) {
	return api.restoreLatestBuilderConversationLimited(ctx, presetQueryID, workspacePath, 0)
}

// slackBuilderRefreshLimit is how many of the most recently updated saved conversations a bot
// lookup refreshes from their CLI transcripts. The lookup only needs the newest one's session id;
// refreshing all 38 of a busy workflow took 70 s and delayed the Slack reply by ~90 s (Confida,
// 2026-09-30).
const slackBuilderRefreshLimit = 6

// restoreLatestBuilderConversationLimited is restoreLatestBuilderConversation that refreshes only the
// refreshLimit most recently updated conversations from their native transcripts (0 = all). The
// saved snapshot of an older conversation cannot be newer than a recent one's live transcript
// unless it is also among the most recent, so the newest is still found.
func (api *StreamingAPI) restoreLatestBuilderConversationLimited(ctx context.Context, presetQueryID, workspacePath string, refreshLimit int) (*workflowBuilderSessionResponse, error) {
	workspacePath = strings.Trim(strings.TrimSpace(workspacePath), "/")
	if workspacePath == "" {
		return nil, nil
	}
	viewerID := GetUserIDFromContext(ctx)
	workflowAccess, _ := workflowAccessForWorkspacePath(ctx, GetUserFromContext(ctx), workspacePath)

	paths := []string{}
	conversationFolder := workspacePath + "/builder/conversation"
	if listing, exists, err := listWorkspaceFolder(ctx, conversationFolder, 5); err != nil {
		return nil, err
	} else if exists {
		collectWorkspaceFilePaths(listing, &paths)
	}

	type candidate struct {
		path       string
		log        builderConversationLog
		rawContent string
		updatedAt  time.Time
	}
	restoreStarted := time.Now()
	candidates := []candidate{}
	for _, path := range paths {
		if !isWorkflowBuilderConversationLogPath(workspacePath, path) {
			continue
		}

		content, exists, err := readFileFromWorkspace(ctx, path)
		if err != nil {
			return nil, err
		}
		if !exists || strings.TrimSpace(content) == "" {
			continue
		}

		var log builderConversationLog
		if err := json.Unmarshal([]byte(content), &log); err != nil {
			continue
		}
		if separateWorkflowConversation(log.SessionID) || !builderConversationVisibleTo(log.UserID, viewerID, workflowAccess) {
			continue
		}
		updatedAt := parseBuilderConversationUpdatedAt(log.UpdatedAt)
		candidates = append(candidates, candidate{path: path, log: log, rawContent: content, updatedAt: updatedAt})
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// PLAT-178: conversation_history is only rewritten when a full turn
	// completes. A resumed session can then continue through /api/live-input,
	// leaving its snapshot behind while the native CLI transcript keeps
	// growing. Refresh *every* candidate before choosing the newest one: doing
	// it only after sorting can select an older saved snapshot and never even
	// inspect the conversation the user actually used last.
	if refreshLimit > 0 && len(candidates) > refreshLimit {
		sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].updatedAt.After(candidates[j].updatedAt) })
		candidates = candidates[:refreshLimit]
	}
	for i := range candidates {
		candidates[i].log = api.refreshLatestBuilderConversationFromNativeTranscript(
			ctx,
			candidates[i].path,
			candidates[i].rawContent,
			candidates[i].log,
		)
		candidates[i].updatedAt = parseBuilderConversationUpdatedAt(candidates[i].log.UpdatedAt)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if !candidates[i].updatedAt.Equal(candidates[j].updatedAt) {
			return candidates[i].updatedAt.After(candidates[j].updatedAt)
		}
		return candidates[i].path > candidates[j].path
	})

	if elapsed := time.Since(restoreStarted); elapsed > 2*time.Second {
		log.Printf("[BUILDER_RESTORE_TIMING] %s: read and refreshed %d saved conversations in %s", workspacePath, len(candidates), elapsed.Round(100*time.Millisecond))
	}

	latest := candidates[0]
	rawEvents := builderConversationToRawEvents(latest.log)
	if len(rawEvents) == 0 {
		return nil, nil
	}

	sessionID := coalesceString(latest.log.SessionID, latest.path)
	updatedAt := latest.log.UpdatedAt
	if updatedAt == "" && !latest.updatedAt.IsZero() {
		updatedAt = latest.updatedAt.Format(time.RFC3339Nano)
	}

	if stepSummary, err := api.restoreLatestWorkflowStepSummary(ctx, workspacePath); err == nil && stepSummary != nil {
		if raw := stepSummaryToRawEvent(sessionID, len(rawEvents), *stepSummary); raw != nil {
			rawEvents = append(rawEvents, raw)
			if latest.updatedAt.IsZero() || stepSummary.UpdatedAt.After(latest.updatedAt) {
				updatedAt = stepSummary.UpdatedAt.Format(time.RFC3339Nano)
			}
		}
	}

	return &workflowBuilderSessionResponse{
		Success:            true,
		Source:             "workspace",
		SessionID:          sessionID,
		PhaseID:            coalesceString(latest.log.PhaseID, "workflow-builder"),
		Status:             "completed",
		DisplayStatus:      "stopped",
		PresetQueryID:      presetQueryID,
		WorkspacePath:      workspacePath,
		WorkflowName:       workflowNameFromWorkspacePath(workspacePath),
		UpdatedAt:          updatedAt,
		ConversationPath:   latest.path,
		Revision:           latest.log.Revision,
		Events:             rawEvents,
		Total:              len(rawEvents),
		LastProcessedIndex: len(rawEvents) - 1,
	}, nil
}

// builderConversationVisibleTo is deliberately stricter than workflow access:
// sharing a workflow lets a reader inspect/run that workflow, but never view
// a different person's private builder conversation. Legacy logs predate the
// user_id field, so only the workflow owner can restore them.
func builderConversationVisibleTo(logUserID, viewerID string, workflowAccess WorkflowAccessLevel) bool {
	logUserID = strings.TrimSpace(logUserID)
	viewerID = strings.TrimSpace(viewerID)
	if logUserID == "" {
		return workflowAccess == WorkflowAccessOwner
	}
	return viewerID != "" && logUserID == viewerID
}

func isWorkflowBuilderConversationLogPath(workspacePath, candidatePath string) bool {
	workspacePath = strings.Trim(strings.TrimSpace(workspacePath), "/")
	candidatePath = strings.Trim(strings.TrimSpace(candidatePath), "/")
	if workspacePath == "" || candidatePath == "" || !strings.HasSuffix(candidatePath, "-conversation.json") {
		return false
	}

	newPrefix := workspacePath + "/builder/conversation/"
	if !strings.HasPrefix(candidatePath, newPrefix) {
		return false
	}
	relative := strings.TrimPrefix(candidatePath, newPrefix)
	parts := strings.Split(relative, "/")
	// Legacy: conversation/YYYY-MM-DD/session-....json
	if len(parts) == 2 {
		if _, err := time.Parse(workflowBuilderConversationDateLayout, parts[0]); err != nil {
			return false
		}
		return strings.HasPrefix(parts[1], "session-")
	}
	// Private: conversation/users/<user-id>/YYYY-MM-DD/session-....json
	// Genuine unowned/system records: conversation/system/YYYY-MM-DD/session-....json
	if len(parts) == 4 && parts[0] == "users" && sanitizeUserIDForPath(parts[1]) == parts[1] {
		if _, err := time.Parse(workflowBuilderConversationDateLayout, parts[2]); err != nil {
			return false
		}
		return strings.HasPrefix(parts[3], "session-")
	}
	if len(parts) == 3 && parts[0] == "system" {
		if _, err := time.Parse(workflowBuilderConversationDateLayout, parts[1]); err != nil {
			return false
		}
		return strings.HasPrefix(parts[2], "session-")
	}
	return false
}

func (api *StreamingAPI) restoreLatestWorkflowStepSummary(ctx context.Context, workspacePath string) (*restoredStepSummary, error) {
	runsFolder := strings.Trim(strings.TrimSpace(workspacePath), "/") + "/runs"
	listing, exists, err := listWorkspaceFolder(ctx, runsFolder, 8)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}

	paths := []string{}
	collectWorkspaceFilePaths(listing, &paths)

	var latest *restoredStepSummary
	for _, path := range paths {
		if !strings.Contains(path, "/logs/") || !strings.Contains(path, "/execution/") || !strings.HasSuffix(path, "-conversation.json") {
			continue
		}

		content, exists, err := readFileFromWorkspace(ctx, path)
		if err != nil {
			return nil, err
		}
		if !exists || strings.TrimSpace(content) == "" {
			continue
		}

		summary := summarizeStepExecutionConversation(path, content)
		if summary == nil {
			continue
		}
		if latest == nil || summary.UpdatedAt.After(latest.UpdatedAt) || (summary.UpdatedAt.Equal(latest.UpdatedAt) && summary.Path > latest.Path) {
			copySummary := *summary
			latest = &copySummary
		}
	}

	return latest, nil
}

func summarizeStepExecutionConversation(path, content string) *restoredStepSummary {
	var log stepExecutionConversationLog
	if err := json.Unmarshal([]byte(content), &log); err != nil {
		return nil
	}

	stepID := stepIDFromExecutionConversationPath(path)
	var latestError *restoredStepSummary
	var latestStatus *restoredStepSummary
	latestObservedAt := time.Time{}

	for _, call := range log.ToolCalls {
		ts := latestToolCallTime(call)
		if ts.IsZero() {
			continue
		}
		if ts.After(latestObservedAt) {
			latestObservedAt = ts
		}
		if strings.TrimSpace(call.StepID) != "" {
			stepID = strings.TrimSpace(call.StepID)
		}
		message := summarizeToolCallResult(call)
		if message == "" {
			continue
		}

		summary := &restoredStepSummary{
			Path:      path,
			StepID:    stepID,
			Status:    "error",
			Message:   message,
			UpdatedAt: ts,
		}
		if latestError == nil || summary.UpdatedAt.After(latestError.UpdatedAt) {
			latestError = summary
		}
	}

	for _, message := range log.ConversationHistory {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "ai" && role != "assistant" {
			continue
		}
		text := builderConversationMessageText(message)
		status := executionStatusFromText(text)
		if status == "" {
			continue
		}
		summary := &restoredStepSummary{
			Path:      path,
			StepID:    stepID,
			Status:    status,
			Message:   truncateRestoredStepMessage(text),
			UpdatedAt: latestObservedAt,
		}
		latestStatus = summary
	}

	if latestError != nil {
		return latestError
	}
	if latestStatus != nil && !latestStatus.UpdatedAt.IsZero() {
		return latestStatus
	}
	return nil
}

func latestToolCallTime(call stepExecutionToolCall) time.Time {
	for _, value := range []string{call.CompletedAt, call.Timestamp, call.StartedAt} {
		if parsed := parseBuilderConversationUpdatedAt(value); !parsed.IsZero() {
			return parsed
		}
	}
	return time.Time{}
}

func summarizeToolCallResult(call stepExecutionToolCall) string {
	result := strings.TrimSpace(call.Result)
	if result == "" {
		return ""
	}

	var decoded struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		Error    string `json:"error"`
		ExitCode *int   `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(result), &decoded); err == nil {
		for _, text := range []string{decoded.Error, decoded.Stderr, decoded.Stdout} {
			text = strings.TrimSpace(text)
			if looksLikeExecutionError(text) {
				return formatStepToolError(call.ToolName, text)
			}
		}
		if decoded.ExitCode != nil && *decoded.ExitCode != 0 {
			text := strings.TrimSpace(coalesceString(decoded.Stderr, decoded.Stdout, result))
			return formatStepToolError(call.ToolName, text)
		}
		return ""
	}

	if looksLikeExecutionError(result) {
		return formatStepToolError(call.ToolName, result)
	}
	return ""
}

func looksLikeExecutionError(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "error:") ||
		strings.Contains(lower, "failed") ||
		strings.Contains(lower, "traceback") ||
		strings.Contains(lower, "exception") ||
		strings.Contains(lower, "status: failed")
}

func formatStepToolError(toolName, message string) string {
	message = truncateRestoredStepMessage(message)
	if strings.TrimSpace(toolName) == "" {
		return message
	}
	return fmt.Sprintf("%s: %s", strings.TrimSpace(toolName), message)
}

func truncateRestoredStepMessage(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= restoredBuilderStepSummaryLimit {
		return message
	}
	return strings.TrimSpace(message[:restoredBuilderStepSummaryLimit]) + "..."
}

func executionStatusFromText(text string) string {
	upper := strings.ToUpper(text)
	switch {
	case strings.Contains(upper, "STATUS: FAILED"):
		return "error"
	case strings.Contains(upper, "STATUS: COMPLETED"):
		return "completed"
	default:
		return ""
	}
}

func stepIDFromExecutionConversationPath(path string) string {
	parts := strings.Split(path, "/")
	for index, part := range parts {
		if part == "logs" && index+1 < len(parts) {
			return parts[index+1]
		}
	}
	return ""
}

func stepSummaryToRawEvent(sessionID string, eventIndex int, summary restoredStepSummary) json.RawMessage {
	timestamp := summary.UpdatedAt
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	stepName := coalesceString(summary.StepID, "workflow step")
	stepStatus := coalesceString(summary.Status, "completed")
	finalResult := fmt.Sprintf("Restored latest workflow step update from `%s`.\n\n%s", stepName, summary.Message)
	if stepStatus == "error" {
		finalResult = fmt.Sprintf("Restored latest workflow step error from `%s`.\n\n%s", stepName, summary.Message)
	}

	raw, err := json.Marshal(map[string]interface{}{
		"id":          fmt.Sprintf("builder-history-%s-step-summary-%d", sessionID, eventIndex),
		"type":        "unified_completion",
		"timestamp":   timestamp,
		"session_id":  sessionID,
		"event_index": eventIndex,
		"data": map[string]interface{}{
			"type":      "unified_completion",
			"timestamp": timestamp,
			"data": map[string]interface{}{
				"status":                   "completed",
				"final_result":             finalResult,
				"timestamp":                timestamp.Format(time.RFC3339Nano),
				"restored_from":            "workflow_step_execution_log",
				"restored_step_status":     stepStatus,
				"step_id":                  summary.StepID,
				"source_conversation_path": summary.Path,
			},
		},
	})
	if err != nil {
		return nil
	}
	return raw
}

func parseBuilderConversationUpdatedAt(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed
	}
	return time.Time{}
}

func builderConversationToRawEvents(log builderConversationLog) []json.RawMessage {
	sessionID := coalesceString(log.SessionID, fmt.Sprintf("builder-history-%d", time.Now().UnixNano()))
	timestamp := parseBuilderConversationUpdatedAt(log.UpdatedAt)
	if timestamp.IsZero() {
		timestamp = time.Now()
	}

	type displayMessage struct {
		originalIndex int
		role          string
		text          string
	}

	displayMessages := []displayMessage{}
	for index, message := range log.ConversationHistory {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		text := builderConversationMessageText(message)
		if text == "" || role == "system" {
			continue
		}
		if role != "human" && role != "user" && role != "ai" && role != "assistant" {
			continue
		}
		displayMessages = append(displayMessages, displayMessage{
			originalIndex: index,
			role:          role,
			text:          text,
		})
	}
	if len(displayMessages) > restoredBuilderConversationMessageLimit {
		displayMessages = displayMessages[len(displayMessages)-restoredBuilderConversationMessageLimit:]
	}

	rawEvents := []json.RawMessage{}
	for _, display := range displayMessages {
		eventType := ""
		payloadKey := ""
		switch display.role {
		case "human", "user":
			eventType = "user_message"
			payloadKey = "content"
		case "ai", "assistant":
			eventType = "unified_completion"
			payloadKey = "final_result"
		}

		data := map[string]interface{}{
			payloadKey:  display.text,
			"timestamp": timestamp.Format(time.RFC3339Nano),
		}
		if eventType == "unified_completion" {
			data["status"] = "completed"
		}

		raw, err := json.Marshal(map[string]interface{}{
			"id":          fmt.Sprintf("builder-history-%s-%d", sessionID, display.originalIndex),
			"type":        eventType,
			"timestamp":   timestamp,
			"session_id":  sessionID,
			"event_index": display.originalIndex,
			"data": map[string]interface{}{
				"type":      eventType,
				"timestamp": timestamp,
				"data":      data,
			},
		})
		if err == nil {
			rawEvents = append(rawEvents, raw)
		}
	}

	return rawEvents
}

func builderConversationMessageText(message builderConversationMessage) string {
	parts := []string{}
	for _, part := range message.Parts {
		text := strings.TrimSpace(part.Text)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func marshalBuilderEvents(events []storeEvents.Event) []json.RawMessage {
	rawEvents := make([]json.RawMessage, 0, len(events))
	for _, event := range events {
		if event.Data != nil && event.Data.Type == "" {
			event.Data.Type = agentEvents.EventType(event.Type)
		}
		raw, err := json.Marshal(event)
		if err == nil {
			rawEvents = append(rawEvents, raw)
		}
	}
	return rawEvents
}

func coalesceString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
