package gmailinbound

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Consent is private server state, never a field that Builder can write into
// a route or workflow manifest. The digest binds target, mailbox, filters,
// rule actions, enabled state and reply permission. Renaming is cosmetic.
func SenderPolicyHash(r Route) string {
	r.Name, r.EnabledAt, r.SelectedRuleID = "", 0, ""
	data, _ := json.Marshal(r) // Route contains only JSON-safe concrete types.
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

type SenderConsentStatus struct {
	Required      bool     `json:"required"`
	Approved      bool     `json:"approved"`
	ConfigHash    string   `json:"config_hash"`
	Senders       []string `json:"senders"`
	BlockedReason string   `json:"blocked_reason,omitempty"`
}

func additionalSenders(r Route, ownerEmail string) []string {
	seen := map[string]bool{}
	add := func(filters *Filters) {
		if filters == nil {
			return
		}
		for _, sender := range filters.SenderAllowlist {
			if !strings.EqualFold(sender, ownerEmail) {
				seen[strings.ToLower(sender)] = true
			}
		}
	}
	add(r.Filters)
	for _, rule := range r.Rules {
		add(rule.Filters)
	}
	senders := make([]string, 0, len(seen))
	for sender := range seen {
		senders = append(senders, sender)
	}
	sort.Strings(senders)
	return senders
}

func validateConsentPolicy(r Route) error {
	if _, err := NormalizeFilters(r.Filters); err != nil {
		return err
	}
	for _, rule := range r.Rules {
		if _, err := NormalizeFilters(rule.Filters); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SenderConsentStatus(ctx context.Context, r Route, ownerEmail string) (SenderConsentStatus, error) {
	status := SenderConsentStatus{ConfigHash: SenderPolicyHash(r), Senders: additionalSenders(r, ownerEmail)}
	status.Required = len(status.Senders) > 0
	if err := validateConsentPolicy(r); err != nil {
		status.BlockedReason = err.Error()
		return status, nil
	}
	if !status.Required {
		return status, nil
	}
	var approvedHash, raw string
	err := s.db.QueryRowContext(ctx, `SELECT c.config_hash,r.data FROM sender_consents c JOIN routes r ON r.id=c.route WHERE c.route=? AND c.owner=? AND r.owner=?`, r.ID, r.OwnerID, r.OwnerID).Scan(&approvedHash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return status, nil
	}
	var current Route
	status.Approved = err == nil && json.Unmarshal([]byte(raw), &current) == nil && current.OwnerID == r.OwnerID && approvedHash == status.ConfigHash && SenderPolicyHash(current) == status.ConfigHash
	return status, err
}

// ConfirmSenderConsent is called only from the owner browser endpoint. Both
// the expected digest and current route are checked within the transaction.
func (s *Store) ConfirmSenderConsent(ctx context.Context, owner, routeID, expectedHash string, approve bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT data FROM routes WHERE id=? AND owner=?`, routeID, owner).Scan(&raw); err != nil {
		return fmt.Errorf("email trigger is unavailable to this owner: %w", err)
	}
	var current Route
	if json.Unmarshal([]byte(raw), &current) != nil || current.OwnerID != owner || expectedHash != SenderPolicyHash(current) {
		return fmt.Errorf("email configuration changed; review the current configuration before confirming")
	}
	if approve {
		if err := validateConsentPolicy(current); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO sender_consents(route,owner,config_hash,approved_at) VALUES(?,?,?,?) ON CONFLICT(route) DO UPDATE SET owner=excluded.owner,config_hash=excluded.config_hash,approved_at=excluded.approved_at`, routeID, owner, expectedHash, time.Now().Unix())
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM sender_consents WHERE route=? AND owner=?`, routeID, owner)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
