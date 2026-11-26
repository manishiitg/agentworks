package server

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	mcpagent "github.com/manishiitg/mcpagent/agent"
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
		claudeFinishedTasks.noteState(update.Owner, task)
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
		if update.Closed != "" {
			// Claude's background commands ended with its process (#3131).
			api.endBackgroundOutputs(sessionID, "claude-code")
		}
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
// between turns. Claude wakes itself like this; a chat turn adopts Claude's own turn, the way T3 Code does
// (PLAT-746-899). While another turn holds the chat the wake waits in nativeWakes (its unclaimed timeout is
// stopped) and is adopted when that turn ends, instead of being dropped.
func (api *StreamingAPI) showClaudeWake(sessionID string) {
	if native, wakeID := claudecode.ClaudeSDKWakeIdentity(sessionID); wakeID != 0 {
		claudecode.QueueClaudeSDKWake(sessionID, native, wakeID)
	}
	nativeWakes.addClaude(sessionID)
	api.runQueuedNativeWakes(sessionID)
}

// adoptQueuedClaudeWake runs the chat turn that adopts Claude's waiting wake.
func (api *StreamingAPI) adoptQueuedClaudeWake(sessionID string) {
	if chatTerminalTurnRefusal(sessionID) != "" {
		dropQueuedNativeWakes(sessionID, "continued_in_terminal")
		return
	}
	reason := ""
	if at, ok := claudeTasksStoppedByPerson.LoadAndDelete(sessionID); ok && time.Since(at.(time.Time)) < 2*time.Minute {
		reason = "stopped_by_person"
	} else if !claudecode.ClaudeSDKWakePending(sessionID) {
		// A person's turn took the kept session first; Claude's conversation keeps what it said.
		log.Printf("[SDK_BACKGROUND] follow_up session=%s action=skip reason=wake_taken_by_turn", sessionID)
		return
	} else if reason = backgroundFollowUps.skipReason(sessionID, api.isSessionMarkedStopped(sessionID)); reason == "" && backgroundFollowUps.atLimit(sessionID) {
		reason = "chain_limit"
	}
	if provider := api.storedChatAgentProvider(sessionID); reason == "" && provider != "" && provider != "claude-code" {
		api.handClaudeWorkToChatAgent(sessionID, provider)
		return
	}
	claudeFinishedTasks.take(sessionID)
	if reason == "" && !api.executeSyntheticTurnWithContext(sessionID, claudeWakeMessage, "", nil, llmtypes.WithCodingProviderAdoptWake, storeevents.BackgroundWakePresentation{Kind: "background_work_finished"}) {
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

// The person moved the chat to another agent (Codex, Cursor...) while Claude's
// background work ran: Claude's own follow-up turn has nobody to show it, and
// adopting it would send the new agent a note with no result. The new agent
// gets the finished work's result instead, like a Codex command's follow-up,
// and Claude's own turn is dropped (#3892).
const claudeWorkForOtherAgentMessage = "[Background work finished] Work that Claude Code started earlier in this chat has finished. This chat now runs on you, so Claude Code will not follow up. Check the result below and carry on with what was promised. If nothing was promised, give the person a one-line summary.\n\n%s"

// storedChatAgentProvider is the provider of the agent the chat's next
// follow-up turn runs on.
func (api *StreamingAPI) storedChatAgentProvider(sessionID string) string {
	api.sessionAgentsMux.RLock()
	wrapper := api.sessionAgents[sessionID]
	api.sessionAgentsMux.RUnlock()
	if wrapper == nil || wrapper.GetUnderlyingAgent() == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(string(mcpagent.ReadAgentRuntimeInfo(wrapper.GetUnderlyingAgent()).Provider)))
}

func (api *StreamingAPI) handClaudeWorkToChatAgent(sessionID, provider string) {
	claudecode.DismissClaudeSDKWake(sessionID, "chat_on_"+provider)
	tasks := claudeFinishedTasks.take(sessionID)
	if !api.executeSyntheticTurnWithContext(sessionID, fmt.Sprintf(claudeWorkForOtherAgentMessage, claudeFinishedTasksText(tasks)), "", nil, nil, storeevents.BackgroundWakePresentation{Kind: "background_work_finished"}) {
		log.Printf("[SDK_BACKGROUND] follow_up session=%s action=skip reason=turn_not_started provider=%s", sessionID, provider)
		return
	}
	api.noteAutomaticFollowUpStarted(sessionID)
	log.Printf("[SDK_BACKGROUND] follow_up session=%s action=handed_to_chat_agent provider=%s tasks=%d", sessionID, provider, len(tasks))
}

// claudeFinishedTask is one of Claude's background tasks that ended since the
// chat's last follow-up, with the tail of its output when it was a command.
type claudeFinishedTask struct {
	taskID, label, state, output string
}

const claudeFinishedTasksKept = 10

// claudeFinishedTasks keeps a chat's recently finished Claude tasks until the
// next follow-up takes them; Claude's own follow-up needs none, so it clears them.
var claudeFinishedTasks = &claudeFinishedTaskList{tasks: map[string][]claudeFinishedTask{}, outputs: map[string]string{}}

type claudeFinishedTaskList struct {
	mu      sync.Mutex
	tasks   map[string][]claudeFinishedTask
	outputs map[string]string // session + "\x00" + task id -> last output
}

func (l *claudeFinishedTaskList) noteState(sessionID string, task claudecode.SDKHelperTask) {
	switch task.State {
	case "finished", "failed", "interrupted":
	default:
		return
	}
	label := strings.TrimSpace(task.Description)
	if label == "" {
		label = strings.TrimSpace(task.Command)
	}
	if label == "" {
		label = "Background task"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	key := sessionID + "\x00" + task.TaskID
	list := append(l.tasks[sessionID], claudeFinishedTask{taskID: task.TaskID, label: label, state: task.State, output: l.outputs[key]})
	delete(l.outputs, key)
	if len(list) > claudeFinishedTasksKept {
		list = list[len(list)-claudeFinishedTasksKept:]
	}
	l.tasks[sessionID] = list
}

// noteOutput keeps a finished Claude command's last output for its task.
func (l *claudeFinishedTaskList) noteOutput(sessionID, taskID, output string) {
	if taskID == "" || strings.TrimSpace(output) == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	key := sessionID + "\x00" + taskID
	list := l.tasks[sessionID]
	for i := range list {
		if list[i].taskID == taskID {
			// The task's end was reported before its last output.
			list[i].output = output
			return
		}
	}
	l.outputs[key] = output
}

func (l *claudeFinishedTaskList) take(sessionID string) []claudeFinishedTask {
	l.mu.Lock()
	defer l.mu.Unlock()
	tasks := l.tasks[sessionID]
	delete(l.tasks, sessionID)
	prefix := sessionID + "\x00"
	for key := range l.outputs {
		if strings.HasPrefix(key, prefix) {
			delete(l.outputs, key)
		}
	}
	return tasks
}

func claudeFinishedTasksText(tasks []claudeFinishedTask) string {
	if len(tasks) == 0 {
		return "Its details stayed with Claude Code; tell the person it finished, and that switching back to Claude Code shows its result."
	}
	parts := make([]string, 0, len(tasks))
	for _, task := range tasks {
		state := map[string]string{"finished": "finished", "failed": "failed", "interrupted": "was stopped"}[task.state]
		output := []rune(task.output)
		if len(output) > codexCommandOutputTail {
			output = append([]rune("..."), output[len(output)-codexCommandOutputTail:]...)
		}
		text := string(output)
		if strings.TrimSpace(text) == "" {
			text = "(no output kept)"
		}
		parts = append(parts, fmt.Sprintf("%s: %s\nLast output:\n%s", task.label, state, text))
	}
	return strings.Join(parts, "\n\n")
}
