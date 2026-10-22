package server

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type agentInboxFilter struct {
	before                    time.Time
	from, inbox, message      string
	processedOnly, unreadOnly bool
}

func parseAgentInboxFilter(args map[string]any, now time.Time) (agentInboxFilter, error) {
	f := agentInboxFilter{}
	for _, key := range []string{"older_than", "from_agent", "inbox_id", "message_id"} {
		if value, exists := args[key]; exists {
			if _, ok := value.(string); !ok {
				return f, fmt.Errorf("%s must be text", key)
			}
		}
	}
	for key, dest := range map[string]*bool{"processed_only": &f.processedOnly, "unread_only": &f.unreadOnly} {
		if value, exists := args[key]; exists {
			b, ok := value.(bool)
			if !ok {
				return f, fmt.Errorf("%s must be true or false", key)
			}
			*dest = b
		}
	}
	if age := strings.TrimSpace(externalArg(args, "older_than")); age != "" {
		if strings.HasSuffix(age, "d") {
			days, err := strconv.ParseInt(strings.TrimSuffix(age, "d"), 10, 64)
			if err != nil || days <= 0 || days > int64((1<<63-1)/(24*time.Hour)) {
				return f, fmt.Errorf("older_than must be an age such as 24h or 7d")
			}
			f.before = now.Add(-time.Duration(days) * 24 * time.Hour)
		} else {
			age, err := time.ParseDuration(age)
			if err != nil || age <= 0 {
				return f, fmt.Errorf("older_than must be an age such as 24h or 7d")
			}
			f.before = now.Add(-age)
		}
	}
	f.from = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(externalArg(args, "from_agent"))), "agent:")
	f.inbox, f.message = strings.TrimSpace(externalArg(args, "inbox_id")), strings.TrimSpace(externalArg(args, "message_id"))
	return f, nil
}

func (f agentInboxFilter) matches(s agentMessageStore, c agentConversation, m agentMessage) bool {
	if f.inbox != "" && f.inbox != c.ID || f.message != "" && f.message != m.ID || !f.before.IsZero() && !m.SentAt.Before(f.before) {
		return false
	}
	processed := !m.ProcessedAt.IsZero() || m.Delivery == "processed"
	if f.processedOnly && !processed || f.unreadOnly && (!m.AdmittedAt.IsZero() || processed) {
		return false
	}
	if f.from != "" {
		id := c.Endpoints[m.From].NamedAgentID
		if id == f.from {
			return true
		}
		for _, a := range s.Agents {
			if a.ID == id && a.Name == f.from {
				return true
			}
		}
		return false
	}
	return true
}

// A cleanup changes only the receiving name's inbox view. It grants no access
// to the other endpoint and never changes its message text, receipt or sequence.
func (api *StreamingAPI) externalAgentInboxCleanup(w http.ResponseWriter, r *http.Request, args map[string]any) {
	action := externalArg(args, "action")
	claims := GetUserFromContext(r.Context())
	if !namedAgentOperationAllowed(claims, action != "count") {
		externalError(w, 403, "insufficient_scope", "This connection cannot manage that agent inbox.")
		return
	}
	name, err := normalizeMessageAgentName(externalArg(args, "agent_name"))
	if err != nil {
		externalError(w, 400, "invalid_arguments", "Choose your registered agent_name to manage its inbox.")
		return
	}
	now := time.Now().UTC()
	filter, err := parseAgentInboxFilter(args, now)
	if err != nil || action == "delete" && filter.message == "" {
		if err == nil {
			err = fmt.Errorf("message_id is required to delete one incoming message")
		}
		externalError(w, 400, "invalid_arguments", err.Error())
		return
	}
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(r.Context())
	if err != nil {
		externalFailure(w, err)
		return
	}
	var agent *namedMessageAgent
	for i := range s.Agents {
		a := &s.Agents[i]
		if a.Name == name && namedAgentConnectionAllowed(*a, claims) {
			agent = a
			break
		}
	}
	if agent == nil {
		externalError(w, 404, "not_found", "That inbox is unavailable or does not belong to this connection.")
		return
	}
	total, unread, pending, processed, matched := 0, 0, 0, 0, 0
	for ci := range s.Conversations {
		c := &s.Conversations[ci]
		for mi := range c.Messages {
			m := &c.Messages[mi]
			dest := c.Endpoints[1-m.From]
			if dest.NamedAgentID != agent.ID || dest.UserID != claims.UserID || !m.InboxRemovedAt.IsZero() {
				continue
			}
			total++
			if !m.ProcessedAt.IsZero() || m.Delivery == "processed" {
				processed++
			} else if m.AdmittedAt.IsZero() {
				unread++
			}
			if (m.Delivery == "stored" || m.Delivery == "pending" || m.Delivery == "dispatching") && !agentMessagePendingExpired(*m, now) {
				pending++
			}
			if filter.matches(s, *c, *m) {
				matched++
				if action != "count" {
					m.InboxRemovedAt = now
				}
			}
		}
	}
	if action != "count" && matched > 0 {
		if err := saveAgentMessageStore(r.Context(), s); err != nil {
			externalFailure(w, err)
			return
		}
		log.Printf("[AGENT-INBOX] action=%s agent=%s removed=%d remaining=%d", action, agent.Name, matched, total-matched)
	}
	out := map[string]any{"agent_name": agent.Name, "total": total, "unread": unread, "pending": pending, "processed": processed, "matched": matched}
	if action != "count" {
		out["removed"], out["remaining"] = matched, total-matched
		out["message"] = fmt.Sprintf("Removed %d messages from %s's inbox. Other participants keep their history and receipts.", matched, agent.Name)
	}
	externalJSON(w, out)
}
