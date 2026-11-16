package server

import (
	"context"
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
	if api.isSessionMarkedStopped(session) || chatTerminalTurnRefusal(session) != "" {
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
	store, err := readAgentMessageStore(context.Background())
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
					log.Printf("[AGENT_CONTINUE] reason=open_task thread=%s task=%s attempt=%d outcome=%s", c.ID, m.ID, m.TaskProgress.Attempts, map[bool]string{true: "closed", false: "stalled"}[state == "done"])
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
		err = saveAgentMessageStore(context.Background(), store)
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
	if api.recheckAgentMessageAdmission(context.Background(), conversation, task) != nil {
		return true
	}
	if reason := backgroundFollowUps.skipReason(session, false); reason != "" {
		api.scheduleAgentTaskCheck(session, now.Add(time.Minute))
		return true
	}
	// Re-read replies under the mutation lock immediately before reserving a wake.
	agentMessageMu.Lock()
	store, err = readAgentMessageStore(context.Background())
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
			if (state != "open" && state != "in_progress") || due.After(now) || m.TaskProgress.Attempts >= 3 {
				continue
			}
			m.TaskProgress.Attempts++
			attempt = m.TaskProgress.Attempts
			m.TaskProgress.NextWake = now.Add(agentTaskWakeDelay(attempt))
			reserved = saveAgentMessageStore(context.Background(), store) == nil
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
	message := fmt.Sprintf("[You haven't replied in thread %s for %q. Continue the work, or reply in that thread with status done / in_progress / blocked / needs_owner. Final text in this chat is not a thread reply.]", conversation.ID, string(text))
	started := api.executeSyntheticTurnWithContext(session, message, "", func(error) {}, nil, storeevents.BackgroundWakePresentation{Kind: "agent_task"})
	if started {
		api.noteAutomaticFollowUpStarted(session)
		log.Printf("[AGENT_CONTINUE] reason=open_task thread=%s task=%s attempt=%d outcome=woke", conversation.ID, task.ID, attempt)
	}
	// Retry a busy race without spending the task's wake allowance.
	if !started {
		agentMessageMu.Lock()
		if latest, e := readAgentMessageStore(context.Background()); e == nil {
			for i := range latest.Conversations {
				for j := range latest.Conversations[i].Messages {
					m := &latest.Conversations[i].Messages[j]
					if latest.Conversations[i].ID == conversation.ID && m.ID == task.ID && m.TaskProgress.Attempts == attempt {
						m.TaskProgress.Attempts--
						m.TaskProgress.NextWake = now.Add(time.Minute)
					}
				}
			}
			_ = saveAgentMessageStore(context.Background(), latest)
		}
		agentMessageMu.Unlock()
		api.scheduleAgentTaskCheck(session, now.Add(time.Minute))
	}
	return true
}
