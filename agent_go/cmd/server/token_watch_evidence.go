package server

import (
	"context"
	"database/sql"
	"log"
	"strings"
	"time"
)

// This is diagnostic evidence only: it never changes the ActiveFamily verdict.
// PLAT-746-512: a generic revoked-or-expired reason could not distinguish a
// short access expiry from loss of the whole sign-in family after an incident.
func logTokenWatchOAuthExpiry(ctx context.Context, tokenID string) {
	if !strings.HasPrefix(tokenID, "oauth-") {
		return
	}
	prefix := tokenID
	if len(prefix) > 16 {
		prefix = prefix[:16]
	}
	checked := time.Now().UTC().Format(time.RFC3339Nano)
	store, err := openMCPOAuthStore()
	if err != nil {
		log.Printf("[TOKEN_WATCH] expiry_evidence token=%s checked_at=%s unavailable=%q", prefix, checked, err)
		return
	}
	defer store.Close()
	var access, refresh, revoked sql.NullInt64
	err = store.db.QueryRowContext(ctx, `SELECT MAX(CASE WHEN kind='access' THEN expires_at END), MAX(CASE WHEN kind='refresh' THEN expires_at END), MAX(revoked_at) FROM tokens WHERE family_id=?`, strings.TrimPrefix(tokenID, "oauth-")).Scan(&access, &refresh, &revoked)
	if err != nil {
		log.Printf("[TOKEN_WATCH] expiry_evidence token=%s checked_at=%s unavailable=%q", prefix, checked, err)
		return
	}
	stamp := func(value sql.NullInt64) string {
		if !value.Valid {
			return "unavailable"
		}
		return time.Unix(value.Int64, 0).UTC().Format(time.RFC3339)
	}
	log.Printf("[TOKEN_WATCH] expiry_evidence token=%s reason=no_active_access access_expires_at=%s family_refresh_expires_at=%s revoked_at=%s checked_at=%s", prefix, stamp(access), stamp(refresh), stamp(revoked), checked)
}
