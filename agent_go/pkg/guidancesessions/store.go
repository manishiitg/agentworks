// Package guidancesessions records delivery of versioned reference topics.
// A session is a cache receipt, never an authorization or proof of reading.
package guidancesessions

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
	_ "modernc.org/sqlite"
)

const IdleExpiry = 30 * 24 * time.Hour
const maxSessions = 4096
const maxCallerSessions = 64

type Store struct{ db *sql.DB }
type Topic struct {
	Name        string     `json:"topic"`
	Version     string     `json:"version"`
	Status      string     `json:"status"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
}
type Receipt struct {
	SessionID string  `json:"guidance_session_id,omitempty"`
	Advisory  bool    `json:"advisory"`
	Topics    []Topic `json:"topics"`
	Fetch     string  `json:"fetch"`
}

func Open(path string) (*Store, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("guidance storage must be absolute")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	for _, p := range []string{dir, path} {
		info, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil && (info.Mode()&os.ModeSymlink != 0 || p == path && !info.Mode().IsRegular()) {
			return nil, errors.New("guidance storage must be a private regular file")
		}
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err := os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteopen.DSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS guidance_sessions (
 id TEXT PRIMARY KEY, caller TEXT NOT NULL, last_used INTEGER NOT NULL,
 deliveries INTEGER NOT NULL DEFAULT 0, missing_hits INTEGER NOT NULL DEFAULT 0,
 stale_hits INTEGER NOT NULL DEFAULT 0, validator_failures INTEGER NOT NULL DEFAULT 0);
 CREATE INDEX IF NOT EXISTS guidance_caller ON guidance_sessions(caller,last_used);
 CREATE TABLE IF NOT EXISTS guidance_topics (
 session_id TEXT NOT NULL REFERENCES guidance_sessions(id) ON DELETE CASCADE,
 topic TEXT NOT NULL, version TEXT NOT NULL, delivered_at INTEGER NOT NULL,
 PRIMARY KEY(session_id,topic));
 CREATE TABLE IF NOT EXISTS guidance_stats (id INTEGER PRIMARY KEY CHECK(id=1), deliveries INTEGER NOT NULL DEFAULT 0, missing_hits INTEGER NOT NULL DEFAULT 0, stale_hits INTEGER NOT NULL DEFAULT 0, validator_failures INTEGER NOT NULL DEFAULT 0);
 INSERT OR IGNORE INTO guidance_stats(id) VALUES(1);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// Check atomically prunes inactive contexts and updates a receipt. create is
// allowed only for preparation, and delivered only for full bodies in its reply.
// Invalid/expired/cross-caller handles all have the same missing status.
func (s *Store) Check(ctx context.Context, caller, handle string, create bool, versions map[string]string, delivered map[string]bool, validatorFailure bool, now time.Time) (Receipt, error) {
	result := Receipt{Advisory: true, Topics: []Topic{}, Fetch: "Reload with help action=skills topics=[topic] (omit known_versions after context loss); retain guidance_session_id per conversation. Delivery does not prove reading."}
	if caller == "" || len(versions) > 64 {
		return result, errors.New("invalid guidance context")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	// Delete explicitly as well as using FK cascade, for databases opened with
	// custom SQLite settings that disable foreign keys.
	if _, err = tx.ExecContext(ctx, `DELETE FROM guidance_topics WHERE session_id IN (SELECT id FROM guidance_sessions WHERE last_used <= ?)`, now.Add(-IdleExpiry).Unix()); err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM guidance_sessions WHERE last_used <= ?`, now.Add(-IdleExpiry).Unix()); err != nil {
		return result, err
	}
	id := ""
	if len(handle) == 68 && handle[:4] == "awg_" {
		var stored string
		err = tx.QueryRowContext(ctx, `SELECT id FROM guidance_sessions WHERE id=? AND caller=?`, hash(handle), hash(caller)).Scan(&stored)
		if err == nil {
			id = stored
			result.SessionID = handle
		} else if !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
	}
	if id == "" && create {
		random := make([]byte, 32)
		if _, err = rand.Read(random); err != nil {
			return result, err
		}
		result.SessionID = "awg_" + hex.EncodeToString(random)
		id = hash(result.SessionID)
		// Retain at most 64 conversations per connection and 4096 per server.
		for _, limit := range []struct {
			where string
			args  []any
			keep  int
		}{{"WHERE caller=?", []any{hash(caller)}, maxCallerSessions - 1}, {"", nil, maxSessions - 1}} {
			query := `SELECT id FROM guidance_sessions ` + limit.where + ` ORDER BY last_used DESC,id LIMIT -1 OFFSET ?`
			args := append(limit.args, limit.keep)
			if _, err = tx.ExecContext(ctx, `DELETE FROM guidance_topics WHERE session_id IN (`+query+`)`, args...); err != nil {
				return result, err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM guidance_sessions WHERE id IN (`+query+`)`, args...); err != nil {
				return result, err
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO guidance_sessions(id,caller,last_used) VALUES(?,?,?)`, id, hash(caller), now.Unix()); err != nil {
			return result, err
		}
	}
	deliveries, missing, stale := 0, 0, 0
	for name, version := range versions {
		topic := Topic{Name: name, Version: version, Status: "missing"}
		if id != "" {
			var previous string
			var when int64
			err = tx.QueryRowContext(ctx, `SELECT version,delivered_at FROM guidance_topics WHERE session_id=? AND topic=?`, id, name).Scan(&previous, &when)
			if err == nil {
				stamp := time.Unix(when, 0).UTC()
				topic.DeliveredAt = &stamp
				topic.Status = "stale"
				if previous == version {
					topic.Status = "delivered_to_client"
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				return result, err
			}
			counter := ""
			if delivered[name] {
				if _, err = tx.ExecContext(ctx, `INSERT INTO guidance_topics VALUES(?,?,?,?) ON CONFLICT(session_id,topic) DO UPDATE SET version=excluded.version,delivered_at=excluded.delivered_at`, id, name, version, now.Unix()); err != nil {
					return result, err
				}
				stamp := now.UTC()
				topic.DeliveredAt = &stamp
				topic.Status = "delivered_to_client"
				counter = "deliveries"
			} else if topic.Status == "missing" {
				counter = "missing_hits"
			} else if topic.Status == "stale" {
				counter = "stale_hits"
			}
			if counter != "" {
				if _, err = tx.ExecContext(ctx, `UPDATE guidance_sessions SET `+counter+`=`+counter+`+1 WHERE id=?`, id); err != nil {
					return result, err
				}
			}
		}
		if delivered[name] && id != "" {
			deliveries++
		} else if topic.Status == "missing" {
			missing++
		} else if topic.Status == "stale" {
			stale++
		}
		result.Topics = append(result.Topics, topic)
	}
	if id != "" {
		failures := 0
		if validatorFailure {
			failures = 1
		}
		if _, err = tx.ExecContext(ctx, `UPDATE guidance_sessions SET last_used=?,validator_failures=validator_failures+? WHERE id=?`, now.Unix(), failures, id); err != nil {
			return result, err
		}
	}
	failures := 0
	if validatorFailure {
		failures = 1
	}
	if _, err = tx.ExecContext(ctx, `UPDATE guidance_stats SET deliveries=deliveries+?,missing_hits=missing_hits+?,stale_hits=stale_hits+?,validator_failures=validator_failures+? WHERE id=1`, deliveries, missing, stale, failures); err != nil {
		return result, err
	}
	err = tx.Commit()
	return result, err
}

type Stats struct{ Deliveries, MissingHits, StaleHits, ValidatorFailures int64 }

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var counts Stats
	err := s.db.QueryRowContext(ctx, `SELECT deliveries,missing_hits,stale_hits,validator_failures FROM guidance_stats WHERE id=1`).Scan(&counts.Deliveries, &counts.MissingHits, &counts.StaleHits, &counts.ValidatorFailures)
	return counts, err
}
