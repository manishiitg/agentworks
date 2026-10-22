package gmailinbound

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"
)

type Client interface {
	List(context.Context, int64, []string, bool) ([]string, error)
	Message(context.Context, string) (RawMessage, error)
}

var ErrMessageGone = errors.New("Gmail message no longer exists")

type Service struct {
	Enabled func(context.Context) bool
	Store   *Store
	Client  func(context.Context, Mailbox) (Client, error)
	Inboxes func(context.Context) ([]Inbox, error)
	Stored  func(context.Context, Inbox) error
	mu      sync.Mutex
	busy    map[string]bool
	wg      sync.WaitGroup
}

func (s *Service) Start(ctx context.Context) {
	s.busy = map[string]bool{}
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
	client, e := s.Client(ctx, m)
	if e != nil {
		return e
	}
	inboxes, e := s.Inboxes(ctx)
	if e != nil {
		return e
	}
	addresses := []string{}
	personal := false
	for _, i := range inboxes {
		if i.ConnectionID == m.ConnectionID && i.OwnerID == m.OwnerID {
			if i.PersonalGmail() {
				personal = true
			} else {
				addresses = append(addresses, i.Address)
			}
		}
	}
	ids, e := client.List(ctx, m.Since, addresses, personal)
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
		for _, inbox := range inboxes {
			if inbox.ConnectionID != m.ConnectionID || inbox.OwnerID != m.OwnerID {
				continue
			}
			if message.ReceivedAt < inbox.CreatedAt*1000 {
				continue
			}
			matched := inbox.PersonalGmail()
			if inbox.PersonalGmail() {
				// Alias queries also retain mail the owner sent to their Crew. That
				// must not widen the private received-mail source to Sent or Drafts.
				for _, label := range raw.LabelIDs {
					if label == "SENT" || label == "DRAFT" {
						matched = false
					}
				}
			}
			for _, to := range message.Recipients {
				if !inbox.PersonalGmail() && strings.EqualFold(to, inbox.Address) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			if s.Enabled != nil && !s.Enabled(ctx) {
				return nil
			}
			if _, err := s.Client(ctx, m); err != nil {
				return err
			}
			fresh, err := s.Store.StoreMessage(ctx, inbox, message)
			if err != nil {
				return err
			}
			// Retry waking even for a duplicate after a failed wake; the Watcher's own ledger deduplicates.
			if s.Stored != nil {
				if err = s.Stored(ctx, inbox); err != nil {
					return err
				}
			}
			if fresh {
				log.Printf("[AGENT-INBOX] stored inbox=%s message=%s thread=%s", inbox.ID, message.ID, message.ThreadID)
			}
		}
	}
	if m.Processed == 0 || m.LastError != "" || len(ids) > 0 {
		log.Printf("[AGENT-INBOX] checked connection=%s inboxes=%d matched=%d", m.ConnectionID, len(addresses), len(ids))
	}
	m.Since = syncStarted
	return s.Store.Synced(ctx, m)
}
