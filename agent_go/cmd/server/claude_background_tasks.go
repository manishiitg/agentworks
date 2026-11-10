package server

import (
	"log"
	"strings"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

// Tasks Claude started itself with run_in_background (a dev server, a long
// build) keep running after its answer (PLAT-872): the provider keeps the
// chat's SDK session while they run. They are background work of the chat,
// listed with the rest (PLAT-705) and stopped one by one from the same list.

const claudeTaskWorkPrefix = "claude-task:"

var (
	claudeBackgroundTasks    = claudecode.ClaudeSDKBackgroundTasks
	stopClaudeBackgroundTask = claudecode.StopClaudeSDKBackgroundTask
)

// releaseAnsweredCodingTurn ends a turn that already gave its answer but was
// held open (a steer, work a steer moved aside) with that answer, before the
// idle limit would cancel it and replace the answer with a failure. False when
// the turn has no answer yet; the caller then cancels it as before.
var releaseAnsweredCodingTurn = func(sessionID string) bool {
	if !claudecode.ReleaseAnsweredSDKTurn(sessionID) {
		return false
	}
	log.Printf("[TURN IDLE] session=%s: the turn already had its answer; ended it with that answer instead of failing it", sessionID)
	return true
}

// The provider reports every change of a chat's list; the chat's background
// work pill reads the list itself, so a change only refreshes the session's
// runtime state and tells open screens to read it again.
func (api *StreamingAPI) wireClaudeBackgroundTasks() {
	claudecode.SetSDKAnswerListener(recordNativeTurnAnswer)
	claudecode.SetSDKHelperListener(func(update claudecode.SDKHelperUpdate) {
		if api.eventStore == nil || update.Owner == "" || !api.eventStore.IsDurableChatSession(update.Owner) {
			return
		}
		task := update.Task
		record := llmtypes.NativeRecord{ID: "sdk-helper:" + task.TaskID + ":" + task.RunID, Provider: "claude-code", SessionID: update.NativeSessionID, Kind: "helper", State: task.State, Origin: "sdk_helper", Name: task.Description, TaskID: task.TaskID, ToolID: task.ToolUseID, Timestamp: time.Now()}
		activity := agentevents.NewAgentEvent(agentevents.NewCodingAgentNativeActivity(record))
		activity.SessionID = update.Owner
		api.observeTurnInactivity(update.Owner, storeevents.Event{Type: string(agentevents.CodingAgentNativeActivity), SessionID: update.Owner, Timestamp: time.Now(), ExecutionKind: "main_agent", Data: activity})
		if task.State == "progress" {
			return
		} // actual native progress, not another transcript row

		if err := api.saveNativeCodingActivity(update.Owner, record); err != nil {
			log.Printf("[SDK_HELPER] event failed session=%s task=%s state=%s: %v", update.Owner, task.TaskID, task.State, err)
			return
		}
		log.Printf("[SDK_HELPER] event saved session=%s task=%s state=%s", update.Owner, task.TaskID, task.State)
	})
	claudecode.SetSDKBackgroundListener(func(update claudecode.SDKBackgroundUpdate) {
		sessionID := strings.TrimSpace(update.Owner)
		if sessionID == "" {
			return
		}
		log.Printf("[SDK_BACKGROUND] list session=%s tasks=%d closed=%s", sessionID, len(update.Tasks), update.Closed)
		api.observeRuntimeSnapshot(sessionID)
		publishSessionsChanged()
	})
}

func claudeTasksRunning(sessionID string) bool {
	return len(claudeBackgroundTasks(sessionID)) > 0
}

func claudeTaskBackgroundWork(sessionID string) []SessionBackgroundWorkItem {
	items := []SessionBackgroundWorkItem{}
	for _, task := range claudeBackgroundTasks(sessionID) {
		label, status := strings.TrimSpace(task.Description), strings.TrimSpace(task.Command)
		if label == "" {
			label, status = status, ""
		}
		if label == "" {
			label = "Background task"
		}
		items = append(items, SessionBackgroundWorkItem{ID: claudeTaskWorkPrefix + task.TaskID, Kind: "claude_task", Label: label, StartedAt: task.StartedAt, CanStop: true, Status: status})
	}
	return items
}

func stopClaudeTaskBackgroundWork(sessionID, execID string) bool {
	taskID := strings.TrimPrefix(execID, claudeTaskWorkPrefix)
	if taskID == "" || !stopClaudeBackgroundTask(sessionID, taskID) {
		return false
	}
	log.Printf("[SDK_BACKGROUND] stop from background work session=%s task=%s", sessionID, taskID)
	return true
}
