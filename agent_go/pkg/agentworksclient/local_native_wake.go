package agentworksclient

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

type nativeWakeTry struct {
	Sequence, Count int
	Reported        bool
}

// Called with the bridge lock. These are metadata subscriptions, one per saved
// login, not a socket or mailbox for every dormant record.
func (b *localBridge) refreshNativeWakeStreams() {
	if b.options.NativeWake == nil || b.closed {
		return
	}
	if b.wakeStreams == nil {
		b.wakeStreams = map[string]context.CancelFunc{}
	}
	wanted := map[string]bool{}
	for _, s := range b.sessions {
		if _, _, ok := b.nativeWakeTarget(s); ok && s.ParentConfigPath != "" {
			wanted[s.ParentConfigPath] = true
		}
	}
	for path, cancel := range b.wakeStreams {
		if !wanted[path] {
			cancel()
			delete(b.wakeStreams, path)
		}
	}
	for path := range wanted {
		if b.wakeStreams[path] != nil {
			continue
		}
		ctx, cancel := context.WithCancel(b.ctx)
		b.wakeStreams[path] = cancel
		b.workers.Add(1)
		go func(path string) {
			defer b.workers.Done()
			login := sha256.Sum256([]byte(path))
			delay := time.Second
			for ctx.Err() == nil {
				client, err := localConfiguredClient(path)
				if err == nil {
					err = client.StreamAgentWakeHints(ctx, func(hint AgentLiveMessage) error {
						b.mu.Lock()
						defer b.mu.Unlock()
						if b.closed || ctx.Err() != nil {
							return context.Canceled
						}
						for _, s := range b.sessions {
							if s.ParentConfigPath != path || s.Attachment.AgentID != hint.AgentID {
								continue
							}
							if hint.AttachmentID != s.Attachment.ID {
								continue
							}
							s.latestSequence = max(s.latestSequence, hint.Sequence)
							if s.Offline {
								b.queueNativeWake(s)
							}
						}
						return nil
					}, func() {
						delay = time.Second
						log.Printf("[BRIDGE-WAKE] outcome=connected login=%x", login[:4])
					})
				}
				if ctx.Err() != nil {
					return
				}
				status := 0
				code := "stream_disconnected"
				var apiError *APIError
				if errors.As(err, &apiError) {
					status, code = apiError.Status, apiError.Code
				}
				log.Printf("[BRIDGE-WAKE] outcome=disconnected login=%x status=%d reason=%s", login[:4], status, code)
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				delay = min(30*time.Second, delay*2)
			}
		}(path)
	}
}

func (b *localBridge) nativeWakeTarget(s *localSession) (*localSession, string, bool) {
	if s == nil || s.client == nil || s.ServerClosed || !s.AccountSenders || s.Attachment.Provider != "codex" {
		return nil, "", false
	}
	key := localBridgeKey(s.Attachment.Provider, s.Attachment.SessionID)
	primary := b.sessions[key]
	if primary == nil || primary.ServerClosed || b.stopped[key] || !primary.AccountSenders || !strings.HasPrefix(primary.NativeRemote, "desktop+unix://") {
		return nil, "", false
	}
	sourceKey := key
	if s.Also {
		sourceKey = localAlsoKey(key, s.ParentConfigPath)
	}
	if b.stopped[sourceKey] || b.sessions[sourceKey] != s {
		return nil, "", false
	}
	return primary, key, true
}

// A pending-message hint or a definite rejected native offer is the only caller.
// Opening is outside the helper lock and deduped across a chat's chosen networks.
func (b *localBridge) queueNativeWake(s *localSession) bool {
	primary, key, ok := b.nativeWakeTarget(s)
	if !ok || b.options.NativeWake == nil || b.closed {
		return false
	}
	if b.wakeOpening == nil {
		b.wakeOpening = map[string]bool{}
		b.wakeAfter = map[string]time.Time{}
		b.wakeTries = map[string]nativeWakeTry{}
	}
	recordKey := key
	if s.Also {
		recordKey = localAlsoKey(key, s.ParentConfigPath)
	}
	tries := b.wakeTries[recordKey]
	if tries.Sequence != s.latestSequence {
		tries = nativeWakeTry{Sequence: s.latestSequence}
	}
	if tries.Count >= 3 {
		s.LastError = "Could not open this chat automatically. Open it once in Codex, or AgentWorks will try again when the next message arrives."
		if !tries.Reported {
			log.Printf("[BRIDGE-WAKE] outcome=gave_up agent=%s attachment=%s sequence=%d", s.Attachment.AgentName, s.Attachment.ID, s.latestSequence)
			tries.Reported = true
			b.wakeTries[recordKey] = tries
		}
		return false
	}
	if b.wakeOpening[key] || time.Now().Before(b.wakeAfter[key]) {
		return false
	}
	tries.Count++
	b.wakeTries[recordKey] = tries
	b.wakeOpening[key] = true
	b.wakeAfter[key] = time.Now().Add(20 * time.Second)
	source, attachment, remote := s, s.Attachment, primary.NativeRemote
	target := primary.Attachment
	b.workers.Add(1)
	go func() {
		defer b.workers.Done()
		defer func() { b.mu.Lock(); delete(b.wakeOpening, key); b.wake(); b.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(b.ctx, 25*time.Second)
		defer cancel()
		var current struct {
			AgentID    string           `json:"agent_id"`
			Attachment *AgentAttachment `json:"attachment"`
		}
		raw, err := source.client.request(ctx, http.MethodPost, "/api/external/v1/agents/attachment", map[string]any{"action": "inspect", "agent_name": attachment.AgentName}, true)
		if err == nil {
			err = json.Unmarshal(raw, &current)
		}
		b.mu.Lock()
		p, _, valid := b.nativeWakeTarget(source)
		valid = valid && p == primary && source.Attachment.ID == attachment.ID && current.AgentID == attachment.AgentID && current.Attachment != nil && current.Attachment.ID == attachment.ID
		if err == nil && valid {
			source.ReadPosition = 0
			_ = b.save()
		}
		b.mu.Unlock()
		if err != nil || !valid {
			status := 0
			var apiError *APIError
			if errors.As(err, &apiError) {
				status = apiError.Status
			}
			log.Printf("[BRIDGE-WAKE] outcome=inspect_refused agent=%s attachment=%s status=%d", attachment.AgentName, attachment.ID, status)
			return
		}
		if err = b.options.NativeWake(ctx, target, remote); err != nil {
			log.Printf("[BRIDGE-WAKE] outcome=failed agent=%s attachment=%s", attachment.AgentName, attachment.ID)
		} else {
			b.mu.Lock()
			if _, _, valid := b.nativeWakeTarget(source); valid && source.Attachment.ID == attachment.ID {
				source.ReadPosition = 0
				_ = b.save()
			}
			b.mu.Unlock()
		}
	}()
	return true
}
