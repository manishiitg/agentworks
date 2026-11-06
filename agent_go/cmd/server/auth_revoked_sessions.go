package server

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"maps"
	"sync"
	"time"
)

// Signing out ends that sign-in on the server: a JWT stays valid until it expires, so its session id is refused from
// then on. A renewed token keeps its session's id, so the older token of the same sign-in ends with it; another
// sign-in of the same person has its own id and stays signed in. Every request reads memory; the store keeps the list
// over a restart. An entry lasts one session lifetime, past which no token of that session can still be valid.
var revokedSessions = struct {
	sync.RWMutex
	until map[string]time.Time
}{until: map[string]time.Time{}}

// sessionRevocationID names a token's sign-in: its session id, or the token's own hash when it has none (a token from
// before session ids, or one the gateway minted).
func sessionRevocationID(claims *UserClaims, token string) string {
	if claims.ID != "" {
		return claims.ID
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func sessionRevoked(id string) bool {
	revokedSessions.RLock()
	defer revokedSessions.RUnlock()
	_, revoked := revokedSessions.until[id]
	return revoked
}

// revokeSession takes effect at once in memory; a failed save only loses it at the next restart.
func revokeSession(id string) {
	now := time.Now()
	until := now.Add(sessionLifetime)
	revokedSessions.Lock()
	maps.DeleteFunc(revokedSessions.until, func(_ string, end time.Time) bool { return !end.After(now) })
	revokedSessions.until[id] = until
	revokedSessions.Unlock()
	store, err := openMCPOAuthStore()
	if err == nil {
		defer store.Close()
		if _, err = store.db.Exec(`DELETE FROM revoked_sessions WHERE expires_at<=?`, now.Unix()); err == nil {
			_, err = store.db.Exec(`INSERT OR REPLACE INTO revoked_sessions (id,expires_at) VALUES (?,?)`, id, until.Unix())
		}
	}
	if err != nil {
		log.Printf("[AUTH] Signed-out session not saved, it is refused until the next restart: %v", err)
	}
}

// loadRevokedSessions fills the list at server start.
func loadRevokedSessions() error {
	store, err := openMCPOAuthStore()
	if err != nil {
		return err
	}
	defer store.Close()
	rows, err := store.db.Query(`SELECT id,expires_at FROM revoked_sessions WHERE expires_at>?`, time.Now().Unix())
	if err != nil {
		return err
	}
	defer rows.Close()
	loaded := map[string]time.Time{}
	for rows.Next() {
		var id string
		var expires int64
		if err = rows.Scan(&id, &expires); err != nil {
			return err
		}
		loaded[id] = time.Unix(expires, 0)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	revokedSessions.Lock()
	revokedSessions.until = loaded
	revokedSessions.Unlock()
	log.Printf("[AUTH] Loaded %d signed-out sessions", len(loaded))
	return nil
}
