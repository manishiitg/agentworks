package browserrelay

import (
	"crypto/sha256"
	"errors"
	"log"
	"time"
)

type accountPairingGrant struct {
	User    string    `json:"account_id"`
	Account string    `json:"account"`
	Expires time.Time `json:"expires_at"`
}

// BeginAccountPairing creates a short-lived handoff, never an account API token.
func (m *Manager) BeginAccountPairing(user, account string) string {
	handoff := secret()
	hash := sha256.Sum256([]byte(handoff))
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.accountPairings == nil {
		m.accountPairings = make(map[[32]byte]accountPairingGrant)
	}
	for key, grant := range m.accountPairings {
		if time.Now().After(grant.Expires) {
			delete(m.accountPairings, key)
		}
	}
	m.accountPairings[hash] = accountPairingGrant{User: user, Account: account, Expires: time.Now().Add(5 * time.Minute)}
	log.Printf("[BROWSER_PAIR] event=handoff_issue user=%q grant=%x result=ready", user, hash[:6])
	return handoff
}

// AccountPairing previews identity or atomically consumes the same grant.
func (m *Manager) AccountPairing(handoff string, consume bool) (accountPairingGrant, error) {
	hash := sha256.Sum256([]byte(handoff))
	m.mu.Lock()
	defer m.mu.Unlock()
	event := "handoff_peek"
	if consume {
		event = "handoff_redeem"
	}
	grant, ok := m.accountPairings[hash]
	if !ok || !time.Now().Before(grant.Expires) {
		delete(m.accountPairings, hash)
		log.Printf("[BROWSER_PAIR] event=%s grant=%x result=expired_or_used", event, hash[:6])
		return accountPairingGrant{}, errors.New("This connection link expired or was already used. In AgentWorks, choose Connect browser again.")
	}
	if consume {
		delete(m.accountPairings, hash)
	}
	log.Printf("[BROWSER_PAIR] event=%s user=%q grant=%x result=accepted", event, grant.User, hash[:6])
	return grant, nil
}
