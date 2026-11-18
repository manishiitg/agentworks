package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
)

// The server state database (#3156): system state the server reads on many
// requests or for every Crew, kept as rows instead of whole JSON documents.
// It lives in the app's private state folder, outside workspace documents,
// so agents, file tools and Team sync never see it. One file per docs root:
// <state>/server-state/<hash of docs>/state.sqlite (directory 0700, file 0600).
//
// First table: the user directory, imported once from config/users.json.

var serverState struct {
	sync.Mutex
	db   *sql.DB
	file string
	// usersImported is false while the one-off user directory import waits for
	// the host (see userDirectoryImportReady); checked again every half minute.
	usersImported  bool
	usersCheckedAt time.Time
	usersWaitLog   bool
}

// userDirectoryImportReady gates the import: on a host with slots, the root
// slot-repair timer must already read accounts from this database, or new
// people would silently get no slot once users.json is gone. Until then the
// directory stays in config/users.json exactly as before.
var userDirectoryImportReady = slots.RepairReadsServerState

func serverStateDatabasePath() (string, error) {
	dir, err := sqliteopen.StateDirectory(fsutil.WorkspaceDocsRoot(), "server-state")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "state.sqlite"), nil
}

// openServerState returns the shared handle, creating the schema and running
// the one-off imports on first use. A failed import (the workspace API not up
// yet) returns an error and is retried by the next caller.
func openServerState(ctx context.Context) (*sql.DB, error) {
	file, err := serverStateDatabasePath()
	if err != nil {
		return nil, err
	}
	serverState.Lock()
	defer serverState.Unlock()
	if serverState.db != nil && serverState.file == file {
		if !serverState.usersImported && time.Since(serverState.usersCheckedAt) > 30*time.Second {
			if err := importUsersWhenReady(ctx, serverState.db, filepath.Dir(file)); err != nil {
				return nil, err
			}
		}
		return serverState.db, nil
	}
	dir := filepath.Dir(file)
	if err := agentworksclient.EnsurePrivateDirectory(dir); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	if info, e := os.Lstat(file); e == nil && !info.Mode().IsRegular() {
		return nil, errors.New("server state must be an ordinary private database")
	} else if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	err = f.Chmod(0o600)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteopen.DSN(file)+"&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS state_imports (name TEXT PRIMARY KEY, source_bytes INTEGER NOT NULL, rows INTEGER NOT NULL, imported_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS state_versions (name TEXT PRIMARY KEY, version INTEGER NOT NULL)`,
		// record is the account's JSON; id/username/disabled are columns for
		// single-row lookups and for the host's slot provisioning script.
		`CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY, username TEXT NOT NULL, email TEXT NOT NULL, disabled INTEGER NOT NULL, record TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS user_access_requests (key TEXT PRIMARY KEY, receipt TEXT NOT NULL)`,
	} {
		if _, err = db.ExecContext(ctx, ddl); err != nil {
			db.Close()
			return nil, err
		}
	}
	serverState.db, serverState.file, serverState.usersImported, serverState.usersWaitLog = db, file, false, false
	if err = importUsersWhenReady(ctx, db, dir); err != nil {
		serverState.db, serverState.file = nil, ""
		db.Close()
		return nil, err
	}
	return db, nil
}

// importUsersWhenReady runs under serverState's lock.
func importUsersWhenReady(ctx context.Context, db *sql.DB, dir string) error {
	serverState.usersCheckedAt = time.Now()
	var done int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM state_imports WHERE name='users'`).Scan(&done); err != nil {
		return err
	}
	if done == 0 && !userDirectoryImportReady() {
		if !serverState.usersWaitLog {
			log.Printf("[SERVER_STATE] import=waiting table=users reason=slot_timer_outdated")
			serverState.usersWaitLog = true
		}
		return nil
	}
	if err := importUserDirectoryOnce(ctx, db, dir); err != nil {
		return err
	}
	serverState.usersImported = true
	return nil
}

// usersFromDatabase opens the state database and says whether the user
// directory already lives there.
func usersFromDatabase(ctx context.Context) (*sql.DB, bool, error) {
	db, err := openServerState(ctx)
	if err != nil {
		return nil, false, err
	}
	serverState.Lock()
	imported := serverState.usersImported
	serverState.Unlock()
	return db, imported, nil
}

// importUserDirectoryOnce moves config/users.json into rows: read it through
// the workspace API, keep its bytes as a private backup, insert the rows and
// the import marker in one transaction, then delete the document. No reader
// ever falls back to the file.
func importUserDirectoryOnce(ctx context.Context, db *sql.DB, dir string) error {
	var done int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM state_imports WHERE name='users'`).Scan(&done); err != nil {
		return err
	}
	if done > 0 {
		return nil
	}
	const source = legacyUserDirectoryPath
	raw, exists, err := readFileFromWorkspaceQueryUncached(ctx, source, "", 0)
	if err != nil {
		return fmt.Errorf("import the user directory: %w", err)
	}
	parsed, err := decodeUserDirectory(raw)
	if err != nil {
		// Fail closed: an unreadable directory must not become an empty one.
		return fmt.Errorf("import the user directory: %w", err)
	}
	if exists {
		backup := filepath.Join(dir, "users.json.imported")
		if err := os.WriteFile(backup, []byte(raw), 0o600); err != nil {
			return err
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeUserDirectoryRows(ctx, tx, parsed); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO state_imports(name,source_bytes,rows) VALUES ('users',?,?)`, len(raw), len(parsed.Users)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("[SERVER_STATE] event=import table=users rows=%d access_requests=%d source_bytes=%d", len(parsed.Users), len(parsed.AccessRequests), len(raw))
	if exists {
		if err := deleteWorkspaceFile(ctx, source); err != nil {
			// The rows are authoritative from here; a leftover file is never read.
			log.Printf("[SERVER_STATE] event=source_retirement_failed table=users error=%v", err)
		} else {
			log.Printf("[SERVER_STATE] event=source_retired table=users")
		}
	}
	return nil
}

// writeUserDirectoryRows replaces the whole directory and bumps its version,
// inside the caller's transaction.
func writeUserDirectoryRows(ctx context.Context, tx *sql.Tx, dir *userDirectory) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM users`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_access_requests`); err != nil {
		return err
	}
	for i := range dir.Users {
		u := &dir.Users[i]
		if u.Products == nil {
			u.Products = []string{}
		}
		record, err := json.Marshal(u)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO users(id,username,email,disabled,record) VALUES (?,?,?,?,?)`, u.ID, u.Username, u.Email, u.Disabled, string(record)); err != nil {
			return fmt.Errorf("save account %q: %w", u.Username, err)
		}
	}
	for key, receipt := range dir.AccessRequests {
		raw, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_access_requests(key,receipt) VALUES (?,?)`, key, string(raw)); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO state_versions(name,version) VALUES ('users',1) ON CONFLICT(name) DO UPDATE SET version=version+1`)
	return err
}

// legacyUserDirectoryPath is the former document, used only until this
// server's one-off import has run. Remove with the import.
const legacyUserDirectoryPath = "config/users.json"

// sqliteUserDirectory is the production store.
type sqliteUserDirectory struct{}

func (sqliteUserDirectory) Version(ctx context.Context) (string, error) {
	db, imported, err := usersFromDatabase(ctx)
	if err != nil {
		return "", err
	}
	if !imported {
		raw, _, err := readFileFromWorkspace(ctx, legacyUserDirectoryPath)
		return "file:" + raw, err
	}
	var v int64
	err = db.QueryRowContext(ctx, `SELECT version FROM state_versions WHERE name='users'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "0", nil
	}
	return strconv.FormatInt(v, 10), err
}

func (sqliteUserDirectory) Load(ctx context.Context) (*userDirectory, string, error) {
	db, imported, err := usersFromDatabase(ctx)
	if err != nil {
		return &userDirectory{}, "", err
	}
	if !imported {
		raw, _, err := readFileFromWorkspace(ctx, legacyUserDirectoryPath)
		if err != nil {
			return &userDirectory{}, "", err
		}
		dir, err := decodeUserDirectory(raw)
		return dir, "file:" + raw, err
	}
	// One read transaction so the rows and the version agree.
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return &userDirectory{}, "", err
	}
	defer tx.Rollback()
	version := "0"
	var v int64
	if err := tx.QueryRowContext(ctx, `SELECT version FROM state_versions WHERE name='users'`).Scan(&v); err == nil {
		version = strconv.FormatInt(v, 10)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return &userDirectory{}, "", err
	}
	out := &userDirectory{}
	rows, err := tx.QueryContext(ctx, `SELECT record FROM users ORDER BY username`)
	if err != nil {
		return &userDirectory{}, "", err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return &userDirectory{}, "", err
		}
		var u UserRecord
		if err := json.Unmarshal([]byte(raw), &u); err != nil {
			return &userDirectory{}, "", fmt.Errorf("user directory row: %w", err)
		}
		out.Users = append(out.Users, u)
	}
	if err := rows.Err(); err != nil {
		return &userDirectory{}, "", err
	}
	reqs, err := tx.QueryContext(ctx, `SELECT key, receipt FROM user_access_requests`)
	if err != nil {
		return &userDirectory{}, "", err
	}
	defer reqs.Close()
	for reqs.Next() {
		var key, raw string
		if err := reqs.Scan(&key, &raw); err != nil {
			return &userDirectory{}, "", err
		}
		var receipt userAccessReceipt
		if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
			return &userDirectory{}, "", fmt.Errorf("user access request row: %w", err)
		}
		if out.AccessRequests == nil {
			out.AccessRequests = map[string]userAccessReceipt{}
		}
		out.AccessRequests[key] = receipt
	}
	return out, version, reqs.Err()
}

func (sqliteUserDirectory) Save(ctx context.Context, dir *userDirectory) error {
	db, imported, err := usersFromDatabase(ctx)
	if err != nil {
		return err
	}
	if !imported {
		raw, err := json.MarshalIndent(userDirectoryFile{Users: dir.Users, AccessRequests: dir.AccessRequests}, "", "  ")
		if err != nil {
			return err
		}
		return writeFileToWorkspace(ctx, legacyUserDirectoryPath, string(raw))
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeUserDirectoryRows(ctx, tx, dir); err != nil {
		return err
	}
	return tx.Commit()
}
