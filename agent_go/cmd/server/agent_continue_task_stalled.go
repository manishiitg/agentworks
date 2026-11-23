package server

import (
	"context"
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
)

// When a task stops without an answer, the agent that asked is told in the task's own thread, so a lead agent can
// retry, hand the task to another agent or ask a person instead of waiting forever (owner, 23 Nov). A task stops
// when its agent was reminded twice and did nothing, or when its turn failed (a provider error, a lost connection),
// after which nothing would wake it again. The notice is a reply from the working side with the server-only status
// "stalled": it wakes the asker like any reply, and agents can't send that status themselves.
const agentTaskStalledStatus = "stalled"

const (
	agentTaskStallNoProgress = "no_progress"
	agentTaskStallTurnFailed = "turn_failed"
)

func agentTaskStallText(worker, title, reason string) string {
	why := "it was reminded twice and did nothing"
	if reason == agentTaskStallTurnFailed {
		why = "its turn failed with an error"
	}
	return fmt.Sprintf("Automatic notice: %s stopped without finishing %q (%s). It won't be reminded again. Decide the next step: send it a new ask to try again, hand the task to another agent, or ask your person with target=person:<name>.", worker, title, why)
}

func agentTaskStallTitle(task agentMessage) string {
	if title := strings.TrimSpace(task.Title); title != "" {
		return title
	}
	text := []rune(strings.Join(strings.Fields(task.Text), " "))
	if len(text) > 60 {
		text = append(text[:60], '…')
	}
	return string(text)
}

// tellAskerTaskStalled posts the stall notice once per task. Tasks kept as a Mac checkpoint of another server's
// thread are told on that server (recordAppTaskStalled), so they are skipped here.
func (api *StreamingAPI) tellAskerTaskStalled(thread, taskID, reason string) {
	agentMessageMu.Lock()
	snapshot, err := readAgentMessageStore(context.Background())
	agentMessageMu.Unlock()
	if err != nil || strings.HasPrefix(thread, "network:") {
		return
	}
	var c agentConversation
	var task agentMessage
	found := false
	for _, conversation := range snapshot.Conversations {
		for _, m := range conversation.Messages {
			if conversation.ID == thread && m.ID == taskID {
				c, task, found = conversation, m, true
			}
		}
	}
	if !found || task.TaskProgress.LocalRecord != nil {
		return
	}
	if state, _ := agentTaskState(c, task, time.Now()); state != "stalled" {
		return
	}
	submission := "task-stalled:" + task.ID
	worker := endpointDisplayLabel(c.Endpoints[1-task.From], c.Endpoints[task.From].UserID)
	notice := agentMessage{ID: "msg-" + uuid.NewString(), From: 1 - task.From, MessageType: "notification", ReplyStatus: agentTaskStalledStatus, TaskReplyTo: task.ID, SentAt: time.Now().UTC(), Submission: submission, Delivery: "stored", Text: agentTaskStallText(worker, agentTaskStallTitle(task), reason)}
	if err := api.recheckAgentMessageAdmission(context.Background(), c, notice); err != nil {
		log.Printf("[AGENT_CONTINUE] reason=%s thread=%s task=%s outcome=asker_not_told error=%v", reason, thread, taskID, err)
		return
	}
	agentMessageMu.Lock()
	current, err := readAgentMessageStore(context.Background())
	saved := false
	if err == nil {
		for i := range current.Conversations {
			t := &current.Conversations[i]
			if t.ID != c.ID || !reflect.DeepEqual(t.Endpoints, c.Endpoints) {
				continue
			}
			for _, m := range t.Messages {
				if m.Submission == submission {
					agentMessageMu.Unlock()
					return
				}
			}
			// A real reply that raced the notice wins.
			if reply, ok := agentTaskLatestReply(*t, task); ok && reply.ReplyStatus != agentTaskStalledStatus {
				agentMessageMu.Unlock()
				return
			}
			t.Next++
			notice.Sequence = t.Next
			if t.Endpoints[task.From].NamedAgentID != "" {
				current.NextAgentSequence++
				notice.AgentSequence = current.NextAgentSequence
			}
			t.Messages = append(t.Messages, notice)
			saved = saveAgentMessageStore(context.Background(), current) == nil
		}
	}
	agentMessageMu.Unlock()
	if saved {
		log.Printf("[AGENT_CONTINUE] reason=%s thread=%s task=%s message=%s outcome=asker_told", reason, thread, taskID, notice.ID)
		api.kickAgentMessages(c.ID)
	}
}

// agentTaskTurnFailed is a chat whose latest turn ended in an error (not a person's Stop, not an app restart).
func (api *StreamingAPI) agentTaskTurnFailed(session string) bool {
	if api.eventStore == nil || api.isSessionMarkedStopped(session) {
		return false
	}
	rows := api.eventStore.LastEventsRaw(session, 60)
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if row.Type == "user_message" {
			return false
		}
		if row.Type != "unified_completion" {
			continue
		}
		status, source := completionStatusSource(row)
		return (status == "error" || status == "failed") && source != "app_restart"
	}
	return false
}

// stallAgentTasksAfterFailure marks this chat's open tasks stalled after a failed turn and tells each asker.
func (api *StreamingAPI) stallAgentTasksAfterFailure(session string) {
	now := time.Now().UTC()
	type stalled struct{ thread, task string }
	var told []stalled
	var remote []stalled
	agentMessageMu.Lock()
	store, err := api.readContinueTaskStore(session)
	if err == nil {
		for i := range store.Conversations {
			c := &store.Conversations[i]
			for j := range c.Messages {
				m := &c.Messages[j]
				if m.TaskProgress.Session != session {
					continue
				}
				if state, _ := agentTaskState(*c, *m, now); state != "open" && state != "in_progress" {
					continue
				}
				m.TaskProgress.NoProgress = 2
				m.TaskProgress.Status = "stalled"
				m.TaskProgress.PendingAttempt = 0
				log.Printf("[AGENT_CONTINUE] reason=%s thread=%s task=%s outcome=stalled", agentTaskStallTurnFailed, c.ID, m.ID)
				if m.TaskProgress.LocalRecord != nil {
					remote = append(remote, stalled{c.ID, m.ID})
				} else {
					told = append(told, stalled{c.ID, m.ID})
				}
			}
		}
		if len(told)+len(remote) > 0 {
			err = api.saveContinueTaskStore(session, store)
		}
	}
	agentMessageMu.Unlock()
	if err != nil {
		return
	}
	for _, s := range told {
		api.tellAskerTaskStalled(s.thread, s.task, agentTaskStallTurnFailed)
	}
	if IsLocalMode() {
		for _, s := range remote {
			api.publishDesktopTaskStalled(session, s.thread, s.task)
		}
	}
}
