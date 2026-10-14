package server

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Status notices (PLAT-861 "realtime optional"). A message to a named agent that nobody has picked up for a few minutes
// is stored and will be read when its person next uses that chat; the sender is told once, in plain words, so it can
// tell its person. The notice is a server-authored status line in a separate per-agent status conversation: it is never
// a message from the target, is never itself reported on, and asks for nothing.

const agentStatusConversationPrefix = "status-inbox-"
const agentStatusNoticeMaxAge = 24 * time.Hour

// agentStatusNoticeAfter is how long a stored message waits before its sender is told; tests shorten it.
var agentStatusNoticeAfter = 3 * time.Minute

type agentStatusEntry struct {
	label   string
	online  bool
	count   int
	oldest  time.Time
	oldest8 string
}

// sweepAgentStatusNotices appends at most one status message per sending agent for messages that stayed "stored" past
// the wait, and marks those messages so they are reported once. It reports whether the store changed.
func sweepAgentStatusNotices(s *agentMessageStore, now time.Time) bool {
	type key struct{ agentID, userID string }
	senders := map[key]map[string]*agentStatusEntry{}
	endpoints := map[key]agentMessageEndpoint{}
	for ci := range s.Conversations {
		c := &s.Conversations[ci]
		if strings.HasPrefix(c.ID, agentStatusConversationPrefix) || strings.HasPrefix(c.ID, "wakeup-inbox-") {
			continue
		}
		for mi := range c.Messages {
			m := &c.Messages[mi]
			if m.Kind != "" || m.Wakeup || m.NoticeSent || m.Delivery != "stored" || now.Sub(m.SentAt) < agentStatusNoticeAfter || now.Sub(m.SentAt) > agentStatusNoticeMaxAge {
				continue
			}
			src, dest := c.Endpoints[m.From], c.Endpoints[1-m.From]
			if src.NamedAgentID == "" || dest.NamedAgentID == "" {
				continue
			}
			k := key{src.NamedAgentID, src.UserID}
			if senders[k] == nil {
				senders[k] = map[string]*agentStatusEntry{}
				endpoints[k] = src
			}
			label := endpointDisplayLabel(dest, src.UserID)
			e := senders[k][label]
			if e == nil {
				agent, _ := s.destAgentOf(dest)
				e = &agentStatusEntry{label: label, online: agent != nil && namedAgentOnline(*agent)}
				senders[k][label] = e
			}
			e.count++
			if e.oldest.IsZero() || m.SentAt.Before(e.oldest) {
				e.oldest, e.oldest8 = m.SentAt, m.ID
			}
			m.NoticeSent = true
		}
	}
	if len(senders) == 0 {
		return false
	}
	keys := make([]key, 0, len(senders))
	for k := range senders {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].agentID < keys[j].agentID })
	for _, k := range keys {
		var lines []string
		labels := make([]string, 0, len(senders[k]))
		for l := range senders[k] {
			labels = append(labels, l)
		}
		sort.Strings(labels)
		for _, l := range labels {
			e := senders[k][l]
			state := "it is offline"
			if e.online {
				state = "it is connected but has not picked it up yet"
			}
			what := fmt.Sprintf("your message %s (sent %s)", shortMessageID(e.oldest8), e.oldest.UTC().Format("15:04 UTC"))
			if e.count > 1 {
				what = fmt.Sprintf("%d of your messages (the oldest sent %s)", e.count, e.oldest.UTC().Format("15:04 UTC"))
			}
			lines = append(lines, fmt.Sprintf("%s has not read %s: %s. It is stored and will be read when its person next uses that chat.", l, what, state))
		}
		text := strings.Join(lines, "\n") + "\nStatus only: you do not need to resend or reply. You can tell your person it is waiting."
		appendAgentStatusMessage(s, endpoints[k], text, now)
	}
	return true
}

func shortMessageID(id string) string {
	id = strings.TrimPrefix(id, "msg-")
	if len(id) > 8 {
		id = id[:8]
	}
	return id
}

// appendAgentStatusMessage adds a server-authored status line for one named agent to its status conversation.
func appendAgentStatusMessage(s *agentMessageStore, endpoint agentMessageEndpoint, text string, now time.Time) {
	endpoint.ShareID, endpoint.Guest = "", "" // the line is for this agent itself, whoever shared it
	id := agentStatusConversationPrefix + endpoint.NamedAgentID
	idx := -1
	for i := range s.Conversations {
		if s.Conversations[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.Conversations = append(s.Conversations, agentConversation{ID: id, Endpoints: [2]agentMessageEndpoint{endpoint, endpoint}, Next: 0})
		idx = len(s.Conversations) - 1
	}
	c := &s.Conversations[idx]
	if len(c.Messages) >= 100 {
		c.Messages = c.Messages[len(c.Messages)-99:] // a sender that never reads them does not grow the list
	}
	c.Next++
	s.NextAgentSequence++
	c.Messages = append(c.Messages, agentMessage{ID: "msg-status-" + fmt.Sprint(s.NextAgentSequence) + "-" + shortMessageID(endpoint.NamedAgentID), Sequence: c.Next, From: 0, Text: text, SentAt: now, Delivery: "stored", Kind: "status", AgentSequence: s.NextAgentSequence})
}

var agentStatusSweeperOnce sync.Once

// startAgentStatusNoticeSweeper looks once a minute for messages that have waited too long.
func startAgentStatusNoticeSweeper() {
	if testing.Testing() {
		return
	}
	agentStatusSweeperOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				runAgentStatusSweep(context.Background(), time.Now().UTC())
			}
		}()
	})
}

func runAgentStatusSweep(ctx context.Context, now time.Time) {
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(ctx)
	if err != nil || !sweepAgentStatusNotices(&s, now) {
		return
	}
	_ = saveAgentMessageStore(ctx, s)
}
