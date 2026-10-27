package services

import (
	"context"
	"crypto/sha256"
	"net/http"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

// SlackBotIdentity is presentation metadata, never a credential or permission.
// Authentication does not establish that incoming events are subscribed/delivered.
type SlackBotIdentity struct {
	Name      string `json:"name,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Verified  bool   `json:"verified"`
}

type slackBotIdentityEntry struct {
	identity SlackBotIdentity
	expires  time.Time
}

type slackBotIdentityCache struct {
	sync.Mutex
	entries map[[32]byte]slackBotIdentityEntry
}

// BotIdentity reads the identity of an already-admitted saved connection. Callers
// must apply existing connection visibility first. Token rotation changes the key;
// the cache never stores permissions or returns tokens to the caller.
func (s *SlackService) BotIdentity(ctx context.Context, conn SlackConnection) SlackBotIdentity {
	if !conn.Enabled || conn.BotToken == "" {
		return SlackBotIdentity{}
	}
	key := sha256.Sum256([]byte(conn.BotToken))
	s.identities.Lock()
	entry, ok := s.identities.entries[key]
	s.identities.Unlock()
	if ok && time.Now().Before(entry.expires) {
		return entry.identity
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	identity := readSlackBotIdentity(ctx, slack.New(conn.BotToken, slack.OptionHTTPClient(http.DefaultClient)))
	ttl := 30 * time.Second
	if identity.Verified {
		ttl = 10 * time.Minute
	}
	s.identities.Lock()
	if s.identities.entries == nil {
		s.identities.entries = make(map[[32]byte]slackBotIdentityEntry)
	}
	for cachedKey, cached := range s.identities.entries {
		if time.Now().After(cached.expires) {
			delete(s.identities.entries, cachedKey)
		}
	}
	s.identities.entries[key] = slackBotIdentityEntry{identity: identity, expires: time.Now().Add(ttl)}
	s.identities.Unlock()
	return identity
}

func readSlackBotIdentity(ctx context.Context, client *slack.Client) SlackBotIdentity {
	response, err := client.AuthTestContext(ctx)
	if err != nil || response == nil {
		return SlackBotIdentity{}
	}
	return SlackBotIdentity{Name: response.User, Workspace: response.Team, Verified: true}
}
