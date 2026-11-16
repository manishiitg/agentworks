package server

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

var agentTaskTimers sync.Map
var agentTaskAfter = func(delay time.Duration, run func()) { time.AfterFunc(delay, run) }

func (api *StreamingAPI) scheduleAgentTaskCheck(session string, due time.Time) {
	key := fmt.Sprintf("%p/%s", api, session)
	if _, exists := agentTaskTimers.LoadOrStore(key, true); exists {
		return
	}
	delay := time.Until(due)
	if delay < time.Second {
		delay = time.Second
	}
	agentTaskAfter(delay, func() { agentTaskTimers.Delete(key); api.continueAgentBridgeTask(session) })
}

func (api *StreamingAPI) agentTaskTurnEndedNormally(session string) bool {
	if api.eventStore == nil {
		return false
	}
	rows := api.eventStore.LastEventsRaw(session, 60)
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if row.Type == "unified_completion" {
			return completionWasNormal(row)
		}
		if row.Type == "user_message" || storeevents.IsTurnEndType(row.Type) && row.Type != "conversation_end" {
			return false
		}
	}
	return false
}

// One source-admitted task per wake, provider independent. The same chat's
// ordinary synthetic-turn gate owns execution; a recipient never gains a turn
// in the sender's chat. Message text is quoted user content, not instructions.
func (api *StreamingAPI) continueAgentBridgeTask(session string) bool {
	if api.isSessionMarkedStopped(session) || chatTerminalTurnRefusal(session) != "" || api.autoNotificationSessionUnreachable(session) {
		return false
	}
	if api.isSessionBusyForAutoNotification(session) {
		api.scheduleAgentTaskCheck(session, time.Now().Add(time.Minute))
		return false
	}
	if !api.agentTaskTurnEndedNormally(session) {
		return false
	}
	now := time.Now().UTC()
	agentMessageMu.Lock()
	store, err := api.readContinueTaskStore(session)
	if err != nil {
		agentMessageMu.Unlock()
		return false
	}
	var conversation agentConversation
	var task agentMessage
	found, open, changed := false, false, false
	var next time.Time
	for i := range store.Conversations {
		c := &store.Conversations[i]
		for j := range c.Messages {
			m := &c.Messages[j]
			if m.TaskProgress.Session != session {
				continue
			}
			state, due := agentTaskState(*c, *m, now)
			if state == "" {
				continue
			}
			if m.TaskProgress.Status != state {
				m.TaskProgress.Status = state
				changed = true
				if state == "done" || state == "stalled" {
					log.Printf("[AGENT_CONTINUE] reason=open_task thread=%s task=%s attempt=%d outcome=%s stop_reason=%s", c.ID, m.ID, m.TaskProgress.Attempts, map[bool]string{true: "closed", false: "stalled"}[state == "done"], map[bool]string{true: "replied", false: "no_progress"}[state == "done"])
				}
			}
			if state == "done" || state == "blocked" || state == "needs_owner" || state == "stalled" {
				continue
			}
			open = true
			if due.IsZero() {
				due = now.Add(agentTaskWakeDelay(0))
				m.TaskProgress.NextWake = due
				changed = true
			}
			if due.After(now) {
				if next.IsZero() || due.Before(next) {
					next = due
				}
				continue
			}
			if !found {
				conversation, task, found = *c, *m, true
			}
		}
	}
	if changed {
		err = api.saveContinueTaskStore(session, store)
	}
	agentMessageMu.Unlock()
	if err != nil {
		return open
	}
	if !next.IsZero() {
		api.scheduleAgentTaskCheck(session, next)
	}
	if !found {
		return open
	}
	api.lastQueryMu.RLock()
	prior, known := api.lastQueryRequests[session]
	api.lastQueryMu.RUnlock()
	expectedOther := conversation.Endpoints[task.From].UserID != conversation.Endpoints[1-task.From].UserID
	if !api.agentTaskTurnStillBound(session, conversation.ID, task.ID) || !known || (task.TaskProgress.LocalRecord == nil && prior.AgentMessageFromOtherPerson != expectedOther) || task.TaskProgress.LocalRecord != nil && prior.appAgentReplyTo != conversation.ID {
		log.Printf("[AGENT_CONTINUE] reason=authority_changed thread=%s task=%s outcome=skipped", conversation.ID, task.ID)
		return true
	}
	if api.recheckContinueTask(conversation, task) != nil {
		return true
	}
	// Re-read replies under the mutation lock immediately before reserving a wake.
	agentMessageMu.Lock()
	store, err = api.readContinueTaskStore(session)
	reserved := false
	attempt := 0
	for i := range store.Conversations {
		c := &store.Conversations[i]
		if c.ID != conversation.ID {
			continue
		}
		for j := range c.Messages {
			m := &c.Messages[j]
			if m.ID != task.ID || m.TaskProgress.Session != session {
				continue
			}
			state, due := agentTaskState(*c, *m, now)
			if (state != "open" && state != "in_progress") || due.After(now) {
				continue
			}
			m.TaskProgress.WakeTimes = append(agentTaskRecentWakes(m.TaskProgress.WakeTimes, now), now)
			m.TaskProgress.Attempts++
			m.TaskProgress.PendingAttempt = m.TaskProgress.Attempts
			attempt = m.TaskProgress.Attempts
			m.TaskProgress.NextWake = now.Add(agentTaskWakeDelay(attempt))
			reserved = api.saveContinueTaskStore(session, store) == nil
		}
	}
	agentMessageMu.Unlock()
	if err != nil || !reserved {
		return true
	}
	text := []rune(strings.ReplaceAll(task.Text, "\n", " "))
	if len(text) > 60 {
		text = text[:60]
	}
	message := fmt.Sprintf("[Task %q is still open. Do the next step now; reply in thread %s only when it is done, blocked or needs the owner. Final text here is not a thread reply.]", string(text), conversation.ID)
	clear := api.eventStore.ExpectAgentMessageSender(session, message, map[string]interface{}{"source": "agent_message", "message_id": task.ID, "agent_message_inbox": conversation.ID})
	started := api.executeSyntheticTurnWithContext(session, message, "", func(err error) {
		clear()
		api.forwardUnsentAgentTaskFinal(session)
		api.finishAgentTaskWake(session, conversation.ID, task.ID, attempt, err)
	}, nil, storeevents.BackgroundWakePresentation{Kind: "agent_task"})
	if started {
		log.Printf("[AGENT_CONTINUE] reason=open_task thread=%s task=%s attempt=%d outcome=woke", conversation.ID, task.ID, attempt)
	}
	// Retry a busy race without spending the task's wake allowance.
	if !started {
		clear()
		agentMessageMu.Lock()
		if latest, e := api.readContinueTaskStore(session); e == nil {
			for i := range latest.Conversations {
				for j := range latest.Conversations[i].Messages {
					m := &latest.Conversations[i].Messages[j]
					if latest.Conversations[i].ID == conversation.ID && m.ID == task.ID && m.TaskProgress.Attempts == attempt {
						m.TaskProgress.Attempts--
						m.TaskProgress.PendingAttempt = 0
						if n := len(m.TaskProgress.WakeTimes); n > 0 {
							m.TaskProgress.WakeTimes = m.TaskProgress.WakeTimes[:n-1]
						}
						m.TaskProgress.NextWake = now.Add(time.Minute)
					}
				}
			}
			_ = api.saveContinueTaskStore(session, latest)
		}
		agentMessageMu.Unlock()
		api.scheduleAgentTaskCheck(session, now.Add(time.Minute))
	}
	return true
}

// Called only for the reserved synthetic wake, not for ordinary turns or timer
// checks. Tool events are normalized by the server for every provider.
func (api *StreamingAPI) finishAgentTaskWake(session, thread, task string, attempt int, turnErr error) {
	tools := false
	if api.eventStore != nil {
		rows := api.eventStore.LastEventsRaw(session, 1000)
		for i := len(rows) - 1; i >= 0; i-- {
			row := rows[i]
			if row.Type == "user_message" {
				break
			}
			if row.Type == "tool_call_start" || row.Type == "tool_call_end" || row.Type == "tool_call_error" {
				tools = true
			}
		}
	}
	normal := turnErr == nil && api.agentTaskTurnEndedNormally(session)
	agentMessageMu.Lock()
	store, err := api.readContinueTaskStore(session)
	if err == nil {
		for i := range store.Conversations {
			c := &store.Conversations[i]
			if c.ID != thread {
				continue
			}
			for j := range c.Messages {
				m := &c.Messages[j]
				if m.ID != task || m.TaskProgress.PendingAttempt != attempt {
					continue
				}
				m.TaskProgress.PendingAttempt = 0
				if normal {
					if tools {
						m.TaskProgress.NoProgress = 0
					} else {
						m.TaskProgress.NoProgress++
					}
					if m.TaskProgress.NoProgress >= 2 {
						m.TaskProgress.Status = "stalled"
						log.Printf("[AGENT_CONTINUE] reason=no_progress thread=%s task=%s attempt=%d outcome=stalled", thread, task, attempt)
					}
				}
			}
		}
		_ = api.saveContinueTaskStore(session, store)
	}
	agentMessageMu.Unlock()
	if normal && IsLocalMode() {
		api.publishDesktopTaskStalled(session, thread, task)
	}
	if normal {
		api.scheduleAgentTaskCheck(session, time.Now().Add(time.Second))
	}
}

// A retained process must still belong to this exact delivered input. A later
// owner turn or another asker's task cannot lend its newer runtime permissions.
func (api *StreamingAPI) agentTaskTurnStillBound(session, thread, task string) bool {
	if api.eventStore == nil {
		return false
	}
	rows := api.eventStore.LastEventsRaw(session, 1000)
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if row.Type != "user_message" {
			continue
		}
		if row.Data == nil {
			return false
		}
		raw, err := json.Marshal(row.Data.Data)
		if err != nil {
			return false
		}
		var payload struct {
			Metadata map[string]interface{} `json:"metadata"`
		}
		if json.Unmarshal(raw, &payload) != nil {
			return false
		}
		return payload.Metadata["source"] == "agent_message" && payload.Metadata["message_id"] == task && payload.Metadata["agent_message_inbox"] == thread
	}
	return false
}
