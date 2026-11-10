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
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Service-owned Team history, never the agent-writable Crew database. Folder
// keys are the existing hashed content directory identities, not browser input.
var crewTeamLedgerSchema = []string{
	`CREATE TABLE IF NOT EXISTS folders (folder TEXT PRIMARY KEY, revision INTEGER NOT NULL, history_floor INTEGER NOT NULL DEFAULT 0)`,
	`CREATE TABLE IF NOT EXISTS imports (folder TEXT PRIMARY KEY, source_bytes INTEGER NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS versions (folder TEXT NOT NULL, path TEXT NOT NULL, hash TEXT, size INTEGER NOT NULL, executable INTEGER NOT NULL, chunks TEXT NOT NULL, revision INTEGER NOT NULL, author TEXT NOT NULL, modified_at TEXT NOT NULL, PRIMARY KEY(folder,path))`,
	`CREATE TABLE IF NOT EXISTS events (folder TEXT NOT NULL, ordinal INTEGER NOT NULL, path TEXT NOT NULL, source TEXT NOT NULL, kind TEXT NOT NULL, revision INTEGER NOT NULL, actor TEXT NOT NULL, author TEXT NOT NULL, at TEXT NOT NULL, hash TEXT, size INTEGER NOT NULL, executable INTEGER NOT NULL, chunks TEXT NOT NULL, PRIMARY KEY(folder,ordinal))`,
	`CREATE INDEX IF NOT EXISTS team_events_revision ON events(folder,revision,ordinal)`,
	`CREATE INDEX IF NOT EXISTS team_events_path_revision ON events(folder,path,revision DESC,ordinal DESC)`,
	`CREATE INDEX IF NOT EXISTS team_events_base ON events(folder,path,hash)`,
	`CREATE TABLE IF NOT EXISTS clashes (folder TEXT NOT NULL, ordinal INTEGER NOT NULL, source TEXT NOT NULL, path TEXT NOT NULL, winning_revision INTEGER NOT NULL, losing_revision INTEGER NOT NULL, winning_author TEXT NOT NULL, losing_author TEXT NOT NULL, resolved INTEGER NOT NULL, merged INTEGER NOT NULL, hunks INTEGER NOT NULL, PRIMARY KEY(folder,ordinal))`,
	`CREATE TABLE IF NOT EXISTS receipts (folder TEXT NOT NULL, receipt_key TEXT NOT NULL, transfer_id TEXT NOT NULL, digest TEXT NOT NULL, revision INTEGER NOT NULL, PRIMARY KEY(folder,receipt_key))`,
	`CREATE TABLE IF NOT EXISTS receipt_conflicts (folder TEXT NOT NULL, receipt_key TEXT NOT NULL, ordinal INTEGER NOT NULL, path TEXT NOT NULL, PRIMARY KEY(folder,receipt_key,ordinal))`,
	`CREATE TABLE IF NOT EXISTS on_purpose (folder TEXT NOT NULL, path TEXT NOT NULL, actor TEXT NOT NULL, PRIMARY KEY(folder,path))`,
}

type crewTeamLedger struct {
	db     *sql.DB
	folder string
}

func openCrewTeamLedger(project string, writerLocked bool) (*crewTeamLedger, error) {
	content, err := crewTeamContentDir(project) // live folder selection, never cached
	if err != nil {
		return nil, err
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		return nil, err
	}
	if err = agentworksclient.EnsurePrivateDirectory(dir); err != nil {
		return nil, err
	}
	// Unlike a Crew workspace, this service directory must never accept slot
	// group access, even if it was accidentally given a shared-folder mode.
	if err = os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	name := filepath.Join(dir, "team.sqlite")
	if info, e := os.Lstat(name); e == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return nil, errors.New("Team ledger must be an ordinary private file")
	} else if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	f, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	err = f.Chmod(0600)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteopen.DSN(name))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	ledger := &crewTeamLedger{db: db, folder: filepath.Base(content)}
	if err = ledger.initialize(name); err == nil {
		err = ledger.importJSON(project, content, writerLocked)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return ledger, nil
}

// Import is the only JSON reader. No normal fallback or dual writing remains.
// A writer passes its existing lock; a first reader takes that same lock itself.
func (l *crewTeamLedger) importJSON(project, content string, writerLocked bool) error {
	name := filepath.Join(content, "files.json")
	if _, err := os.Lstat(name); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if !writerLocked {
		dir, err := crewTeamDir(project)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		unlock, err := wf.LockWorkspace(ctx, dir)
		if err != nil {
			return err
		}
		defer unlock()
		selected, err := crewTeamContentDir(project)
		if err != nil {
			return err
		}
		if selected != content {
			return errors.New("the shared folder changed; reopen it before continuing")
		}
	}
	// Recheck after waiting: another request may have finished the import/rename.
	info, err := os.Lstat(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("Team history import must be an ordinary file")
	}
	if _, err = os.Lstat(name + ".imported"); err == nil {
		return errors.New("Team history has both an original and an imported backup. Ask the server administrator to compare them and remove the extra files.json copy before reopening the Team. Keep files.json.imported.")
	} else if !os.IsNotExist(err) {
		return err
	}
	var marker int64
	err = l.db.QueryRow(`SELECT source_bytes FROM imports WHERE folder=?`, l.folder).Scan(&marker)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		raw, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		var state crewTeamFileState
		if err = json.Unmarshal(raw, &state); err != nil {
			return fmt.Errorf("Team history import: %w", err)
		}
		tx, err := l.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err = l.saveTx(tx, state); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO imports(folder,source_bytes) VALUES (?,?)`, l.folder, len(raw)); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		log.Printf("[CREW_TEAM_LEDGER] project=%s folder=%s event=import versions=%d events=%d clashes=%d receipts=%d on_purpose=%d source_bytes=%d", project, l.folder, len(state.Versions), len(state.Events), len(state.Clashes), len(state.Receipts), len(state.OnPurpose), len(raw))
	}
	// A committed marker permits just this rename after a crash, never replay.
	return os.Rename(name, name+".imported")
}

func readCrewTeamFileState(project string) (crewTeamFileState, error) {
	return loadCrewTeamFileState(project, false)
}
func readCrewTeamFileStateLocked(project string) (crewTeamFileState, error) {
	return loadCrewTeamFileState(project, true)
}
func loadCrewTeamFileState(project string, writerLocked bool) (crewTeamFileState, error) {
	l, err := openCrewTeamLedger(project, writerLocked)
	if err != nil {
		return crewTeamFileState{}, err
	}
	defer l.db.Close()
	return l.load()
}
func saveCrewTeamFileState(project string, state crewTeamFileState) error {
	l, err := openCrewTeamLedger(project, true)
	if err != nil {
		return err
	}
	defer l.db.Close()
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	before := len(state.Events)
	removed, removedBytes := state.retainHistory(time.Now().UTC())
	if err = l.saveTx(tx, state); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if removed > 0 {
		log.Printf("[CREW_TEAM_LEDGER] project=%s event=prune events_before=%d events_after=%d events_removed=%d payload_bytes_removed=%d history_floor=%d", project, before, len(state.Events), removed, removedBytes, state.HistoryFloor)
	}
	return nil
}

func teamLedgerFile(hash sql.NullString, size int64, executable bool) *crewsync.File {
	if !hash.Valid {
		return nil
	}
	return &crewsync.File{Hash: hash.String, Size: size, Executable: executable}
}
func teamLedgerFileColumns(file *crewsync.File) (any, int64, bool) {
	if file == nil {
		return nil, 0, false
	}
	return file.Hash, file.Size, file.Executable
}

func (l *crewTeamLedger) load() (crewTeamFileState, error) { return l.loadState(true) }

// Current file readers need neither history, clashes nor retry receipts.
func readCrewTeamCurrentState(project string) (crewTeamFileState, error) {
	l, err := openCrewTeamLedger(project, false)
	if err != nil {
		return crewTeamFileState{}, err
	}
	defer l.db.Close()
	return l.loadState(false)
}

func (l *crewTeamLedger) loadState(history bool) (crewTeamFileState, error) {
	state := crewTeamFileState{Versions: map[string]crewTeamVersion{}, Receipts: map[string]crewTeamReceipt{}, OnPurpose: map[string]string{}}
	tx, err := l.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return state, err
	}
	defer tx.Rollback()
	if err = tx.QueryRow(`SELECT revision,history_floor FROM folders WHERE folder=?`, l.folder).Scan(&state.Revision, &state.HistoryFloor); err != nil {
		return state, err
	}
	// Every query closes before the next one: one connection, one coherent snapshot.
	read := func(query string, scan func(*sql.Rows) error) error {
		rows, e := tx.Query(query, l.folder)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			if e = scan(rows); e != nil {
				return e
			}
		}
		return rows.Err()
	}
	err = read(`SELECT path,hash,size,executable,chunks,revision,author,modified_at FROM versions WHERE folder=?`, func(rows *sql.Rows) error {
		var path, chunks string
		var v crewTeamVersion
		var hash sql.NullString
		var size int64
		var executable bool
		if e := rows.Scan(&path, &hash, &size, &executable, &chunks, &v.Revision, &v.Author, &v.ModifiedAt); e != nil {
			return e
		}
		v.File = teamLedgerFile(hash, size, executable)
		if e := json.Unmarshal([]byte(chunks), &v.Chunks); e != nil {
			return e
		}
		state.Versions[path] = v
		return nil
	})
	if err != nil {
		return state, err
	}
	if history {
		err = read(`SELECT source,path,kind,revision,actor,author,at,hash,size,executable,chunks FROM events WHERE folder=? ORDER BY ordinal`, func(rows *sql.Rows) error {
			var e crewTeamEvent
			var hash sql.NullString
			var size int64
			var executable bool
			var chunks string
			if err := rows.Scan(&e.Source, &e.Path, &e.Kind, &e.Revision, &e.Actor, &e.Author, &e.At, &hash, &size, &executable, &chunks); err != nil {
				return err
			}
			e.File = teamLedgerFile(hash, size, executable)
			if err := json.Unmarshal([]byte(chunks), &e.Chunks); err != nil {
				return err
			}
			state.Events = append(state.Events, e)
			return nil
		})
		if err != nil {
			return state, err
		}
		err = read(`SELECT source,path,winning_revision,losing_revision,winning_author,losing_author,resolved,merged,hunks FROM clashes WHERE folder=? ORDER BY ordinal`, func(rows *sql.Rows) error {
			var c crewTeamClash
			if err := rows.Scan(&c.Source, &c.Path, &c.WinningRevision, &c.LosingRevision, &c.WinningAuthor, &c.LosingAuthor, &c.Resolved, &c.Merged, &c.Hunks); err != nil {
				return err
			}
			state.Clashes = append(state.Clashes, c)
			return nil
		})
		if err != nil {
			return state, err
		}
		err = read(`SELECT receipt_key,transfer_id,digest,revision FROM receipts WHERE folder=?`, func(rows *sql.Rows) error {
			var key string
			var r crewTeamReceipt
			if err := rows.Scan(&key, &r.TransferID, &r.Digest, &r.Revision); err != nil {
				return err
			}
			r.Conflicts = []string{}
			state.Receipts[key] = r
			return nil
		})
		if err != nil {
			return state, err
		}
		err = read(`SELECT receipt_key,path FROM receipt_conflicts WHERE folder=? ORDER BY receipt_key,ordinal`, func(rows *sql.Rows) error {
			var key, path string
			if err := rows.Scan(&key, &path); err != nil {
				return err
			}
			r := state.Receipts[key]
			r.Conflicts = append(r.Conflicts, path)
			state.Receipts[key] = r
			return nil
		})
		if err != nil {
			return state, err
		}
	}
	err = read(`SELECT path,actor FROM on_purpose WHERE folder=?`, func(rows *sql.Rows) error {
		var path, actor string
		if err := rows.Scan(&path, &actor); err != nil {
			return err
		}
		state.OnPurpose[path] = actor
		return nil
	})
	if err != nil {
		return state, err
	}
	return state, tx.Commit()
}

// Existing writers hold the filesystem Team lock across canonical bytes and
// these rows. UPSERT filters avoid rewriting unchanged rows; receipts are only
// added after all files succeed, including the existing partial-write contract.
func (l *crewTeamLedger) saveTx(tx *sql.Tx, s crewTeamFileState) error {
	// Prepare each row statement once per transaction, not once per history row.
	statements := map[string]*sql.Stmt{}
	defer func() {
		for _, stmt := range statements {
			stmt.Close()
		}
	}()
	exec := func(query string, args ...any) (sql.Result, error) {
		stmt := statements[query]
		if stmt == nil {
			var err error
			stmt, err = tx.Prepare(query)
			if err != nil {
				return nil, err
			}
			statements[query] = stmt
		}
		return stmt.Exec(args...)
	}
	if _, err := exec(`UPDATE folders SET revision=?,history_floor=? WHERE folder=? AND (revision<>? OR history_floor<>?)`, s.Revision, s.HistoryFloor, l.folder, s.Revision, s.HistoryFloor); err != nil {
		return err
	}
	for path, v := range s.Versions {
		chunks, err := json.Marshal(v.Chunks)
		if err != nil {
			return err
		}
		hash, size, executable := teamLedgerFileColumns(v.File)
		_, err = exec(`INSERT INTO versions(folder,path,hash,size,executable,chunks,revision,author,modified_at) VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(folder,path) DO UPDATE SET hash=excluded.hash,size=excluded.size,executable=excluded.executable,chunks=excluded.chunks,revision=excluded.revision,author=excluded.author,modified_at=excluded.modified_at WHERE (hash,size,executable,chunks,revision,author,modified_at) IS NOT (excluded.hash,excluded.size,excluded.executable,excluded.chunks,excluded.revision,excluded.author,excluded.modified_at)`, l.folder, path, hash, size, executable, string(chunks), v.Revision, v.Author, v.ModifiedAt)
		if err != nil {
			return err
		}
	}
	for i, e := range s.Events {
		chunks, err := json.Marshal(e.Chunks)
		if err != nil {
			return err
		}
		hash, size, executable := teamLedgerFileColumns(e.File)
		_, err = exec(`INSERT INTO events(folder,ordinal,path,source,kind,revision,actor,author,at,hash,size,executable,chunks) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(folder,ordinal) DO UPDATE SET path=excluded.path,source=excluded.source,kind=excluded.kind,revision=excluded.revision,actor=excluded.actor,author=excluded.author,at=excluded.at,hash=excluded.hash,size=excluded.size,executable=excluded.executable,chunks=excluded.chunks WHERE (path,source,kind,revision,actor,author,at,hash,size,executable,chunks) IS NOT (excluded.path,excluded.source,excluded.kind,excluded.revision,excluded.actor,excluded.author,excluded.at,excluded.hash,excluded.size,excluded.executable,excluded.chunks)`, l.folder, i, e.Path, e.Source, e.Kind, e.Revision, e.Actor, e.Author, e.At, hash, size, executable, string(chunks))
		if err != nil {
			return err
		}
	}
	// Retention compacts ordinal order but never changes public revision identities.
	if _, err := exec(`DELETE FROM events WHERE folder=? AND ordinal>=?`, l.folder, len(s.Events)); err != nil {
		return err
	}
	for i, c := range s.Clashes {
		_, err := exec(`INSERT INTO clashes(folder,ordinal,source,path,winning_revision,losing_revision,winning_author,losing_author,resolved,merged,hunks) VALUES (?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(folder,ordinal) DO UPDATE SET source=excluded.source,path=excluded.path,winning_revision=excluded.winning_revision,losing_revision=excluded.losing_revision,winning_author=excluded.winning_author,losing_author=excluded.losing_author,resolved=excluded.resolved,merged=excluded.merged,hunks=excluded.hunks WHERE (source,path,winning_revision,losing_revision,winning_author,losing_author,resolved,merged,hunks) IS NOT (excluded.source,excluded.path,excluded.winning_revision,excluded.losing_revision,excluded.winning_author,excluded.losing_author,excluded.resolved,excluded.merged,excluded.hunks)`, l.folder, i, c.Source, c.Path, c.WinningRevision, c.LosingRevision, c.WinningAuthor, c.LosingAuthor, c.Resolved, c.Merged, c.Hunks)
		if err != nil {
			return err
		}
	}
	for key, r := range s.Receipts {
		_, err := exec(`INSERT INTO receipts(folder,receipt_key,transfer_id,digest,revision) VALUES (?,?,?,?,?) ON CONFLICT(folder,receipt_key) DO UPDATE SET transfer_id=excluded.transfer_id,digest=excluded.digest,revision=excluded.revision WHERE (transfer_id,digest,revision) IS NOT (excluded.transfer_id,excluded.digest,excluded.revision)`, l.folder, key, r.TransferID, r.Digest, r.Revision)
		if err != nil {
			return err
		}
		for i, path := range r.Conflicts {
			if _, err = exec(`INSERT INTO receipt_conflicts(folder,receipt_key,ordinal,path) VALUES (?,?,?,?) ON CONFLICT(folder,receipt_key,ordinal) DO UPDATE SET path=excluded.path WHERE path<>excluded.path`, l.folder, key, i, path); err != nil {
				return err
			}
		}
		if _, err = exec(`DELETE FROM receipt_conflicts WHERE folder=? AND receipt_key=? AND ordinal>=?`, l.folder, key, len(r.Conflicts)); err != nil {
			return err
		}
	}
	// On-purpose grants can be removed; do not leave a deleted grant in the ledger.
	rows, err := tx.Query(`SELECT path FROM on_purpose WHERE folder=?`, l.folder)
	if err != nil {
		return err
	}
	var removed []string
	for rows.Next() {
		var path string
		if err = rows.Scan(&path); err != nil {
			rows.Close()
			return err
		}
		if _, ok := s.OnPurpose[path]; !ok {
			removed = append(removed, path)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, path := range removed {
		if _, err = exec(`DELETE FROM on_purpose WHERE folder=? AND path=?`, l.folder, path); err != nil {
			return err
		}
	}
	for path, actor := range s.OnPurpose {
		if _, err = exec(`INSERT INTO on_purpose(folder,path,actor) VALUES (?,?,?) ON CONFLICT(folder,path) DO UPDATE SET actor=excluded.actor WHERE actor<>excluded.actor`, l.folder, path, actor); err != nil {
			return err
		}
	}
	return nil
}
