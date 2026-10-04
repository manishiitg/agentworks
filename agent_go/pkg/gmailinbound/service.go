package gmailinbound

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Client interface {
	Watch(context.Context) (string, int64, error)
	History(context.Context, string) ([]string, string, error)
	Message(context.Context, string) (RawMessage, error)
	Resync(context.Context, int64) ([]string, string, error)
}

var ErrHistoryExpired = errors.New("Gmail history cursor expired")
var ErrMessageGone = errors.New("Gmail message no longer exists")

type Service struct {
	Enabled   func(context.Context) bool
	Store     *Store
	Client    func(context.Context, Mailbox) (Client, error)
	Authorize func(context.Context, Route, Message) error
	Dispatch  func(context.Context, *Delivery) error
	Reply     func(context.Context, Delivery) error
	Verify    func(context.Context, string) error
	mu        sync.Mutex
	busy      map[string]bool
	wg        sync.WaitGroup
}

func (s *Service) Start(ctx context.Context) {
	s.busy = map[string]bool{}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			_ = s.Store.PruneContent(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	for i := 0; i < 4; i++ {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.syncNext(ctx)
				}
			}
		}()
	}
	for i := 0; i < 2; i++ {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.deliverNext(ctx)
				}
			}
		}()
	}
}
func (s *Service) Wait() { s.wg.Wait() }
func (s *Service) syncNext(ctx context.Context) {
	if s.Enabled != nil && !s.Enabled(ctx) {
		return
	}
	boxes, e := s.Store.Mailboxes(ctx)
	if e != nil {
		return
	}
	for _, m := range boxes {
		s.mu.Lock()
		busy := s.busy[m.Email]
		if !busy {
			s.busy[m.Email] = true
		}
		s.mu.Unlock()
		if busy {
			continue
		}
		e = s.syncMailbox(ctx, m)
		if e != nil && ctx.Err() == nil {
			_ = s.Store.SyncError(ctx, m.ConnectionID, e)
			log.Printf("[GMAIL-INBOUND] mailbox sync needs attention: %s", m.ConnectionID)
		}
		s.mu.Lock()
		delete(s.busy, m.Email)
		s.mu.Unlock()
		return
	}
}
func (s *Service) syncMailbox(ctx context.Context, m Mailbox) error {
	syncStarted := time.Now().Unix()
	initial := m.Cursor == "" || m.Processed == 0
	client, e := s.Client(ctx, m)
	if e != nil {
		return e
	}
	if m.RenewAt <= time.Now().Unix() {
		cursor, expiration, e := client.Watch(ctx)
		if e != nil {
			return e
		}
		if cursor == "" {
			return fmt.Errorf("Gmail watch returned no history position")
		}
		renew := time.Now().Add(24 * time.Hour).Unix()
		if expiration > 0 && expiration-3600 < renew {
			renew = expiration - 3600
		}
		if e = s.Store.Watch(ctx, m.ConnectionID, cursor, renew); e != nil {
			return e
		}
		if m.Cursor == "" {
			m.Cursor = cursor
		}
	}
	ids, cursor, e := client.History(ctx, m.Cursor)
	if initial || errors.Is(e, ErrHistoryExpired) {
		ids, cursor, e = client.Resync(ctx, m.Since)
	}
	if e != nil {
		return e
	}
	routes, e := s.Store.Routes(ctx)
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		raw, e := client.Message(ctx, id)
		if errors.Is(e, ErrMessageGone) {
			continue
		}
		if e != nil {
			return e
		}
		message, e := raw.Parse(m.Email)
		if e != nil {
			continue
		}
		if message.Blocked || !message.Authenticated {
			continue
		}
		for _, r := range routes {
			if !r.Enabled || r.ConnectionID != m.ConnectionID {
				continue
			}
			if r.EnabledAt > 0 && message.ReceivedAt < r.EnabledAt {
				continue
			}
			matched := false
			for _, to := range message.Recipients {
				if strings.EqualFold(to, r.Address) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			if e = s.Store.EnqueueAuthorized(ctx, r, message, s.Authorize); e != nil {
				return e
			}
		}
	}
	if cursor == "" {
		cursor = m.Cursor
	}
	m.Since = syncStarted
	return s.Store.Synced(ctx, m, cursor)
}
func (s *Service) deliverNext(ctx context.Context) {
	if s.Enabled != nil && !s.Enabled(ctx) {
		return
	}
	d, ok, e := s.Store.Claim(ctx)
	if e != nil || !ok {
		return
	}
	if !d.Route.Enabled {
		_ = s.Store.Finish(ctx, d, "rejected", fmt.Errorf("email route was disabled"))
		return
	}
	if e = s.Authorize(ctx, d.Route, d.Message); e != nil {
		_ = s.Store.Finish(ctx, d, "rejected", e)
		return
	}
	if d.Status == "reply" {
		e = s.Reply(ctx, d)
		status := "complete"
		if e != nil {
			status = "reply_failed"
		}
		_ = s.Store.Finish(context.WithoutCancel(ctx), d, status, e)
		return
	}
	reason, e := s.Store.FilterReason(ctx, d)
	if e != nil {
		_ = s.Store.Finish(ctx, d, "failed", e)
		return
	}
	if reason != "" {
		_ = s.Store.Finish(ctx, d, "filtered", errors.New(reason))
		return
	}
	e = s.Dispatch(ctx, &d)
	status := "complete"
	if e != nil {
		status = "failed"
	} else if d.Route.Reply && strings.TrimSpace(d.Response) != "" {
		status = "reply"
	}
	if ctx.Err() != nil {
		status = "uncertain"
	}
	_ = s.Store.Finish(context.WithoutCancel(ctx), d, status, e)
}

// Receive authenticates before accepting any payload. A successful response
// means the wakeup is on disk, not that an agent has finished its work.
func (s *Service) Receive(w http.ResponseWriter, r *http.Request) {
	if s.Verify == nil {
		http.Error(w, "email receiver is not configured", http.StatusServiceUnavailable)
		return
	}
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") || s.Verify(r.Context(), strings.TrimPrefix(header, "Bearer ")) != nil {
		http.Error(w, "invalid Pub/Sub identity", http.StatusUnauthorized)
		return
	}
	var envelope struct {
		Message struct {
			Data string `json:"data"`
		} `json:"message"`
	}
	if e := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&envelope); e != nil {
		http.Error(w, "invalid notification", http.StatusBadRequest)
		return
	}
	data, e := base64.StdEncoding.DecodeString(envelope.Message.Data)
	if e != nil {
		data, e = base64.RawURLEncoding.DecodeString(envelope.Message.Data)
	}
	var payload struct {
		Email     string `json:"emailAddress"`
		HistoryID string `json:"historyId"`
	}
	if e != nil || json.Unmarshal(data, &payload) != nil || !decimalID(payload.HistoryID) || payload.Email == "" {
		http.Error(w, "invalid mailbox notification", http.StatusBadRequest)
		return
	}
	if e = s.Store.Wake(r.Context(), strings.ToLower(payload.Email)); e != nil {
		http.Error(w, "cannot persist notification", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func decimalID(s string) bool {
	if s == "" || len(s) > 30 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
