package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// The signed-in app backend projects existence from its current tab registry,
// not provider Working/Idle state. A device stream can wake an idle open tab.
func appAgentChatOpen(a namedMessageAgent) bool {
	return a.Attachment != nil && a.Attachment.DeliveryRoute == appAgentDeliveryRoute && a.Attachment.RuntimeInfo != nil && a.Attachment.RuntimeInfo.ChatOpen
}

func appAgentListed(a namedMessageAgent) bool {
	return a.Attachment == nil || a.Attachment.DeliveryRoute != appAgentDeliveryRoute || appAgentChatOpen(a)
}

type hostedAgentKey struct {
	Owner   string
	Binding hostedBridgeBinding
}

// Addresses/messages keep their historical identities; listing the same tab
// under several registered IDs must not multiply it into several agents.
func uniqueHostedAgents(agents []namedMessageAgent) []namedMessageAgent {
	seen := map[hostedAgentKey]bool{}
	out := make([]namedMessageAgent, 0, len(agents))
	for _, a := range agents {
		if a.Hosted != nil {
			key := hostedAgentKey{a.UserID, *a.Hosted}
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		out = append(out, a)
	}
	return out
}

// No attachment/session and no hosted binding: abandon registrations after a
// week without activity. Message histories stay intact. Back up removed rows
// before the existing transaction actually deletes them; no separate poller.
func expireAbandonedAgentRegistrations(ctx context.Context, s *agentMessageStore, now time.Time) error {
	removed := []namedMessageAgent{}
	kept := make([]namedMessageAgent, 0, len(s.Agents))
	for _, a := range s.Agents {
		last := a.CreatedAt
		if a.LastOnlineAt.After(last) {
			last = a.LastOnlineAt
		}
		abandoned := a.Hosted == nil && (a.Attachment == nil || a.Attachment.SessionID == "") && !namedAgentOnline(a) && !last.IsZero() && last.Before(now.Add(-7*24*time.Hour))
		if abandoned {
			removed = append(removed, a)
		} else {
			kept = append(kept, a)
		}
	}
	if len(removed) == 0 {
		return nil
	}
	mb, err := currentAgentMailbox()
	if err != nil {
		return err
	}
	dir := filepath.Join(filepath.Dir(mb.path), "registration-backups")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, now.UTC().Format("20060102T150405.000000000")+".json")
	if err = fsutil.WriteJSONAtomic(path, removed, 0600); err != nil {
		return fmt.Errorf("back up abandoned agent registrations: %w", err)
	}
	s.Agents = kept
	for _, a := range removed {
		s.revokeServerShares(a.ID, a.UserID, now)
		log.Printf("[AGENTBRIDGE] event=registration_expiry_prepared agent=%s user=%s reason=no_session age_days=%d", a.ID, a.UserID, int(now.Sub(a.CreatedAt).Hours()/24))
	}
	return nil
}

// Only de-duplicate after the caller's individual share checks have admitted
// each row; a private alias must never suppress an authorized public alias.
func uniqueHostedDirectoryRows(rows []map[string]interface{}, agents []namedMessageAgent) []map[string]interface{} {
	keys := map[string]hostedAgentKey{}
	for _, a := range agents {
		if a.Hosted != nil {
			keys[a.ID] = hostedAgentKey{a.UserID, *a.Hosted}
		}
	}
	seen := map[hostedAgentKey]bool{}
	out := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		if key, ok := keys[stringField(row["agent_id"])]; ok {
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		out = append(out, row)
	}
	return out
}
