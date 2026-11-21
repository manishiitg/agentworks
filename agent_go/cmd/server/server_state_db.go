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
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
)

// The server state database (#3156): system state the server reads on many
// requests or for every Crew, kept as rows instead of whole JSON documents.
// It lives in the app's private state folder, outside workspace documents,
// so agents, file tools and Team sync never see it. One file per docs root:
// <state>/server-state/<hash of docs>/state.sqlite (directory 0700, file 0600).
//
// Tables: the user directory, imported once from config/users.json, and the
// automation records of every Crew and workflow (automation_records.go).

var serverState struct {
	sync.Mutex
	db   *sql.DB
	file string
	// usersImported is false while the one-off user directory import waits for
	// the host (see userDirectoryImportReady); checked again every half minute.
	usersImported  bool
	usersCheckedAt time.Time
	usersWaitLog   bool
	// usersVersion caches the directory's version row: every account lookup
	// asks for it, and the query plus path resolution was a fifth of the
	// server's CPU (#3156 profile, 19 Nov). This process is the only writer and
	// clears it on save; the short life covers anything else touching the file.
	usersVersion   string
	usersVersionAt time.Time
}

// statePath caches the database location per docs and state root: resolving
// it walks every path component through symlinks, on each lookup.
var statePath struct {
	sync.Mutex
	key, file string
}

const usersVersionLife = 2 * time.Second

// userDirectoryImportReady gates the import: on a host with slots, the root
// slot-repair timer must already read accounts from this database, or new
// people would silently get no slot once users.json is gone. Until then the
// directory stays in config/users.json exactly as before.
var userDirectoryImportReady = slots.RepairReadsServerState

func serverStateDatabasePath() (string, error) {
	docs := fsutil.WorkspaceDocsRoot()
	state, err := crewroot.StateRoot()
	if err != nil {
		return "", err
	}
	key := docs + "\x00" + state
	statePath.Lock()
	defer statePath.Unlock()
	if statePath.key == key {
		return statePath.file, nil
	}
	dir, err := sqliteopen.StateDirectory(docs, "server-state")
	if err != nil {
		return "", err
	}
	statePath.key, statePath.file = key, filepath.Join(dir, "state.sqlite")
	return statePath.file, nil
}

// openServerState returns the shared handle and runs the user directory's
// one-off import (see importUsersWhenReady). A failed import (the workspace
// API not up yet) returns an error and is retried by the next caller.
func openServerState(ctx context.Context) (*sql.DB, error) {
	serverState.Lock()
	defer serverState.Unlock()
	db, fresh, err := serverStateHandleLocked(ctx)
	if err != nil {
		return nil, err
	}
	if !fresh && (serverState.usersImported || time.Since(serverState.usersCheckedAt) <= 30*time.Second) {
		return db, nil
	}
	if err := importUsersWhenReady(ctx, db, filepath.Dir(serverState.file)); err != nil {
		if fresh {
			serverState.db, serverState.file = nil, ""
			db.Close()
		}
		return nil, err
	}
	return db, nil
}

// serverStateHandle returns the shared handle without touching any import.
func serverStateHandle(ctx context.Context) (*sql.DB, error) {
	serverState.Lock()
	defer serverState.Unlock()
	db, _, err := serverStateHandleLocked(ctx)
	return db, err
}

// serverStateHandleLocked opens the database for the current docs root and
// creates its schema; fresh reports a newly opened handle.
func serverStateHandleLocked(ctx context.Context) (*sql.DB, bool, error) {
	file, err := serverStateDatabasePath()
	if err != nil {
		return nil, false, err
	}
	if serverState.db != nil && serverState.file == file {
		return serverState.db, false, nil
	}
	if serverState.db != nil {
		serverState.db.Close()
		serverState.db, serverState.file = nil, ""
	}
	dir := filepath.Dir(file)
	if err := agentworksclient.EnsurePrivateDirectory(dir); err != nil {
		return nil, false, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, false, err
	}
	if info, e := os.Lstat(file); e == nil && !info.Mode().IsRegular() {
		return nil, false, errors.New("server state must be an ordinary private database")
	} else if e != nil && !os.IsNotExist(e) {
		return nil, false, e
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	err = f.Chmod(0o600)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, false, err
	}
	db, err := sql.Open("sqlite", sqliteopen.DSN(file)+"&_txlock=immediate")
	if err != nil {
		return nil, false, err
	}
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS state_imports (name TEXT PRIMARY KEY, source_bytes INTEGER NOT NULL, rows INTEGER NOT NULL, imported_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS state_versions (name TEXT PRIMARY KEY, version INTEGER NOT NULL)`,
		// record is the account's JSON; id/username/disabled are columns for
		// single-row lookups and for the host's slot provisioning script.
		`CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY, username TEXT NOT NULL, email TEXT NOT NULL, disabled INTEGER NOT NULL, record TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS user_access_requests (key TEXT PRIMARY KEY, receipt TEXT NOT NULL)`,
		// One row per automation record document (schedules, functions or
		// webhooks of a Crew, workflow or Goal), keyed by the physical file it
		// replaced. folder_dev/folder_ino identify the folder that held it, so
		// a deleted and recreated Crew or workflow never inherits old rows.
		// document NULL: the folder has none of that kind.
		`CREATE TABLE IF NOT EXISTS automation_records (file TEXT PRIMARY KEY, folder_dev INTEGER NOT NULL, folder_ino INTEGER NOT NULL, document TEXT, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	} {
		if _, err = db.ExecContext(ctx, ddl); err != nil {
			db.Close()
			return nil, false, err
		}
	}
	serverState.db, serverState.file, serverState.usersImported, serverState.usersWaitLog = db, file, false, false
	serverState.usersVersion, serverState.usersVersionAt = "", time.Now()
	return db, true, nil
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
	serverState.Lock()
	cached, at := serverState.usersVersion, serverState.usersVersionAt
	serverState.Unlock()
	if cached != "" && time.Since(at) < usersVersionLife {
		return cached, nil
	}
	start := time.Now()
	var v int64
	err = db.QueryRowContext(ctx, `SELECT version FROM state_versions WHERE name='users'`).Scan(&v)
	version := strconv.FormatInt(v, 10)
	if errors.Is(err, sql.ErrNoRows) {
		version, err = "0", nil
	}
	if err == nil {
		serverState.Lock()
		if serverState.usersVersionAt.Before(start) {
			serverState.usersVersion, serverState.usersVersionAt = version, start
		}
		serverState.Unlock()
	}
	return version, err
}

// forgetUsersVersion makes the next lookup read the version row again.
func forgetUsersVersion() {
	serverState.Lock()
	serverState.usersVersion, serverState.usersVersionAt = "", time.Now()
	serverState.Unlock()
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
	err = tx.Commit()
	forgetUsersVersion()
	return err
}
