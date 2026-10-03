package gmailinbound

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	_ = f.Close()
	db, e := sql.Open("sqlite", path)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	_, e = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;
CREATE TABLE IF NOT EXISTS mailboxes(connection TEXT PRIMARY KEY,email TEXT NOT NULL,cursor TEXT NOT NULL DEFAULT '',generation INTEGER NOT NULL DEFAULT 1,processed INTEGER NOT NULL DEFAULT 0,renew_at INTEGER NOT NULL DEFAULT 0,next_sync INTEGER NOT NULL DEFAULT 0,error TEXT NOT NULL DEFAULT '',started_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS routes(id TEXT PRIMARY KEY,owner TEXT NOT NULL,workspace TEXT NOT NULL,connection TEXT NOT NULL,address TEXT NOT NULL UNIQUE,data TEXT NOT NULL,enabled INTEGER NOT NULL,UNIQUE(owner,workspace));
CREATE TABLE IF NOT EXISTS sender_consents(route TEXT PRIMARY KEY,owner TEXT NOT NULL,config_hash TEXT NOT NULL,approved_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS deliveries(id TEXT PRIMARY KEY,route TEXT NOT NULL,message TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'staged',session TEXT NOT NULL DEFAULT '',response TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',created_at INTEGER NOT NULL,received_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS delivery_pending ON deliveries(status,created_at);
CREATE INDEX IF NOT EXISTS delivery_thread ON deliveries(route,json_extract(message,'$.thread_id'));
UPDATE deliveries SET status='uncertain',error='Server stopped during execution; inspect the saved app thread before retrying' WHERE status IN ('running','sending');`)
	if e != nil {
		_ = db.Close()
		return nil, e
	}
	// Older inbox databases predate named rules. Keep every existing delivery
	// and dedup key while adding optional admission metadata.
	for _, migration := range []struct{ name, sql string }{
		{"rule_id", "ALTER TABLE deliveries ADD COLUMN rule_id TEXT NOT NULL DEFAULT ''"},
		{"rule_name", "ALTER TABLE deliveries ADD COLUMN rule_name TEXT NOT NULL DEFAULT ''"},
	} {
		var count int
		if e = db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('deliveries') WHERE name=?", migration.name).Scan(&count); e == nil && count == 0 {
			_, e = db.Exec(migration.sql)
		}
		if e != nil {
			_ = db.Close()
			return nil, e
		}
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) SaveRoute(ctx context.Context, r Route, email string) error {
	filters, err := NormalizeFilters(r.Filters)
	if err != nil {
		return err
	}
	r.Filters = filters
	r.Rules, err = NormalizeRules(r.Rules, r.WorkflowTrigger)
	if err != nil {
		return err
	}
	r.SelectedRuleID = ""
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	// Any authority-bearing edit invalidates consent atomically. Restoring an
	// earlier policy later cannot revive a revoked approval.
	if _, e = tx.ExecContext(ctx, `DELETE FROM sender_consents WHERE route=? AND (owner<>? OR config_hash<>?)`, r.ID, r.OwnerID, SenderPolicyHash(r)); e != nil {
		return e
	}
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO routes(id,owner,workspace,connection,address,data,enabled) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET connection=excluded.connection,address=excluded.address,data=excluded.data,enabled=excluded.enabled`, r.ID, r.OwnerID, r.WorkspacePath, r.ConnectionID, r.Address, string(b), r.Enabled)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO mailboxes(connection,email,started_at) VALUES(?,?,?) ON CONFLICT(connection) DO UPDATE SET email=excluded.email,generation=generation+1,next_sync=0`, r.ConnectionID, email, time.Now().Unix())
	if e != nil {
		return e
	}
	if !r.Enabled {
		if _, e = tx.ExecContext(ctx, `UPDATE deliveries SET status='rejected',error='Email route disabled' WHERE route=? AND status IN ('staged','pending','reply')`, r.ID); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) Routes(ctx context.Context) ([]Route, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT data FROM routes ORDER BY address`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Route{}
	for rows.Next() {
		var b string
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var r Route
		if e = json.Unmarshal([]byte(b), &r); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) Wake(ctx context.Context, email string) error {
	_, e := s.db.ExecContext(ctx, `UPDATE mailboxes SET generation=generation+1,next_sync=MIN(next_sync,?) WHERE email=?`, time.Now().Unix(), email)
	return e
}
func (s *Store) Mailboxes(ctx context.Context) ([]Mailbox, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT connection,email,cursor,generation,renew_at,error,started_at,processed FROM mailboxes WHERE next_sync<=? AND connection IN (SELECT connection FROM routes WHERE enabled=1) ORDER BY next_sync LIMIT 100`, time.Now().Unix())
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Mailbox{}
	for rows.Next() {
		var m Mailbox
		if e = rows.Scan(&m.ConnectionID, &m.Email, &m.Cursor, &m.Generation, &m.RenewAt, &m.LastError, &m.Since, &m.Processed); e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) MailboxStatus(ctx context.Context, id string) (Mailbox, error) {
	var m Mailbox
	e := s.db.QueryRowContext(ctx, `SELECT connection,email,cursor,generation,renew_at,error,started_at,processed FROM mailboxes WHERE connection=?`, id).Scan(&m.ConnectionID, &m.Email, &m.Cursor, &m.Generation, &m.RenewAt, &m.LastError, &m.Since, &m.Processed)
	return m, e
}
func (s *Store) Watch(ctx context.Context, id, cursor string, renewAt int64) error {
	// A renewal must never replace an unprocessed cursor with the current
	// history position. Only a brand-new mailbox starts at registration.
	_, e := s.db.ExecContext(ctx, `UPDATE mailboxes SET cursor=CASE WHEN cursor='' THEN ? ELSE cursor END,renew_at=?,error='' WHERE connection=?`, cursor, renewAt, id)
	return e
}
func (s *Store) Synced(ctx context.Context, m Mailbox, cursor string) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	_, e = tx.ExecContext(ctx, `UPDATE mailboxes SET cursor=?,started_at=?,processed=?,next_sync=CASE WHEN generation=? THEN ? ELSE 0 END,error='' WHERE connection=?`, cursor, m.Since, m.Generation, m.Generation, time.Now().Add(5*time.Minute).Unix(), m.ConnectionID)
	if e != nil {
		return e
	}
	// Recovery lists can arrive newest-first. Publish the whole sync batch
	// together, then dispatch by Gmail's receipt time to preserve conversation order.
	_, e = tx.ExecContext(ctx, `UPDATE deliveries SET status='pending' WHERE status='staged' AND route IN(SELECT id FROM routes WHERE connection=?)`, m.ConnectionID)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) SyncError(ctx context.Context, id string, err error) error {
	_, e := s.db.ExecContext(ctx, `UPDATE mailboxes SET error=?,next_sync=? WHERE connection=?`, err.Error(), time.Now().Add(time.Minute).Unix(), id)
	return e
}
func (s *Store) Enqueue(ctx context.Context, r Route, m Message) error {
	return s.EnqueueAuthorized(ctx, r, m, nil)
}

// Select and reserve the first matching authorized rule atomically. Redelivery
// keeps its original rule; a later configuration change never replays an email.
func (s *Store) EnqueueAuthorized(ctx context.Context, r Route, m Message, authorize func(context.Context, Route, Message) error) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	// A redelivery remains harmless even while the queue is full.
	var exists bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM deliveries WHERE id=?)`, r.ID+":"+m.ID).Scan(&exists); e != nil {
		return e
	}
	if exists {
		return tx.Commit()
	}
	candidates := []Route{r}
	if len(r.Rules) > 0 {
		candidates = nil
		for _, rule := range r.Rules {
			if rule.IsEnabled() {
				candidate := r
				candidate.SelectedRuleID = rule.ID
				candidates = append(candidates, candidate)
			}
		}
	}
	reason := "No enabled email rule matched"
	selectedID, selectedName := "", ""
	authorized := false
	for _, candidate := range candidates {
		if authorize != nil {
			if !candidate.AcceptsMessageKind(m) || authorize(ctx, candidate, m) != nil {
				continue
			}
		}
		authorized = true
		mismatch := candidate.FilterMismatch(m)
		if required, allRules := candidate.NewThreadScope(); mismatch == "" && required {
			var accepted bool
			if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM deliveries WHERE route=? AND json_extract(message,'$.thread_id')=? AND status!='filtered' AND (? OR rule_id=?))`, r.ID, m.ThreadID, allRules, candidate.SelectedRuleID).Scan(&accepted); e != nil {
				return e
			}
			if accepted {
				mismatch = "New threads only: this thread has already been accepted"
			}
		}
		if mismatch == "" {
			reason = ""
			selectedID = candidate.SelectedRuleID
			if rule, _ := candidate.SelectedRule(); rule != nil {
				selectedName = rule.Name
			}
			break
		}
		if len(r.Rules) == 0 {
			reason = mismatch
		}
	}
	if authorize != nil && !authorized {
		return tx.Commit() // Preserve the existing no-history behavior for unauthorized mail.
	}
	if reason == "" {
		var count int
		if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deliveries WHERE status IN ('staged','pending','running','reply','sending')`).Scan(&count); e != nil {
			return e
		}
		if count >= 10000 {
			return fmt.Errorf("email delivery queue is full")
		}
	}
	b, e := json.Marshal(m)
	if e != nil {
		return e
	}
	receivedAt := m.ReceivedAt
	if receivedAt == 0 {
		receivedAt = time.Now().UnixMilli()
	}
	status := "staged"
	if reason != "" {
		status = "filtered"
	}
	_, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO deliveries(id,route,message,status,error,created_at,received_at,rule_id,rule_name) VALUES(?,?,?,?,?,?,?,?,?)`, r.ID+":"+m.ID, r.ID, string(b), status, reason, time.Now().Unix(), receivedAt, selectedID, selectedName)
	if e != nil {
		return e
	}
	return tx.Commit()
}

// Recheck changed filters before execution. A queued message may be the first
// accepted message in its thread; do not count that message or later messages.
func (s *Store) FilterReason(ctx context.Context, d Delivery) (string, error) {
	if reason := d.Route.FilterMismatch(d.Message); reason != "" {
		return reason, nil
	}
	required, allRules := d.Route.NewThreadScope()
	if !required {
		return "", nil
	}
	var earlier bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM deliveries a JOIN deliveries d ON d.id=? WHERE a.route=d.route AND json_extract(a.message,'$.thread_id')=json_extract(d.message,'$.thread_id') AND a.status!='filtered' AND (? OR a.rule_id=d.rule_id) AND (a.received_at<d.received_at OR (a.received_at=d.received_at AND a.rowid<d.rowid)))`, d.ID, allRules).Scan(&earlier)
	if earlier {
		return "New threads only: this thread has already been accepted", err
	}
	return "", err
}

// Keep compact IDs for durable deduplication, while releasing message bodies
// and final responses after 30 days. App conversation retention is separate.
func (s *Store) PruneContent(ctx context.Context) error {
	_, e := s.db.ExecContext(ctx, `UPDATE deliveries SET message=json_set(message,'$.body','','$.attachments',json('[]')),response='' WHERE status NOT IN ('staged','pending','running','reply','sending') AND created_at<? AND (response!='' OR json_extract(message,'$.body')!='' OR json_array_length(message,'$.attachments')>0)`, time.Now().Add(-30*24*time.Hour).Unix())
	return e
}
func (s *Store) Claim(ctx context.Context) (Delivery, bool, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Delivery{}, false, e
	}
	defer tx.Rollback()
	var d Delivery
	var mb, rb string
	// Replies and subsequent turns in one email conversation stay ordered.
	e = tx.QueryRowContext(ctx, `SELECT d.id,r.data,d.message,d.session,d.response,d.status,d.rule_id,d.rule_name FROM deliveries d JOIN routes r ON r.id=d.route WHERE d.status IN ('pending','reply') AND NOT EXISTS(SELECT 1 FROM deliveries a WHERE a.status IN ('running','sending') AND a.route=d.route AND json_extract(a.message,'$.thread_id')=json_extract(d.message,'$.thread_id')) ORDER BY d.received_at,d.rowid LIMIT 1`).Scan(&d.ID, &rb, &mb, &d.SessionID, &d.Response, &d.Status, &d.RuleID, &d.RuleName)
	if errors.Is(e, sql.ErrNoRows) {
		return d, false, nil
	}
	if e != nil {
		return d, false, e
	}
	if e = json.Unmarshal([]byte(rb), &d.Route); e != nil {
		return d, false, e
	}
	if e = json.Unmarshal([]byte(mb), &d.Message); e != nil {
		return d, false, e
	}
	d.Route.SelectedRuleID = d.RuleID
	status := "running"
	if d.Status == "reply" {
		status = "sending"
	}
	_, e = tx.ExecContext(ctx, `UPDATE deliveries SET status=? WHERE id=?`, status, d.ID)
	if e != nil {
		return d, false, e
	}
	e = tx.Commit()
	return d, e == nil, e
}
func (s *Store) Finish(ctx context.Context, d Delivery, status string, err error) error {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	_, e := s.db.ExecContext(ctx, `UPDATE deliveries SET status=?,session=?,response=?,error=? WHERE id=?`, status, d.SessionID, d.Response, msg, d.ID)
	return e
}

type DeliveryStatus struct {
	RuleID    string `json:"rule_id,omitempty"`
	RuleName  string `json:"rule_name,omitempty"`
	ID        string `json:"id"`
	Status    string `json:"status"`
	SessionID string `json:"session_id"`
	Error     string `json:"error,omitempty"`
}

func (s *Store) History(ctx context.Context, routeID string) ([]DeliveryStatus, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT id,status,session,error,rule_id,rule_name FROM deliveries WHERE route=? ORDER BY rowid DESC LIMIT 30`, routeID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []DeliveryStatus{}
	for rows.Next() {
		var d DeliveryStatus
		if e = rows.Scan(&d.ID, &d.Status, &d.SessionID, &d.Error, &d.RuleID, &d.RuleName); e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
