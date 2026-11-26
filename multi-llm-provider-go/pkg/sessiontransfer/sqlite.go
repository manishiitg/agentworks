package sessiontransfer

import (
	"context"
	"crypto/md5" // Mirrors Cursor's workspace directory naming, not cryptography.
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func CursorCLIPath(config, workspace, id string) (string, error) {
	if !UUID.MatchString(id) {
		return "", fmt.Errorf("invalid Cursor CLI identity")
	}
	rel := fmt.Sprintf("chats/%x/%s", md5.Sum([]byte(filepath.Clean(workspace))), id)
	return filepath.Join(config, filepath.FromSlash(rel)), nil
}

func validateCursorCLIMetadata(root, workspace string) error {
	p, e := Path(root, "meta.json", false)
	if e != nil {
		return e
	}
	raw, e := os.ReadFile(p)
	if e != nil {
		return e
	}
	if len(raw) > 1<<20 {
		return fmt.Errorf("Cursor native metadata is too large")
	}
	var meta struct {
		Cwd           string `json:"cwd"`
		SchemaVersion int    `json:"schemaVersion"`
	}
	if json.Unmarshal(raw, &meta) != nil {
		return fmt.Errorf("Cursor CLI native metadata is unreadable")
	}
	// schemaVersion is Cursor's session store layout, not its app version: every Cursor CLI writes 1 today.
	if meta.SchemaVersion != 1 {
		return fmt.Errorf("Cursor CLI session store schema %d is unsupported", meta.SchemaVersion)
	}
	if filepath.Clean(meta.Cwd) != filepath.Clean(workspace) {
		return fmt.Errorf("Cursor CLI native workspace does not match")
	}
	p, e = Path(root, "store.db", false)
	if e != nil {
		return e
	}
	db, e := openDB(p, true)
	if e != nil {
		return e
	}
	defer db.Close()
	var check string
	if e = db.QueryRow("PRAGMA quick_check").Scan(&check); e != nil {
		return e
	}
	if check != "ok" {
		return fmt.Errorf("Cursor native database is incomplete")
	}
	return nil
}

func ExportCursorCLI(config, workspace, id, out string, remember bool) (*Bundle, error) {
	root, e := CursorCLIPath(config, workspace, id)
	if e != nil {
		return nil, e
	}
	// Reject links in the account-selected configuration path's descendants.
	rel, e := filepath.Rel(config, root)
	if e != nil {
		return nil, e
	}
	root, e = PathDirectory(config, filepath.ToSlash(rel), false)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(out, 0700); e != nil {
		return nil, e
	}
	b := &Bundle{Provider: "cursor-cli", Format: "cursor-cli-sqlite", Transport: "tmux", SessionID: id, Workspace: workspace, Root: out, Files: map[string]File{}}
	for _, name := range []string{"store.db", "meta.json", "prompt_history.json"} {
		p, e := Path(root, name, false)
		if os.IsNotExist(e) && name == "prompt_history.json" {
			continue
		}
		if e != nil {
			return nil, e
		}
		dest, e := Path(out, name, true)
		if e != nil {
			return nil, e
		}
		if name == "store.db" {
			e = SnapshotDB(p, dest)
		} else {
			e = Copy(out, dest, p)
		}
		if e != nil {
			return nil, e
		}
		f, e := Inspect(dest)
		if e != nil {
			return nil, e
		}
		b.Files[name] = f
	}
	if e = setSnapshotLineage(root, b); e != nil {
		return nil, e
	}
	if e = b.ValidateFiles(); e != nil {
		return nil, e
	}
	if e = validateCursorCLIMetadata(out, workspace); e != nil {
		return nil, e
	}
	if remember {
		if e = RememberSnapshotExport(root, b); e != nil {
			return nil, e
		}
	}
	return b, nil
}

func ImportCursorCLI(config, workspace string, b *Bundle) error {
	if b.Provider != "cursor-cli" || b.Format != "cursor-cli-sqlite" {
		return fmt.Errorf("invalid Cursor CLI transfer")
	}
	if e := b.ValidateFiles(); e != nil {
		return e
	}
	if e := validateCursorCLIMetadata(b.Root, b.Workspace); e != nil {
		return e
	}
	root, e := CursorCLIPath(config, workspace, b.SessionID)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(config, 0700); e != nil {
		return e
	}
	rel, e := filepath.Rel(config, root)
	if e != nil {
		return e
	}
	root, e = PathDirectory(config, filepath.ToSlash(rel), true)
	if e != nil {
		return e
	}
	if _, e = Path(root, "store.db", false); e == nil {
		tmp, e := os.MkdirTemp("", "cursor-native-check-")
		if e != nil {
			return e
		}
		defer os.RemoveAll(tmp)
		current, e := ExportCursorCLI(config, workspace, b.SessionID, tmp, false)
		if e != nil {
			return e
		}
		if e = CheckSnapshotReplacement(root, current, b); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if e = quiesceNativeDatabases(root, b); e != nil {
		return e
	}
	for name := range b.Files {
		source, e := Path(b.Root, name, false)
		if e != nil {
			return e
		}
		dest, e := Path(root, name, true)
		if e != nil {
			return e
		}
		if e = Copy(root, dest, source); e != nil {
			return e
		}
	}
	// Only the selected chat's plain metadata changes. Blob bytes, checkpoint
	// encryption state and native IDs are preserved exactly.
	metaPath, e := Path(root, "meta.json", false)
	if e != nil {
		return e
	}
	raw, e := os.ReadFile(metaPath)
	if e != nil {
		return e
	}
	var meta map[string]json.RawMessage
	if e = json.Unmarshal(raw, &meta); e != nil {
		return e
	}
	meta["cwd"], _ = json.Marshal(workspace)
	raw, e = json.Marshal(meta)
	if e != nil {
		return e
	}
	if e = Write(root, metaPath, raw); e != nil {
		return e
	}
	if e = validateCursorCLIMetadata(root, workspace); e != nil {
		return e
	}
	tmp, e := os.MkdirTemp("", "cursor-import-baseline-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(tmp)
	current, e := ExportCursorCLI(config, workspace, b.SessionID, tmp, false)
	if e != nil {
		return e
	}
	return rememberSnapshotImport(root, current, b)
}

func ExportCursorSDK(store, workspace, id, out string, remember bool) (*Bundle, error) {
	if !sdkID.MatchString(id) {
		return nil, fmt.Errorf("invalid Cursor SDK agent")
	}
	b := &Bundle{Provider: "cursor-cli", Format: "cursor-sdk-sqlite", Transport: "sdk", SessionID: id, Workspace: workspace, Root: out, Files: map[string]File{}}
	if e := os.MkdirAll(out, 0700); e != nil {
		return nil, e
	}
	checkpoint := fmt.Sprintf("agents/agent-%x/store.db", sha256.Sum256([]byte(id)))
	for _, name := range []string{"index.db", checkpoint} {
		source, e := Path(store, name, false)
		if os.IsNotExist(e) && name != "index.db" {
			continue
		}
		if e != nil {
			return nil, e
		}
		dest, e := Path(out, name, true)
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
		b.Files[name] = f
	}
	if e := setSnapshotLineage(store, b); e != nil {
		return nil, e
	}
	if e := b.ValidateFiles(); e != nil {
		return nil, e
	}
	if e := validateSDKStore(out, id, workspace); e != nil {
		return nil, e
	}
	if remember {
		if e := RememberSnapshotExport(store, b); e != nil {
			return nil, e
		}
	}
	return b, nil
}

func ImportCursorSDK(store, workspace string, b *Bundle) error {
	if b.Provider != "cursor-cli" || b.Format != "cursor-sdk-sqlite" {
		return fmt.Errorf("invalid Cursor SDK transfer")
	}
	if e := b.ValidateFiles(); e != nil {
		return e
	}
	if e := validateSDKStore(b.Root, b.SessionID, b.Workspace); e != nil {
		return e
	}
	// A pre-existing private store may contain an offline branch. Snapshot it
	// consistently and compare with its last successful export before writing.
	if _, e := Path(store, "index.db", false); e == nil {
		tmp, e := os.MkdirTemp("", "cursor-native-check-")
		if e != nil {
			return e
		}
		defer os.RemoveAll(tmp)
		current, e := ExportCursorSDK(store, workspace, b.SessionID, tmp, false)
		if e != nil {
			return e
		}
		if e = CheckSnapshotReplacement(store, current, b); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if e := quiesceNativeDatabases(store, b); e != nil {
		return e
	}
	for name := range b.Files {
		source, e := Path(b.Root, name, false)
		if e != nil {
			return e
		}
		dest, e := Path(store, name, true)
		if e != nil {
			return e
		}
		if e = Copy(store, dest, source); e != nil {
			return e
		}
	}
	// Agent.resume scopes its lookup by workspaceRef. Only this one agent is
	// in the private index, and the original workspace was checked above.
	u := url.URL{Scheme: "file", Path: filepath.Join(store, "index.db")}
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return e
	}
	if e = secureNativeDB(db); e != nil {
		db.Close()
		return e
	}
	result, e := db.Exec("UPDATE agents SET workspace_ref=? WHERE agent_id=? AND workspace_ref=?", workspace, b.SessionID, b.Workspace)
	if e != nil {
		db.Close()
		return e
	}
	n, e := result.RowsAffected()
	db.Close()
	if e != nil {
		return e
	}
	if n != 1 {
		return fmt.Errorf("Cursor native workspace could not be rebound")
	}
	if e = validateSDKStore(store, b.SessionID, workspace); e != nil {
		return e
	}
	marker := filepath.Join(store, ".agentworks-native-handoff.json")
	raw, _ := json.Marshal(map[string]string{"session_id": b.SessionID, "workspace": workspace})
	if e = Write(store, marker, raw); e != nil {
		return e
	}
	tmp, e := os.MkdirTemp("", "cursor-import-baseline-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(tmp)
	current, e := ExportCursorSDK(store, workspace, b.SessionID, tmp, false)
	if e != nil {
		return e
	}
	return rememberSnapshotImport(store, current, b)
}

// The stopped SDK may leave a committed WAL behind. Checkpoint the old copy
// before replacing its DB, otherwise that old WAL can replay over the imported
// state on the return leg. A live writer must finish before replacement.
func quiesceNativeDatabases(root string, b *Bundle) error {
	for name := range b.Files {
		if !strings.HasSuffix(name, ".db") {
			continue
		}
		p, e := Path(root, name, false)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		if e = quiesceDB(p); e != nil {
			return e
		}
	}
	return nil
}

// quiesceDB folds one database's WAL into the file and removes its sidecars.
func quiesceDB(p string) error {
	u := url.URL{Scheme: "file", Path: p}
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return e
	}
	if e = secureNativeDB(db); e != nil {
		db.Close()
		return e
	}
	if _, e = db.Exec("PRAGMA busy_timeout=1000"); e != nil {
		db.Close()
		return e
	}
	var busy, log, checkpointed int
	e = db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpointed)
	if e != nil || busy != 0 {
		db.Close()
		return fmt.Errorf("finish the current native reply before replacing its state")
	}
	var mode string
	e = db.QueryRow("PRAGMA journal_mode=DELETE").Scan(&mode)
	db.Close()
	if e != nil || !strings.EqualFold(mode, "delete") {
		return fmt.Errorf("the native database is still in use")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if e = os.Remove(p + suffix); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	return nil
}

func openDB(path string, immutable bool) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{"mode": {"ro"}}
	if immutable {
		q.Set("immutable", "1")
	}
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return nil, e
	}
	if e = secureNativeDB(db); e != nil {
		db.Close()
		return nil, e
	}
	return db, nil
}

// SnapshotDB reads committed WAL state through SQLite. Copying the live DB or
// independently copying its WAL can silently lose a completed native turn.
func SnapshotDB(source, destination string) error {
	db, e := openDB(source, false)
	if e != nil {
		return e
	}
	defer db.Close()
	f, e := os.CreateTemp(filepath.Dir(destination), ".snapshot-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	f.Close()
	os.Remove(tmp)
	defer os.Remove(tmp)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, e = db.ExecContext(ctx, "VACUUM INTO ?", tmp); e != nil {
		return fmt.Errorf("snapshot native SQLite state: %w", e)
	}
	i, e := os.Stat(tmp)
	if e != nil {
		return e
	}
	if i.Size() > MaxBytes() {
		return fmt.Errorf("native database exceeds transfer capacity")
	}
	if e = os.Chmod(tmp, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, destination)
}

// SDK stores created by AgentWorks belong to exactly one chat. Never accept a
// workspace-wide SDK index containing an unrelated agent or another workspace.
func validateSDKStore(root, id, workspace string) error {
	p, e := Path(root, "index.db", false)
	if e != nil {
		return e
	}
	db, e := openDB(p, true)
	if e != nil {
		return e
	}
	defer db.Close()
	rows, e := db.Query("SELECT agent_id,workspace_ref,status,active_run_id,metadata_json FROM agents")
	if e != nil {
		return e
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var got, cwd, status, metadata string
		var active sql.NullString
		if e = rows.Scan(&got, &cwd, &status, &active, &metadata); e != nil {
			return e
		}
		if got != id || filepath.Clean(cwd) != filepath.Clean(workspace) {
			return fmt.Errorf("Cursor SDK store contains another conversation")
		}
		if strings.EqualFold(status, "running") || active.Valid && active.String != "" {
			return fmt.Errorf("finish the current Cursor reply before moving it")
		}
		// Encryption keys here decode this conversation's checkpoint blobs;
		// they are native state, not an account login. Do not log metadata.
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(metadata), &fields) != nil {
			return fmt.Errorf("invalid Cursor native metadata")
		}
		for key := range fields {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "apikey") || strings.Contains(lower, "access_token") || strings.Contains(lower, "refresh_token") {
				return fmt.Errorf("Cursor state contains account configuration")
			}
		}
		n++
	}
	if e = rows.Err(); e != nil {
		return e
	}
	if n != 1 {
		return fmt.Errorf("Cursor SDK native agent is missing or shared")
	}
	var foreign int
	if e = db.QueryRow("SELECT COUNT(*) FROM runs WHERE agent_id<>?", id).Scan(&foreign); e != nil {
		return e
	}
	if foreign != 0 {
		return fmt.Errorf("Cursor SDK store contains unrelated runs")
	}
	if e = db.QueryRow("SELECT COUNT(*) FROM run_events WHERE run_id NOT IN (SELECT run_id FROM runs WHERE agent_id=?)", id).Scan(&foreign); e != nil {
		return e
	}
	if foreign != 0 {
		return fmt.Errorf("Cursor SDK store contains unrelated run events")
	}
	return nil
}

// A local file baseline detects unexported edits; the parent snapshot detects
// branches even when both computers independently exported their local changes.
type snapshotState struct {
	Baseline Bundle `json:"baseline"`
	Parent   string `json:"parent_snapshot,omitempty"`
}

func loadSnapshotState(root string) (snapshotState, error) {
	var state snapshotState
	p, e := Path(root, ".agentworks-last-export.json", false)
	if e != nil {
		return state, e
	}
	raw, e := os.ReadFile(p)
	if e != nil {
		return state, e
	}
	if len(raw) > 2<<20 {
		return state, fmt.Errorf("invalid native baseline")
	}
	if json.Unmarshal(raw, &state) != nil || state.Baseline.Validate() != nil || state.Parent != "" && !digest.MatchString(state.Parent) {
		return state, fmt.Errorf("invalid native baseline")
	}
	return state, nil
}
func setSnapshotLineage(root string, b *Bundle) error {
	b.Snapshot = snapshotHash(b)
	state, e := loadSnapshotState(root)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if state.Baseline.Provider != b.Provider || state.Baseline.Format != b.Format || state.Baseline.SessionID != b.SessionID {
		return fmt.Errorf("native baseline belongs to another conversation")
	}
	b.Parent = state.Parent
	return nil
}
func loadSnapshotBaseline(root string, current *Bundle) (snapshotState, error) {
	state, err := loadSnapshotState(root)
	if os.IsNotExist(err) {
		return state, ErrDiverged
	}
	if err != nil {
		return state, err
	}
	baseline := state.Baseline
	if baseline.Provider != current.Provider || baseline.Format != current.Format || baseline.SessionID != current.SessionID || !reflect.DeepEqual(baseline.Files, current.Files) {
		return state, ErrDiverged
	}
	return state, nil
}
func CheckSnapshotBaseline(root string, current *Bundle) error {
	_, err := loadSnapshotBaseline(root, current)
	return err
}
func CheckSnapshotReplacement(root string, current, incoming *Bundle) error {
	if incoming.Snapshot != snapshotHash(incoming) {
		return fmt.Errorf("native snapshot identity does not match its files")
	}
	state, err := loadSnapshotBaseline(root, current)
	if err != nil {
		return err
	}
	if incoming.Provider != current.Provider || incoming.Format != current.Format || incoming.SessionID != current.SessionID || incoming.Snapshot != state.Baseline.Snapshot && incoming.Parent != state.Baseline.Snapshot {
		return ErrDiverged
	}
	return nil
}
func RememberSnapshotExport(root string, b *Bundle) error {
	return writeSnapshotState(root, snapshotState{Baseline: *b, Parent: b.Parent})
}
func rememberSnapshotImport(root string, current, incoming *Bundle) error {
	current.Snapshot = incoming.Snapshot
	current.Parent = incoming.Parent
	return writeSnapshotState(root, snapshotState{Baseline: *current, Parent: incoming.Snapshot})
}
func writeSnapshotState(root string, state snapshotState) error {
	p, e := Path(root, ".agentworks-last-export.json", true)
	if e != nil {
		return e
	}
	raw, e := json.Marshal(state)
	if e != nil {
		return e
	}
	return Write(root, p, raw)
}

// Native agent state has no views or triggers. A writable remote runtime must
// not supply schema code that runs during the importer's workspace remapping.
func secureNativeDB(db *sql.DB) error {
	db.SetMaxOpenConns(1)
	if _, e := db.Exec("PRAGMA trusted_schema=OFF"); e != nil {
		return e
	}
	var count int
	if e := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type IN ('trigger','view')").Scan(&count); e != nil {
		return e
	}
	if count != 0 {
		return fmt.Errorf("native SQLite schema contains unsupported triggers or views")
	}
	return nil
}

func snapshotHash(b *Bundle) string {
	raw, _ := json.Marshal(struct {
		Provider, Format, Session string
		Files                     map[string]File
	}{b.Provider, b.Format, b.SessionID, b.Files})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
