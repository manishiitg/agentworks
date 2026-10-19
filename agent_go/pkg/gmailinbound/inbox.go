package gmailinbound

import (
	"context"
	"database/sql"
	"errors"
)

// Inbox is a person's receiving identity. Product bindings reference it; neither
// a target ID nor an email can choose or change this identity.
type Inbox struct {
	ID           string `json:"id"`
	OwnerID      string `json:"owner_id"`
	ConnectionID string `json:"connection_id"`
	Address      string `json:"address"`
}

func (s *Store) Inbox(ctx context.Context, owner string) (*Inbox, error) {
	var i Inbox
	err := s.db.QueryRowContext(ctx, `SELECT id,owner,connection,address FROM inboxes WHERE owner=?`, owner).Scan(&i.ID, &i.OwnerID, &i.ConnectionID, &i.Address)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &i, err
}

func (s *Store) SaveInbox(ctx context.Context, i Inbox) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO inboxes(id,owner,connection,address) VALUES(?,?,?,?) ON CONFLICT(owner) DO UPDATE SET connection=excluded.connection,address=excluded.address`, i.ID, i.OwnerID, i.ConnectionID, i.Address)
	return err
}

func (i Inbox) Matches(r Route) bool {
	return i.OwnerID == r.OwnerID && i.ID == r.InboxID && i.ConnectionID == r.ConnectionID && i.Address == r.Address
}
