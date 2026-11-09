package server

import (
	"context"
	"fmt"
	"strings"
	"sync"

	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Whoever starts a run gets its result. A chat that starts a run with a
// schedule's run-now is resumed with the run's terminal result through the same
// auto-notification path a started step or a function call uses. The run itself
// executes in its own scheduled session, so without this the result reached
// only that session. A timer has no caller; it is reported to the linked Crew
// by the scheduler as before.
//
// A chat-started trigger shares this registration and cancellation path.
// Unattended deliveries have no caller; declared functions keep their own watch.

// chatFunctionOrigin accepts only a session captured by the server's tool
// registrar for this authenticated person, never a field in tool arguments.
func (api *StreamingAPI) chatFunctionOrigin(ctx context.Context) string {
	if api == nil {
		return ""
	}
	id, _ := ctx.Value(structuredFunctionCallerSessionKey{}).(string)
	if id == "" || isScheduledSessionIdentity(id, "") {
		return ""
	}
	active, ok := api.getActiveSession(id)
	if !ok || active.UserID != GetUserIDFromContext(ctx) {
		return ""
	}
	return id
}

// scheduleCallerNotice is the pending result owed to the chat that started a run.
type scheduleCallerNotice struct {
	notifier    *workshopExecutionBgNotifier
	executionID string
	name        string
	mu          sync.Mutex
	done        bool
}

// rememberScheduleCaller records the chat that asked for this run. Only an
// active chat can be resumed, and the run-now of a scheduled session is not a
// chat.
func (s *SchedulerService) rememberScheduleCaller(sctx *ScheduleContext, originSessionID string) {
	originSessionID = strings.TrimSpace(originSessionID)
	if s == nil || s.api == nil || sctx == nil || originSessionID == "" || isScheduledSessionIdentity(originSessionID, "") {
		return
	}
	s.api.activeSessionsMux.RLock()
	session, active := s.api.activeSessions[originSessionID]
	s.api.activeSessionsMux.RUnlock()
	if !active || session == nil {
		return
	}
	sctx.CallerSessionID = originSessionID
	sctx.CallerExecutionID = s.api.currentConversationTurnExecutionID(originSessionID)
}

// beginCallerNotice registers the run as a running execution of the caller's
// chat, so the chat knows a result is coming.
func (s *SchedulerService) beginCallerNotice(sctx *ScheduleContext, runID string) *scheduleCallerNotice {
	if s == nil || s.api == nil || s.api.bgAgentRegistry == nil || sctx == nil || sctx.CallerSessionID == "" || strings.TrimSpace(runID) == "" {
		return nil
	}
	caller := sctx.CallerSessionID
	parentSession, ok := s.api.getActiveSession(caller)
	if !ok {
		return nil
	}
	name := "Schedule " + firstNonEmptyString(sctx.Schedule.Name, sctx.Schedule.ID)
	parent := firstNonEmptyString(sctx.CallerExecutionID, "session:"+caller)
	notice := &scheduleCallerNotice{
		notifier:    &workshopExecutionBgNotifier{api: s.api, sessionID: caller, workspacePath: parentSession.WorkspacePath, userID: parentSession.UserID},
		executionID: "schedule-run-" + runID,
		name:        name,
	}
	notice.notifier.OnExecutionStart(todo_creation_human.WorkshopExecutionStart{
		ID: notice.executionID, ParentExecutionID: parent, Name: name,
		Kind: "trigger_auto_notify", Cancel: func() { s.stopChatWorkflowRun(runID, sctx.Schedule.ID) },
		Metadata: map[string]string{
			"execution_type": "schedule-run-auto-notify",
			"schedule_id":    sctx.Schedule.ID,
			"run_id":         runID,
		},
	})
	if s.api.bgAgentRegistry.Get(caller, notice.executionID) == nil {
		return nil
	}
	return notice
}

// finish resumes the caller's chat once with the run's outcome. A run whose own
// destination chat is the caller already posts its result there, so the pending
// execution is settled without a second message.
func (n *scheduleCallerNotice) finish(sctx *ScheduleContext, status, errMsg, runFolder string) {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.done {
		return
	}
	n.done = true
	var meta map[string]string
	if sctx.ChatDestinationSessionID != "" && sctx.ChatDestinationSessionID == sctx.CallerSessionID {
		meta = map[string]string{"suppress_auto_notification": "true"}
	}
	files := ""
	if strings.TrimSpace(runFolder) != "" {
		files = fmt.Sprintf(" Run files: `%s/runs/%s`.", sctx.WorkspacePath, runFolder)
	}
	if status == "error" || status == "failed" {
		reason := firstNonEmptyString(errMsg, "the run failed")
		n.notifier.OnExecutionComplete(n.executionID, n.name, "", meta, fmt.Errorf("%s failed: %s.%s", n.name, strings.TrimSuffix(reason, "."), files))
		return
	}
	if status == scheduleRunStatusWaitingForCapacity {
		n.notifier.OnExecutionComplete(n.executionID, n.name, fmt.Sprintf("%s is paused, waiting for capacity: %s It resumes by itself.%s", n.name, strings.TrimSpace(errMsg), files), meta, nil)
		return
	}
	text := fmt.Sprintf("%s finished: %s.", n.name, firstNonEmptyString(status, "completed"))
	if status == "partial" || status == "stopped" {
		if strings.TrimSpace(errMsg) != "" {
			text += " " + strings.TrimSpace(errMsg)
		}
	}
	n.notifier.OnExecutionComplete(n.executionID, n.name, text+files, meta, nil)
}
