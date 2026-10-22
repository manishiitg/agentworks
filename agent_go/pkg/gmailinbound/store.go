package gmailinbound

import (
	"context"
	"database/sql"
	"encoding/json"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"time"
)

type Store struct{ db *sql.DB }
type Inbox struct {
	ID            string `json:"id"`
	ProfileID     string `json:"profile_id"`
	ProjectID     string `json:"project_id"`
	WorkspacePath string `json:"workspace_path"`
	OwnerID       string `json:"-"`
	ConnectionID  string `json:"-"`
	Address       string `json:"address"`
	CreatedAt     int64  `json:"created_at"`
}
type Item struct {
	Sequence int64 `json:"sequence"`
	Message
}
type Page struct {
	Messages   []Item `json:"messages"`
	NextCursor int64  `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;
 CREATE TABLE IF NOT EXISTS agent_inboxes(id TEXT PRIMARY KEY,profile TEXT NOT NULL,project TEXT NOT NULL,workspace TEXT NOT NULL,owner TEXT NOT NULL,connection TEXT NOT NULL,address TEXT NOT NULL,created_at INTEGER NOT NULL,active INTEGER NOT NULL DEFAULT 1,UNIQUE(profile,project,owner,connection));
 CREATE TABLE IF NOT EXISTS inbox_messages(sequence INTEGER PRIMARY KEY AUTOINCREMENT,inbox TEXT NOT NULL,message_id TEXT NOT NULL,thread TEXT NOT NULL,data TEXT NOT NULL,received_at INTEGER NOT NULL,UNIQUE(inbox,message_id));
 CREATE INDEX IF NOT EXISTS inbox_thread ON inbox_messages(inbox,thread,sequence);
 CREATE TABLE IF NOT EXISTS mailboxes(connection TEXT PRIMARY KEY,email TEXT NOT NULL,owner TEXT NOT NULL,processed INTEGER NOT NULL DEFAULT 0,next_sync INTEGER NOT NULL DEFAULT 0,error TEXT NOT NULL DEFAULT '',started_at INTEGER NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) EnsureInbox(ctx context.Context, i Inbox) (Inbox, error) {
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_inboxes(id,profile,project,workspace,owner,connection,address,created_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET workspace=excluded.workspace,owner=excluded.owner,address=excluded.address,active=1`, i.ID, i.ProfileID, i.ProjectID, i.WorkspacePath, i.OwnerID, i.ConnectionID, i.Address, time.Now().Unix())
	if err != nil {
		return i, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT created_at FROM agent_inboxes WHERE id=?`, i.ID).Scan(&i.CreatedAt)
	return i, err
}
func (s *Store) Inboxes(ctx context.Context) ([]Inbox, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,profile,project,workspace,owner,connection,address,created_at FROM agent_inboxes WHERE active=1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Inbox{}
	for rows.Next() {
		var i Inbox
		if err = rows.Scan(&i.ID, &i.ProfileID, &i.ProjectID, &i.WorkspacePath, &i.OwnerID, &i.ConnectionID, &i.Address, &i.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}
func (s *Store) StoreMessage(ctx context.Context, i Inbox, m Message) (bool, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return false, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO inbox_messages(inbox,message_id,thread,data,received_at) VALUES(?,?,?,?,?)`, i.ID, m.ID, m.ThreadID, string(b), m.ReceivedAt)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

// Each Watcher has its own cursor. Reading one inbox never consumes another caller's mail.
func (s *Store) Messages(ctx context.Context, inbox string, after int64, thread string, limit int) (Page, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	if after < 0 {
		after = 0
	}
	p := Page{Messages: []Item{}, NextCursor: after}
	rows, err := s.db.QueryContext(ctx, `SELECT sequence,data FROM inbox_messages WHERE inbox=? AND sequence>? AND (?='' OR thread=?) ORDER BY sequence LIMIT ?`, inbox, after, thread, thread, limit+1)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var i Item
		var raw string
		if err = rows.Scan(&i.Sequence, &raw); err != nil {
			return p, err
		}
		if len(p.Messages) == limit {
			p.HasMore = true
			break
		}
		if err = json.Unmarshal([]byte(raw), &i.Message); err != nil {
			return p, err
		}
		for n := range i.Attachments {
			i.Attachments[n].Data = ""
		}
		p.Messages = append(p.Messages, i)
		p.NextCursor = i.Sequence
	}
	return p, rows.Err()
}
func (s *Store) Message(ctx context.Context, inbox, id string) (Message, error) {
	var raw string
	var m Message
	err := s.db.QueryRowContext(ctx, `SELECT data FROM inbox_messages WHERE inbox=? AND message_id=?`, inbox, id).Scan(&raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &m)
	}
	return m, err
}

// Each personal connection has one shared poll position across all its owner's Crews.
func (s *Store) SetMailbox(ctx context.Context, id, owner, email string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO mailboxes(connection,owner,email,started_at) VALUES(?,?,?,?) ON CONFLICT(connection) DO NOTHING`, id, owner, email, time.Now().Unix())
	return err
}
func (s *Store) Mailboxes(ctx context.Context) ([]Mailbox, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT connection,owner,email,error,started_at,processed FROM mailboxes WHERE next_sync<=? ORDER BY next_sync`, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Mailbox{}
	for rows.Next() {
		var m Mailbox
		if err = rows.Scan(&m.ConnectionID, &m.OwnerID, &m.Email, &m.LastError, &m.Since, &m.Processed); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) MailboxStatus(ctx context.Context, id string) (Mailbox, error) {
	var m Mailbox
	err := s.db.QueryRowContext(ctx, `SELECT connection,owner,email,error,started_at,processed FROM mailboxes WHERE connection=?`, id).Scan(&m.ConnectionID, &m.OwnerID, &m.Email, &m.LastError, &m.Since, &m.Processed)
	return m, err
}
func (s *Store) Synced(ctx context.Context, m Mailbox) error {
	_, err := s.db.ExecContext(ctx, `UPDATE mailboxes SET started_at=?,processed=?,next_sync=?,error='' WHERE connection=?`, m.Since, time.Now().Unix(), time.Now().Add(time.Minute).Unix(), m.ConnectionID)
	return err
}
func (s *Store) SyncError(ctx context.Context, id string, e error) error {
	_, err := s.db.ExecContext(ctx, `UPDATE mailboxes SET error=?,next_sync=? WHERE connection=?`, e.Error(), time.Now().Add(time.Minute).Unix(), id)
	return err
}

// Remove retired mailbox pollers and inbox bindings. Stored mail is never reattached to a different source.
func (s *Store) Reconcile(ctx context.Context, active map[string]bool) error {
	all, err := s.Inboxes(ctx)
	if err != nil {
		return err
	}
	for _, i := range all {
		if !active[i.ID] {
			if _, err = s.db.ExecContext(ctx, `UPDATE agent_inboxes SET active=0 WHERE id=?`, i.ID); err != nil {
				return err
			}
		}
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM mailboxes WHERE connection NOT IN (SELECT connection FROM agent_inboxes WHERE active=1)`)
	return err
}

// The screen opens on recent mail; Watchers and MCP use the forward cursor above.
func (s *Store) RecentMessages(ctx context.Context, inbox string, before int64) (Page, error) {
	p := Page{Messages: []Item{}}
	rows, err := s.db.QueryContext(ctx, `SELECT sequence,data FROM inbox_messages WHERE inbox=? AND (?=0 OR (received_at,sequence)<(SELECT received_at,sequence FROM inbox_messages WHERE inbox=? AND sequence=?)) ORDER BY received_at DESC,sequence DESC LIMIT 51`, inbox, before, inbox, before)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var i Item
		var raw string
		if err = rows.Scan(&i.Sequence, &raw); err != nil {
			return p, err
		}
		if len(p.Messages) == 50 {
			p.HasMore = true
			break
		}
		if err = json.Unmarshal([]byte(raw), &i.Message); err != nil {
			return p, err
		}
		for n := range i.Attachments {
			i.Attachments[n].Data = ""
		}
		p.Messages = append(p.Messages, i)
		p.NextCursor = i.Sequence
	}
	return p, rows.Err()
}

// Stored Crew mail remains readable after disconnection, never through a new owner's binding.
func (s *Store) SavedInbox(ctx context.Context, profile, project, owner string) (Inbox, error) {
	var i Inbox
	err := s.db.QueryRowContext(ctx, `SELECT id,profile,project,workspace,owner,connection,address,created_at FROM agent_inboxes WHERE profile=? AND project=? AND owner=? ORDER BY created_at DESC LIMIT 1`, profile, project, owner).Scan(&i.ID, &i.ProfileID, &i.ProjectID, &i.WorkspacePath, &i.OwnerID, &i.ConnectionID, &i.Address, &i.CreatedAt)
	return i, err
}
