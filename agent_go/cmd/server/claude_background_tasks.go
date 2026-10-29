package server

import (
	"log"
	"strings"

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
