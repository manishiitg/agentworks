package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// A timer is requested by an agent, bound to that exact session, and never
// interprets silence or automatically resends a message to another agent.
type agentWakeup struct {
	ID       string               `json:"wakeup_id"`
	Endpoint agentMessageEndpoint `json:"endpoint"`
	Due      time.Time            `json:"due_at"`
	Text     string               `json:"message"`
	Status   string               `json:"status"`
}

func materializeAgentWakeups(s *agentMessageStore, now time.Time) bool {
	return materializeAgentWakeupsAllowed(s, now, nil)
}
func materializeAgentWakeupsWithPause(s *agentMessageStore, now time.Time) bool {
	config, err := LoadSchedulerConfig(context.Background())
	if err != nil {
		return false
	}
	return materializeAgentWakeupsAllowed(s, now, config)
}
func materializeAgentWakeupsAllowed(s *agentMessageStore, now time.Time, config *SchedulerConfig) bool {
	changed := false
	for i := range s.Wakeups {
		w := &s.Wakeups[i]
		product := w.Endpoint.Profile
		if w.Endpoint.Kind == triggerCallerWorkflow {
			product = "agentworks"
		}
		if config.ProductPaused(product) {
			continue
		}
		if w.Status != "scheduled" || w.Due.After(now) {
			continue
		}
		pruneAgentConversations(s, now, "")
		if len(s.Conversations) >= agentConversationKeep {
			continue
		}
		c := agentConversation{ID: "wakeup-inbox-" + w.ID, Endpoints: [2]agentMessageEndpoint{w.Endpoint, w.Endpoint}, Next: 1}
		c.Messages = []agentMessage{{ID: "msg-" + w.ID, Sequence: 1, From: 0, Text: w.Text, SentAt: now, Delivery: "pending", Wakeup: true}}
		s.Conversations = append(s.Conversations, c)
		w.Status = "fired"
		changed = true
	}
	return changed
}

func (api *StreamingAPI) scheduleAgentMessageWakeup(ctx context.Context, userID string, caller triggerLinkCaller, action, id, text string, seconds int) (map[string]interface{}, error) {
	e := api.agentMessageCallerRole(callerMessageEndpoint(userID, caller))
	if e.External || e.Session == "" {
		return nil, fmt.Errorf("wakeups require an internal agent conversation")
	}
	if action != "cancel" && (seconds < 1 || seconds > 7*24*3600 || strings.TrimSpace(text) == "" || len([]rune(text)) > codeChatMessageMaxRune) {
		return nil, fmt.Errorf("provide a message and after_seconds between 1 and 604800")
	}
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		return nil, err
	}
	// A Crew woken from a message conversation must wake that conversation's
	// chat, not its main chat: take the receiving chat the store already knows.
	if e.Kind == triggerCallerCrew && !strings.HasPrefix(e.ChatKey, "messages:") {
		for _, c := range s.Conversations {
			for _, end := range c.Endpoints {
				if end.Session == e.Session && strings.HasPrefix(end.ChatKey, "messages:") {
					e.ChatKey = end.ChatKey
				}
			}
		}
	}
	index := -1
	for i, w := range s.Wakeups {
		if w.ID == id && messageEndpointMatches(w.Endpoint, userID, caller) {
			index = i
			break
		}
	}
	if id != "" && index < 0 {
		return nil, fmt.Errorf("wakeup unavailable or access denied")
	}
	if action == "cancel" {
		if index < 0 {
			return nil, fmt.Errorf("wakeup_id is required")
		}
		if s.Wakeups[index].Status != "scheduled" {
			canceled := false
			if s.Wakeups[index].Status == "fired" {
				for i := range s.Conversations {
					if s.Conversations[i].ID == "wakeup-inbox-"+id {
						for j := range s.Conversations[i].Messages {
							m := &s.Conversations[i].Messages[j]
							if m.Delivery == "pending" {
								m.Delivery = "canceled"
								canceled = true
							}
						}
					}
				}
			}
			if !canceled {
				return nil, fmt.Errorf("wakeup has already started or been canceled")
			}
		}
		s.Wakeups[index].Status = "canceled"
	} else {
		if index < 0 {
			active := 0
			for _, w := range s.Wakeups {
				if w.Status == "scheduled" && w.Endpoint.Session == e.Session {
					active++
				}
			}
			if active >= 10 || len(s.Wakeups) >= agentConversationKeep {
				return nil, fmt.Errorf("wakeup capacity reached")
			}
			s.Wakeups = append(s.Wakeups, agentWakeup{ID: "wake-" + uuid.NewString(), Endpoint: e})
			index = len(s.Wakeups) - 1
		} else if s.Wakeups[index].Status != "scheduled" {
			return nil, fmt.Errorf("wakeup has already fired or been canceled")
		}
		s.Wakeups[index].Due = time.Now().UTC().Add(time.Duration(seconds) * time.Second)
		s.Wakeups[index].Text = strings.TrimSpace(text)
		s.Wakeups[index].Status = "scheduled"
	}
	if err = saveAgentMessageStore(ctx, s); err != nil {
		return nil, err
	}
	w := s.Wakeups[index]
	out := map[string]interface{}{"wakeup_id": w.ID, "status": w.Status, "due_at": w.Due, "note": "One wakeup in this exact conversation, on the scheduler tick. Messages do not cancel it and no question is resent."}
	// A scheduler pause holds timed runs, wakeups included. Say so now rather
	// than let the agent believe the wakeup will fire on time.
	product := w.Endpoint.Profile
	if w.Endpoint.Kind == triggerCallerWorkflow {
		product = "agentworks"
	}
	if config, cfgErr := LoadSchedulerConfig(ctx); cfgErr == nil && w.Status == "scheduled" && config.ProductPaused(product) {
		out["held_by_scheduler_pause"] = true
		out["note"] = fmt.Sprintf("%v The scheduler is paused for this product, so this wakeup will not fire until the pause is lifted.", out["note"])
	}
	return out, nil
}
