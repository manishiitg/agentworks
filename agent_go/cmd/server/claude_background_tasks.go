package server

import (
	"fmt"
	"log"
	"strings"
	"sync"
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
		kind := task.Kind
		if kind != "background_command" {
			kind = "helper"
		}
		at := time.Now()
		if task.UpdatedAt > 0 {
			at = time.UnixMilli(task.UpdatedAt)
		}
		record := llmtypes.NativeRecord{ID: "sdk-helper:" + task.TaskID + ":" + task.RunID, Provider: "claude-code", SessionID: update.NativeSessionID, Kind: kind, State: task.State, Origin: "sdk_helper", Name: task.Description, HelperType: task.HelperType, HelperDescription: task.HelperDescription, HelperMode: task.HelperMode, CurrentStep: task.CurrentStep, CommandLabel: task.Command, BackgroundMode: task.BackgroundMode, TaskID: task.TaskID, ToolID: task.ToolUseID, Timestamp: at, TaskRunID: task.RunID, StartedAt: time.UnixMilli(task.StartedAt), Tokens: task.TotalTokens, DurationMS: task.DurationMS}
		activity := agentevents.NewAgentEvent(agentevents.NewCodingAgentNativeActivity(record))
		activity.SessionID = update.Owner
		api.observeTurnInactivity(update.Owner, storeevents.Event{Type: string(agentevents.CodingAgentNativeActivity), SessionID: update.Owner, Timestamp: time.Now(), ExecutionKind: "main_agent", Data: activity})
		if task.StartedAt <= 0 {
			record.StartedAt = time.Time{}
		}
		if task.TotalTokens != nil {
			record.TokenSource = "task_report"
		}
		if task.State == "progress" {
			if api.nativeBackgroundReportUnchanged(update.Owner, record) {
				return
			}
			tokens := int64(-1)
			if task.TotalTokens != nil {
				tokens = *task.TotalTokens
			}
			record.ID += fmt.Sprintf(":progress:%d:%d:%d", task.UpdatedAt, task.DurationMS, tokens)
		}

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
		api.publishSessionChanged(sessionID)
		if update.Wake {
			go api.showClaudeWake(sessionID)
		}
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
	claudeTasksStoppedByPerson.Store(sessionID, time.Now())
	log.Printf("[SDK_BACKGROUND] stop from background work session=%s task=%s", sessionID, taskID)
	return true
}

// claudeTasksStoppedByPerson remembers when the person stopped a chat's Claude task from the background-work list: the
// turn Claude starts on its own about that is not shown.
var claudeTasksStoppedByPerson sync.Map // sessionID -> time.Time

// claudeWakeMessage is what the chat shows for a turn Claude started on its own; Claude does not receive it.
const claudeWakeMessage = "[Background work finished] Claude is following up on work it started earlier in this chat."

// showClaudeWake shows the turn Claude starts on its own when its background work (a build, a test run) finishes
// between turns. Claude wakes itself like this; the kept session used to drop that turn and the app sent its own
// "background work finished" message instead (PLAT-746-856). Now a chat turn adopts Claude's own turn, the way T3
// Code does (PLAT-746-899). Not after Stop, while another turn holds the chat or past the follow-up limit: that
// turn is then not shown, and a session with none of Claude's tasks left closes.
func (api *StreamingAPI) showClaudeWake(sessionID string) {
	reason := ""
	if at, ok := claudeTasksStoppedByPerson.LoadAndDelete(sessionID); ok && time.Since(at.(time.Time)) < 2*time.Minute {
		reason = "stopped_by_person"
	} else {
		reason = backgroundFollowUps.skipReason(sessionID, api.isSessionMarkedStopped(sessionID), api.isSessionBusyForAutoNotification(sessionID))
	}
	if reason == "" && !api.executeSyntheticTurnWithContext(sessionID, claudeWakeMessage, "", nil, llmtypes.WithCodingProviderAdoptWake) {
		reason = "turn_not_started"
	}
	if reason != "" {
		claudecode.DismissClaudeSDKWake(sessionID, reason)
		log.Printf("[SDK_BACKGROUND] follow_up session=%s action=skip reason=%s", sessionID, reason)
		return
	}
	api.noteAutomaticFollowUpStarted(sessionID)
	log.Printf("[SDK_BACKGROUND] follow_up session=%s action=adopted", sessionID)
}
