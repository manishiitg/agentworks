package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
)

const agentTaskUpdateKind = "task_update"

func agentTaskReplySummary(c agentConversation, reply agentMessage) string {
	if reply.TaskReplyTo == "" || reply.ReplyStatus == "in_progress" {
		return ""
	}
	for _, task := range c.Messages {
		if task.ID == reply.TaskReplyTo && task.From != reply.From && task.MessageType == "request" {
			return fmt.Sprintf("%s replied to your request %q: %s. %s\n\n", endpointDisplayLabel(c.Endpoints[reply.From], c.Endpoints[task.From].UserID), firstLineClip(task.Text, 60), reply.ReplyStatus, firstLineClip(reply.Text, 160))
		}
	}
	return ""
}

// An exact server-stamped input/reply link is mandatory. Adjacency to a final
// answer is not enough: it may be the owner's text or another asker's turn.
func agentTaskFinalText(rows []storeevents.Event, task string) (string, string, time.Time) {
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if row.Type == "user_message" {
			return "", "", time.Time{}
		}
		if row.Type != "unified_completion" {
			continue
		}
		if row.ReplyToAgentMessageID != task || !completionWasNormal(row) || row.Data == nil {
			return "", "", time.Time{}
		}
		raw, err := json.Marshal(row.Data.Data)
		if err != nil {
			return "", "", time.Time{}
		}
		var payload struct {
			Final string `json:"final_result"`
		}
		if json.Unmarshal(raw, &payload) != nil {
			return "", "", time.Time{}
		}
		start := row.Timestamp
		if row.Data.TurnID != "" {
			for j := i - 1; j >= 0; j-- {
				if rows[j].Type == "user_message" && rows[j].Data != nil && rows[j].Data.TurnID == row.Data.TurnID {
					start = rows[j].Timestamp
					break
				}
			}
		}
		return row.ID, redaction.Text(strings.TrimSpace(payload.Final)), start
	}
	return "", "", time.Time{}
}

// Automatic progress is a labelled transcript update, never an explicit
// response. The task reducer ignores this kind and the worker still continues.
func (api *StreamingAPI) forwardUnsentAgentTaskFinal(session string) {
	if api.eventStore == nil || !api.agentTaskTurnEndedNormally(session) {
		return
	}
	rows := api.eventStore.LastEventsRaw(session, 1000)
	agentMessageMu.Lock()
	snapshot, err := api.readContinueTaskStore(session)
	agentMessageMu.Unlock()
	if err != nil {
		return
	}
	for _, c := range snapshot.Conversations {
		for _, request := range c.Messages {
			if request.TaskProgress.Session != session || request.MessageType != "request" || request.Kind != "" {
				continue
			}
			event, text, at := agentTaskFinalText(rows, request.ID)
			if event == "" || text == "" {
				continue
			}
			if IsLocalMode() && request.TaskProgress.LocalRecord != nil {
				api.forwardDesktopTaskFinal(c, request, event, text)
				continue
			}

			if reply, found := agentTaskLatestReply(c, request); found && !reply.SentAt.Before(at) {
				continue
			}
			state, _ := agentTaskState(c, request, time.Now())
			if state == "done" || state == "blocked" || state == "needs_owner" {
				continue
			}
			submission := "task-final:" + request.ID + ":" + event
			label := endpointDisplayLabel(c.Endpoints[1-request.From], c.Endpoints[request.From].UserID)
			update := agentMessage{ID: "msg-" + uuid.NewString(), From: 1 - request.From, Kind: agentTaskUpdateKind, MessageType: "notification", ReplyStatus: "in_progress", SentAt: time.Now().UTC(), Submission: submission, Delivery: "stored", Text: "Auto-forwarded from " + label + "'s chat (not a reply)\n\n" + text}
			if err := api.recheckAgentMessageAdmission(context.Background(), c, update); err != nil {
				continue
			}
			agentMessageMu.Lock()
			current, e := readAgentMessageStore(context.Background())
			saved := false
			if e == nil {
				for i := range current.Conversations {
					thread := &current.Conversations[i]
					if thread.ID != c.ID || !reflect.DeepEqual(thread.Endpoints, c.Endpoints) {
						continue
					}
					exists := false
					for _, m := range thread.Messages {
						if m.Submission == submission {
							exists = true
						}
					}
					if exists {
						break
					}
					// A reply that raced the final must win over automatic forwarding.
					if reply, ok := agentTaskLatestReply(*thread, request); ok && !reply.SentAt.Before(at) {
						break
					}
					thread.Next++
					update.Sequence = thread.Next
					if thread.Endpoints[1-update.From].External {
						update.Delivery = "stored"
					}
					if thread.Endpoints[1-update.From].NamedAgentID != "" {
						current.NextAgentSequence++
						update.AgentSequence = current.NextAgentSequence
					}
					thread.Messages = append(thread.Messages, update)
					saved = saveAgentMessageStore(context.Background(), current) == nil
				}
			}
			agentMessageMu.Unlock()
			if saved {
				log.Printf("[AGENT-MESSAGE] event=final_text_forwarded thread=%s task=%s message=%s", c.ID, request.ID, update.ID)
				api.kickAgentMessages(c.ID)
			}
		}
	}
}
