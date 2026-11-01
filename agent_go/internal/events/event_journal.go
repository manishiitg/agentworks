package events

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

// DurableEventJournal is the persistence boundary for the canonical structured
// chat stream. Append assigns a monotonic per-session sequence atomically and
// reports whether event_id was new; replaying the same event is idempotent.
type DurableEventJournal interface {
	Append(sessionID string, event Event) (persisted Event, inserted bool, err error)
	LoadTail(sessionID string, limit int) ([]Event, error)
	Close() error
}

type SQLiteEventJournal struct {
	db   *sql.DB
	path string
}

type EventJournalStats struct {
	Path      string
	SizeBytes int64
	Events    int64
}

type DurableEventJournalPageReader interface {
	ReadPage(sessionID string, opts DurableEventPageOptions) (DurableEventPage, error)
	DeleteSession(sessionID string) error
}

// DurableEventJournalBulkDeleter removes a workflow's chats in one transaction.
type DurableEventJournalBulkDeleter interface {
	DeleteSessions(sessionIDs []string) error
}

type DurableEventJournalMigrator interface {
	MigrationComplete(sessionID string) (bool, error)
	MarkMigrationComplete(sessionID string) error
}

// DurableEventJournalSessionProbe reports whether the journal already holds a
// session, and its durable tip, without loading any payloads.
type DurableEventJournalSessionProbe interface {
	KnownSession(sessionID string) (known bool, lastSequence int64, err error)
}

type DurableEventJournalOwnership interface {
	RegisterOwner(sessionID, ownerID string) error
	Owner(sessionID string) (string, error)
	// ReassignOwner replaces a recorded owner. Callers decide when that is right; the journal only records it.
	ReassignOwner(sessionID, ownerID string) error
}

type DurableEventPageOptions struct {
	Limit          int
	BeforeSequence int64
	AfterSequence  int64
	FromStart      bool
}

type DurableEventPage struct {
	Events                []Event
	Exists                bool
	HasOlder              bool
	HasNewer              bool
	OldestSequence        int64
	LatestSequence        int64
	JournalLatestSequence int64
}

func OpenSQLiteEventJournal(path string) (*SQLiteEventJournal, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("structured event journal path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	_, statErr := os.Stat(path)
	freshDatabase := errors.Is(statErr, os.ErrNotExist)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if freshDatabase {
		// Must precede the first table; existing files keep their mode and are
		// reported by maintenance as needing a one-time VACUUM instead.
		if _, err := db.Exec(`PRAGMA auto_vacuum=INCREMENTAL`); err != nil {
			db.Close()
			return nil, fmt.Errorf("initialize structured event journal: %w", err)
		}
	}
	for _, statement := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=FULL`,
		`PRAGMA busy_timeout=5000`,
		`CREATE TABLE IF NOT EXISTS structured_chat_events (
			session_id TEXT NOT NULL,
			sequence INTEGER NOT NULL,
			event_id TEXT NOT NULL,
			payload BLOB NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY (session_id, sequence),
			UNIQUE (session_id, event_id)
		)`,
		`CREATE TABLE IF NOT EXISTS structured_chat_sequences (
			session_id TEXT PRIMARY KEY,
			last_sequence INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS structured_chat_event_id ON structured_chat_events(event_id)`,
		`CREATE TABLE IF NOT EXISTS structured_chat_migrations (
			session_id TEXT PRIMARY KEY,
			migrated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS structured_chat_sessions (
			session_id TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, fmt.Errorf("initialize structured event journal: %w", err)
		}
	}
	_ = os.Chmod(path, 0o600)
	return &SQLiteEventJournal{db: db, path: path}, nil
}

func (j *SQLiteEventJournal) RegisterOwner(sessionID, ownerID string) error {
	if j == nil || j.db == nil || sessionID == "" || ownerID == "" {
		return nil
	}
	var existing string
	err := j.db.QueryRow(`SELECT owner_id FROM structured_chat_sessions WHERE session_id = ?`, sessionID).Scan(&existing)
	if err == nil && existing != ownerID {
		return fmt.Errorf("durable chat %s is already owned by another user", sessionID)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = j.db.Exec(`INSERT INTO structured_chat_sessions(session_id, owner_id, updated_at) VALUES(?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET updated_at = excluded.updated_at`, sessionID, ownerID, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (j *SQLiteEventJournal) ReassignOwner(sessionID, ownerID string) error {
	if j == nil || j.db == nil || sessionID == "" || ownerID == "" {
		return nil
	}
	_, err := j.db.Exec(`UPDATE structured_chat_sessions SET owner_id = ?, updated_at = ? WHERE session_id = ?`, ownerID, time.Now().UTC().Format(time.RFC3339Nano), sessionID)
	return err
}

func (j *SQLiteEventJournal) Owner(sessionID string) (string, error) {
	if j == nil || j.db == nil || sessionID == "" {
		return "", nil
	}
	var ownerID string
	err := j.db.QueryRow(`SELECT owner_id FROM structured_chat_sessions WHERE session_id = ?`, sessionID).Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return ownerID, err
}

func (j *SQLiteEventJournal) KnownSession(sessionID string) (bool, int64, error) {
	if j == nil || j.db == nil || sessionID == "" {
		return false, 0, nil
	}
	var lastSequence sql.NullInt64
	var owned int
	err := j.db.QueryRow(`SELECT
		(SELECT last_sequence FROM structured_chat_sequences WHERE session_id = ?),
		EXISTS(SELECT 1 FROM structured_chat_sessions WHERE session_id = ?)`, sessionID, sessionID).Scan(&lastSequence, &owned)
	if err != nil {
		return false, 0, err
	}
	return lastSequence.Valid || owned == 1, lastSequence.Int64, nil
}

func (j *SQLiteEventJournal) MigrationComplete(sessionID string) (bool, error) {
	if j == nil || j.db == nil || sessionID == "" {
		return false, nil
	}
	var present int
	err := j.db.QueryRow(`SELECT 1 FROM structured_chat_migrations WHERE session_id = ?`, sessionID).Scan(&present)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (j *SQLiteEventJournal) MarkMigrationComplete(sessionID string) error {
	if j == nil || j.db == nil || sessionID == "" {
		return nil
	}
	_, err := j.db.Exec(`INSERT INTO structured_chat_migrations(session_id, migrated_at) VALUES(?, ?)
		ON CONFLICT(session_id) DO NOTHING`, sessionID, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (j *SQLiteEventJournal) Append(sessionID string, event Event) (Event, bool, error) {
	if j == nil || j.db == nil || sessionID == "" || event.ID == "" {
		return Event{}, false, fmt.Errorf("structured event journal requires session_id and event_id")
	}
	// Written before the transaction so file IO never holds the database; a
	// replayed duplicate simply rewrites the same artifact content.
	event, err := j.spillOversizedEvent(sessionID, event)
	if err != nil {
		return Event{}, false, err
	}
	tx, err := j.db.BeginTx(context.Background(), &sql.TxOptions{})
	if err != nil {
		return Event{}, false, err
	}
	defer tx.Rollback()
	var existing []byte
	if err := tx.QueryRow(`SELECT payload FROM structured_chat_events WHERE session_id = ? AND event_id = ?`, sessionID, event.ID).Scan(&existing); err == nil {
		var persisted Event
		if json.Unmarshal(existing, &persisted) != nil {
			return Event{}, false, fmt.Errorf("decode existing structured event")
		}
		return persisted, false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Event{}, false, err
	}

	var last int64
	if err := tx.QueryRow(`SELECT last_sequence FROM structured_chat_sequences WHERE session_id = ?`, sessionID).Scan(&last); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Event{}, false, err
	}
	next := last + 1
	// Live-only streaming events consume in-memory sequence numbers without
	// paying a durable transaction. Preserve their gap in the next semantic
	// boundary so restart cursors remain monotonic across the full live stream.
	if event.Sequence > next {
		next = event.Sequence
	}
	event.Sequence = next
	event.SessionID = sessionID
	payload, err := json.Marshal(event)
	if err != nil {
		return Event{}, false, err
	}
	if _, err := tx.Exec(`INSERT INTO structured_chat_events(session_id, sequence, event_id, payload, created_at) VALUES(?, ?, ?, ?, ?)`,
		sessionID, next, event.ID, payload, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return Event{}, false, err
	}
	if _, err := tx.Exec(`INSERT INTO structured_chat_sequences(session_id, last_sequence) VALUES(?, ?)
		ON CONFLICT(session_id) DO UPDATE SET last_sequence = excluded.last_sequence`, sessionID, next); err != nil {
		return Event{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Event{}, false, err
	}
	return event, true, nil
}

func (j *SQLiteEventJournal) Stats() (EventJournalStats, error) {
	if j == nil || j.db == nil {
		return EventJournalStats{}, fmt.Errorf("structured event journal is closed")
	}
	stats := EventJournalStats{Path: j.path}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(j.path + suffix)
		if err == nil {
			stats.SizeBytes += info.Size()
		} else if !os.IsNotExist(err) {
			return EventJournalStats{}, err
		}
	}
	if err := j.db.QueryRow(`SELECT COUNT(*) FROM structured_chat_events`).Scan(&stats.Events); err != nil {
		return EventJournalStats{}, err
	}
	return stats, nil
}

func (j *SQLiteEventJournal) Checkpoint() error {
	if j == nil || j.db == nil {
		return nil
	}
	_, err := j.db.Exec(`PRAGMA wal_checkpoint(PASSIVE)`)
	return err
}

func (j *SQLiteEventJournal) LoadTail(sessionID string, limit int) ([]Event, error) {
	if j == nil || j.db == nil || sessionID == "" {
		return []Event{}, nil
	}
	if limit <= 0 {
		limit = 1500
	}
	rows, err := j.db.Query(`SELECT payload FROM structured_chat_events WHERE session_id = ? ORDER BY sequence DESC LIMIT ?`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reversed := make([]Event, 0, limit)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var event Event
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, err
		}
		reversed = append(reversed, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]Event, len(reversed))
	for index := range reversed {
		result[len(reversed)-1-index] = reversed[index]
	}
	return result, nil
}

func (j *SQLiteEventJournal) ReadPage(sessionID string, opts DurableEventPageOptions) (DurableEventPage, error) {
	if j == nil || j.db == nil || sessionID == "" {
		return DurableEventPage{Events: []Event{}}, nil
	}
	if opts.BeforeSequence > 0 && (opts.AfterSequence > 0 || opts.FromStart) {
		return DurableEventPage{}, fmt.Errorf("before_sequence and after_sequence are mutually exclusive")
	}
	limit := opts.Limit
	if limit <= 0 || limit > 1000 {
		limit = 300
	}

	var total int
	var minimum, maximum sql.NullInt64
	if err := j.db.QueryRow(`SELECT COUNT(*), MIN(sequence), MAX(sequence) FROM structured_chat_events WHERE session_id = ?`, sessionID).Scan(&total, &minimum, &maximum); err != nil {
		return DurableEventPage{}, err
	}
	page := DurableEventPage{Events: []Event{}, Exists: total > 0}
	if total == 0 {
		return page, nil
	}
	page.JournalLatestSequence = maximum.Int64

	query := `SELECT payload FROM structured_chat_events WHERE session_id = ? ORDER BY sequence DESC LIMIT ?`
	args := []interface{}{sessionID, limit + 1}
	descending := true
	if opts.BeforeSequence > 0 {
		query = `SELECT payload FROM structured_chat_events WHERE session_id = ? AND sequence < ? ORDER BY sequence DESC LIMIT ?`
		args = []interface{}{sessionID, opts.BeforeSequence, limit + 1}
	} else if opts.AfterSequence > 0 || opts.FromStart {
		query = `SELECT payload FROM structured_chat_events WHERE session_id = ? AND sequence > ? ORDER BY sequence ASC LIMIT ?`
		args = []interface{}{sessionID, opts.AfterSequence, limit + 1}
		descending = false
	}

	rows, err := j.db.Query(query, args...)
	if err != nil {
		return DurableEventPage{}, err
	}
	defer rows.Close()
	loaded := make([]Event, 0, limit+1)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return DurableEventPage{}, err
		}
		var event Event
		if err := json.Unmarshal(payload, &event); err != nil {
			return DurableEventPage{}, err
		}
		loaded = append(loaded, event)
	}
	if err := rows.Err(); err != nil {
		return DurableEventPage{}, err
	}

	moreInQueryDirection := len(loaded) > limit
	if moreInQueryDirection {
		loaded = loaded[:limit]
	}
	if descending {
		for left, right := 0, len(loaded)-1; left < right; left, right = left+1, right-1 {
			loaded[left], loaded[right] = loaded[right], loaded[left]
		}
	}
	page.Events = loaded
	if len(loaded) == 0 {
		page.HasOlder = opts.AfterSequence > minimum.Int64
		page.HasNewer = opts.BeforeSequence > 0 && opts.BeforeSequence <= maximum.Int64
		return page, nil
	}
	page.OldestSequence = loaded[0].Sequence
	page.LatestSequence = loaded[len(loaded)-1].Sequence
	page.HasOlder = page.OldestSequence > minimum.Int64
	page.HasNewer = page.LatestSequence < maximum.Int64
	if opts.BeforeSequence > 0 {
		page.HasOlder = moreInQueryDirection
	}
	if opts.AfterSequence > 0 || opts.FromStart {
		page.HasNewer = moreInQueryDirection
	}
	return page, nil
}

func (j *SQLiteEventJournal) DeleteSession(sessionID string) error {
	return j.DeleteSessions([]string{sessionID})
}

func (j *SQLiteEventJournal) DeleteSessions(sessionIDs []string) error {
	if j == nil || j.db == nil || len(sessionIDs) == 0 {
		return nil
	}
	tx, err := j.db.BeginTx(context.Background(), &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, sessionID := range sessionIDs {
		if sessionID == "" {
			continue
		}
		for _, table := range []string{"structured_chat_events", "structured_chat_sequences", "structured_chat_migrations", "structured_chat_sessions"} {
			if _, err := tx.Exec(`DELETE FROM `+table+` WHERE session_id = ?`, sessionID); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	var errs []error
	for _, sessionID := range sessionIDs {
		if sessionID != "" {
			if err := j.removeSessionArtifacts(sessionID); err != nil {
				errs = append(errs, err)
			}
		}
	}
	// FULL synchronous WAL commits already make deletion durable. Checkpointing
	// the shared journal after every chat adds unrelated disk work to deletion
	// and blocks other chats on the single DB connection. Normal WAL/retention
	// maintenance reclaims the space.
	return errors.Join(errs...)
}

func (j *SQLiteEventJournal) Close() error {
	if j == nil || j.db == nil {
		return nil
	}
	return j.db.Close()
}

// DeleteEvents removes the given events of a session. Remaining events keep
// their sequence numbers, so readers paging with "since" are unaffected.
func (j *SQLiteEventJournal) DeleteEvents(sessionID string, eventIDs []string) (int64, error) {
	if j == nil || j.db == nil || sessionID == "" || len(eventIDs) == 0 {
		return 0, nil
	}
	tx, err := j.db.BeginTx(context.Background(), &sql.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	statement, err := tx.Prepare(`DELETE FROM structured_chat_events WHERE session_id = ? AND event_id = ?`)
	if err != nil {
		return 0, err
	}
	defer statement.Close()
	var deleted int64
	for _, id := range eventIDs {
		result, err := statement.Exec(sessionID, id)
		if err != nil {
			return 0, err
		}
		n, _ := result.RowsAffected()
		deleted += n
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return deleted, nil
}
