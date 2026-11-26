package sessiontransfer

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// Antigravity ("agy-cli") keeps one self-contained SQLite file per
// conversation, <store>/<id>.db (the store is ~/.gemini/antigravity-cli/
// conversations). The id is the one `agy --conversation <id>` resumes. Steps
// only ever append; the account login, MCP catalogue and settings live outside
// this file, so the bundle carries nothing but the conversation.
//
// Resume needs no rewriting for a new computer or folder: the working
// directory appears in the file only as descriptive file:// URIs, and
// `--conversation` found the copy in a fresh home with a different cwd
// (proved with a real agy 1.5.2 conversation). The summary index agy keeps next
// to the store is rebuilt by agy itself and is not carried.
//
// Not carried: child conversations started with agy's native subagents (their
// own .db files) and the per-conversation brain/ artifact folder.
const (
	agyFormat = "agy-conversation"
	agyRoot   = "conversation.db"
	// agy marks a finished step with status 3.
	agyStepDone = 3
)

// Every table agy 1.5.2 creates. Anything else is a layout this code was not
// written for, so the export refuses rather than carry unknown state.
var agyTables = map[string]bool{
	"trajectory_meta": true, "steps": true, "gen_metadata": true, "executor_metadata": true,
	"parent_references": true, "trajectory_metadata_blob": true, "battle_mode_infos": true,
}

// Blob columns per table, keyed by their primary key column. A fork rewrites the
// conversation's ids in all of them; the config tables are also checked for
// credentials.
var agyBlobColumns = []struct {
	table, key string
	columns    []string
	config     bool
}{
	{"steps", "idx", []string{"metadata", "error_details", "permissions", "task_details", "render_info", "step_payload"}, false},
	{"gen_metadata", "idx", []string{"data"}, true},
	{"executor_metadata", "idx", []string{"data"}, true},
	{"parent_references", "idx", []string{"data"}, true},
	{"trajectory_metadata_blob", "id", []string{"data"}, true},
	{"battle_mode_infos", "idx", []string{"data"}, true},
}

// Markers of a login or API key. The config tables hold none today; if a future
// agy puts one there the export stops instead of sending it to another computer.
var agyCredentialMarkers = [][]byte{[]byte("ya29."), []byte("AIza"), []byte("refresh_token"), []byte("access_token"), []byte("GEMINI_API_KEY"), []byte("GOOGLE_API_KEY"), []byte("-----BEGIN")}

// ExportAgy copies one conversation into private staging. The snapshot is taken
// through SQLite so a committed WAL is never lost. The caller closes agy first.
func ExportAgy(transport, store, workspace, id, out string) (*Bundle, error) {
	return ExportAgyWithBudget(transport, store, workspace, id, out, MaxBytes())
}

func ExportAgyWithBudget(transport, store, workspace, id, out string, maxBytes int64) (*Bundle, error) {
	if !UUID.MatchString(id) || !filepath.IsAbs(store) || !filepath.IsAbs(out) || !filepath.IsAbs(workspace) {
		return nil, fmt.Errorf("invalid native export identity")
	}
	source, e := Path(store, id+".db", false)
	if e != nil {
		if os.IsNotExist(e) {
			return nil, fmt.Errorf("the native agy conversation is missing")
		}
		return nil, e
	}
	sources := map[string]string{"database": source}
	if _, statErr := os.Stat(source + "-wal"); statErr == nil {
		sources["wal"] = source + "-wal"
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}
	if e = checkExportBudget(sources, maxBytes); e != nil {
		return nil, e
	}
	if e = os.MkdirAll(out, 0700); e != nil {
		return nil, e
	}
	dest, e := Path(out, agyRoot, true)
	if e != nil {
		return nil, e
	}
	if e = SnapshotDB(source, dest); e != nil {
		return nil, e
	}
	f, e := Inspect(dest)
	if e != nil {
		return nil, e
	}
	b := &Bundle{Provider: "agy-cli", Format: agyFormat, Transport: transport, SessionID: id, Workspace: workspace, Root: out, Files: map[string]File{agyRoot: f}}
	if e = b.ValidateFiles(); e != nil {
		return nil, e
	}
	if e = b.ValidateJournals(); e != nil {
		return nil, e
	}
	return b, nil
}

// validateAgyDB checks one conversation file: known layout only, no triggers or
// views, belongs to id, finished (no step still running), no credentials.
func validateAgyDB(path, id string) error {
	db, e := openDB(path, true)
	if e != nil {
		return e
	}
	defer db.Close()
	var check string
	if e = db.QueryRow("PRAGMA quick_check").Scan(&check); e != nil || check != "ok" {
		return fmt.Errorf("agy conversation database is incomplete")
	}
	rows, e := db.Query("SELECT name FROM sqlite_master WHERE type='table'")
	if e != nil {
		return e
	}
	have := map[string]bool{}
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			rows.Close()
			return e
		}
		if !agyTables[name] {
			rows.Close()
			return fmt.Errorf("unsupported agy conversation layout")
		}
		have[name] = true
	}
	rows.Close()
	if !have["trajectory_meta"] || !have["steps"] {
		return fmt.Errorf("unsupported agy conversation layout")
	}
	var cascade, trajectory string
	var n int
	if e = db.QueryRow("SELECT COUNT(*) FROM trajectory_meta").Scan(&n); e != nil || n != 1 {
		return fmt.Errorf("agy conversation database holds more than one conversation")
	}
	if e = db.QueryRow("SELECT cascade_id, trajectory_id FROM trajectory_meta").Scan(&cascade, &trajectory); e != nil {
		return e
	}
	if cascade != id || !UUID.MatchString(trajectory) {
		return fmt.Errorf("agy conversation database belongs to another conversation")
	}
	var last int
	if e = db.QueryRow("SELECT status FROM steps ORDER BY idx DESC LIMIT 1").Scan(&last); e != nil {
		return fmt.Errorf("agy conversation has no steps")
	}
	if last != agyStepDone {
		return fmt.Errorf("finish the current agy reply before moving it")
	}
	for _, t := range agyBlobColumns {
		if !t.config || !have[t.table] {
			continue
		}
		for _, col := range t.columns {
			r, e := db.Query(fmt.Sprintf("SELECT %s FROM %s", col, t.table))
			if e != nil {
				return e
			}
			for r.Next() {
				var raw []byte
				if e = r.Scan(&raw); e != nil {
					r.Close()
					return e
				}
				for _, marker := range agyCredentialMarkers {
					if bytes.Contains(raw, marker) {
						r.Close()
						return fmt.Errorf("agy conversation state contains account configuration")
					}
				}
			}
			r.Close()
		}
	}
	return nil
}

type agyStepKey struct {
	kind int
	sum  [32]byte
}

// agySteps returns each step's type and payload digest by position, plus the
// trajectory id, from a bundle's conversation file.
func agySteps(b *Bundle) (map[int]agyStepKey, string, error) {
	p, e := Path(b.Root, agyRoot, false)
	if e != nil {
		return nil, "", e
	}
	db, e := openDB(p, true)
	if e != nil {
		return nil, "", e
	}
	defer db.Close()
	var trajectory string
	if e = db.QueryRow("SELECT trajectory_id FROM trajectory_meta").Scan(&trajectory); e != nil {
		return nil, "", e
	}
	rows, e := db.Query("SELECT idx, step_type, step_payload FROM steps")
	if e != nil {
		return nil, "", e
	}
	defer rows.Close()
	steps := map[int]agyStepKey{}
	for rows.Next() {
		var idx, kind int
		var payload []byte
		if e = rows.Scan(&idx, &kind, &payload); e != nil {
			return nil, "", e
		}
		steps[idx] = agyStepKey{kind, sha256.Sum256(payload)}
	}
	return steps, trajectory, rows.Err()
}

// agyAncestor: old is an ancestor of next when next still holds every step of
// old unchanged. Steps are appended by position, so this is the SQLite
// counterpart of the journals' byte-prefix check.
func agyAncestor(old, next *Bundle) error {
	oldSteps, oldTrajectory, e := agySteps(old)
	if e != nil {
		return e
	}
	nextSteps, nextTrajectory, e := agySteps(next)
	if e != nil {
		return e
	}
	if oldTrajectory != nextTrajectory {
		return ErrDiverged
	}
	for idx, want := range oldSteps {
		if got, ok := nextSteps[idx]; !ok || got != want {
			return ErrDiverged
		}
	}
	return nil
}

// ImportAgy writes the bundle's conversation into the store under its own id.
// An existing copy must be an ancestor of the bundle, otherwise both histories
// are kept and ErrDiverged is returned.
func ImportAgy(store, workspace string, b *Bundle) error {
	if b.Provider != "agy-cli" || b.Format != agyFormat {
		return fmt.Errorf("invalid agy transfer")
	}
	if !filepath.IsAbs(workspace) {
		return fmt.Errorf("agy import needs an absolute workspace")
	}
	if e := b.ValidateFiles(); e != nil {
		return e
	}
	if e := b.ValidateJournals(); e != nil {
		return e
	}
	if e := os.MkdirAll(store, 0700); e != nil {
		return e
	}
	existing := false
	if _, e := Path(store, b.SessionID+".db", false); e == nil {
		existing = true
	} else if !os.IsNotExist(e) {
		return e
	}
	if existing {
		tmp, e := os.MkdirTemp("", "agy-previous-")
		if e != nil {
			return e
		}
		defer os.RemoveAll(tmp)
		previous, e := ExportAgy(b.Transport, store, workspace, b.SessionID, tmp)
		if e != nil {
			return e
		}
		if e = Ancestor(previous, b); e != nil {
			return e
		}
	}
	dest, e := Path(store, b.SessionID+".db", true)
	if e != nil {
		return e
	}
	source, e := Path(b.Root, agyRoot, false)
	if e != nil {
		return e
	}
	// A stale WAL left beside an old copy must not replay over the new file.
	if existing {
		if e = quiesceDB(dest); e != nil {
			return e
		}
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if e = os.Remove(dest + suffix); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	return Copy(store, dest, source)
}

// forkAgy gives the copy its own conversation and trajectory ids. Both are the
// same length (36 bytes) wherever they are embedded in the protobuf blobs, so
// they are replaced in place without re-encoding anything.
func forkAgy(source *Bundle, out, workspace string) (*Bundle, error) {
	from, e := Path(source.Root, agyRoot, false)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(out, 0700); e != nil {
		return nil, e
	}
	dest, e := Path(out, agyRoot, true)
	if e != nil {
		return nil, e
	}
	if e = Copy(out, dest, from); e != nil {
		return nil, e
	}
	newID, newTrajectory := uuid.NewString(), uuid.NewString()
	db, e := sql.Open("sqlite", "file:"+dest)
	if e != nil {
		return nil, e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var oldTrajectory string
	if e = db.QueryRow("SELECT trajectory_id FROM trajectory_meta").Scan(&oldTrajectory); e != nil {
		return nil, e
	}
	tx, e := db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	replacer := [][2][]byte{{[]byte(source.SessionID), []byte(newID)}, {[]byte(oldTrajectory), []byte(newTrajectory)}}
	for _, t := range agyBlobColumns {
		for _, col := range t.columns {
			rows, e := tx.Query(fmt.Sprintf("SELECT %s, %s FROM %s WHERE %s IS NOT NULL", t.key, col, t.table, col))
			if e != nil {
				if strings.Contains(e.Error(), "no such table") {
					continue
				}
				return nil, e
			}
			type edit struct {
				key any
				raw []byte
			}
			var edits []edit
			for rows.Next() {
				var key any
				var raw []byte
				if e = rows.Scan(&key, &raw); e != nil {
					rows.Close()
					return nil, e
				}
				changed := raw
				for _, pair := range replacer {
					changed = bytes.ReplaceAll(changed, pair[0], pair[1])
				}
				if !bytes.Equal(changed, raw) {
					edits = append(edits, edit{key, changed})
				}
			}
			rows.Close()
			for _, ed := range edits {
				if _, e = tx.Exec(fmt.Sprintf("UPDATE %s SET %s=? WHERE %s=?", t.table, col, t.key), ed.raw, ed.key); e != nil {
					return nil, e
				}
			}
		}
	}
	if _, e = tx.Exec("UPDATE trajectory_meta SET cascade_id=?, trajectory_id=?", newID, newTrajectory); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	db.Close()
	f, e := Inspect(dest)
	if e != nil {
		return nil, e
	}
	fork := *source
	fork.Root, fork.SessionID, fork.Workspace, fork.Files = out, newID, workspace, map[string]File{agyRoot: f}
	if e = fork.ValidateFiles(); e != nil {
		return nil, e
	}
	if e = fork.ValidateJournals(); e != nil {
		return nil, e
	}
	return &fork, nil
}

func (b *Bundle) validateAgy() error {
	p, e := Path(b.Root, agyRoot, false)
	if e != nil {
		return e
	}
	return validateAgyDB(p, b.SessionID)
}
