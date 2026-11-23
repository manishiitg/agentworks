package sessiontransfer

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// PLAT-856-442: a private SDK snapshot must exclude other chats, remap only
// the selected agent's workspace, and keep the offline branch on a conflict.
func TestCursorSDKSnapshotRejectsForeignAgentsAndOfflineFork(t *testing.T) {
	store, work, target, targetWork := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	id := "agent-11111111-1111-4111-8111-111111111111"
	db, e := sql.Open("sqlite", filepath.Join(store, "index.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	for _, query := range []string{
		`CREATE TABLE agents(agent_id TEXT PRIMARY KEY,workspace_ref TEXT,status TEXT,active_run_id TEXT,metadata_json TEXT)`,
		`CREATE TABLE runs(run_id TEXT PRIMARY KEY,agent_id TEXT)`,
		`CREATE TABLE run_events(run_id TEXT)`,
	} {
		if _, e = db.Exec(query); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = db.Exec(`INSERT INTO agents VALUES(?,?,'IDLE',NULL,'{}')`, id, work); e != nil {
		t.Fatal(e)
	}
	bundle, e := ExportCursorSDK(store, work, id, t.TempDir(), true)
	if e != nil {
		t.Fatal(e)
	}
	if e = ImportCursorSDK(target, targetWork, bundle); e != nil {
		t.Fatal(e)
	}
	returned, e := ExportCursorSDK(target, targetWork, id, t.TempDir(), true)
	if e != nil {
		t.Fatal(e)
	}
	if returned.SessionID != id {
		t.Fatal("SDK native ID changed")
	}
	if _, e = os.Stat(filepath.Join(target, ".agentworks-native-handoff.json")); e != nil {
		t.Fatal(e)
	}
	// PLAT-856-442 review: imported triggers must be refused before remapping.
	malicious := *bundle
	malicious.Root = t.TempDir()
	for name := range bundle.Files {
		source, _ := Path(bundle.Root, name, false)
		dest, _ := Path(malicious.Root, name, true)
		if e = Copy(malicious.Root, dest, source); e != nil {
			t.Fatal(e)
		}
	}
	injected, e := sql.Open("sqlite", filepath.Join(malicious.Root, "index.db"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = injected.Exec("CREATE TRIGGER injected AFTER UPDATE ON agents BEGIN DELETE FROM runs; END")
	injected.Close()
	if e != nil {
		t.Fatal(e)
	}
	malicious.Files = map[string]File{}
	for name := range bundle.Files {
		malicious.Files[name], e = Inspect(filepath.Join(malicious.Root, name))
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = ImportCursorSDK(t.TempDir(), targetWork, &malicious); e == nil || !strings.Contains(e.Error(), "triggers or views") {
		t.Fatalf("schema code accepted or wrong refusal: %v", e)
	}
	// An actual committed offline change must refuse a return, not overwrite it.
	if _, e = db.Exec(`INSERT INTO runs VALUES('offline-turn',?)`, id); e != nil {
		t.Fatal(e)
	}
	if e = ImportCursorSDK(store, work, returned); e == nil || !errors.Is(e, ErrDiverged) {
		t.Fatalf("offline fork accepted: %v", e)
	}
	var kept int
	if e = db.QueryRow(`SELECT COUNT(*) FROM runs WHERE run_id='offline-turn'`).Scan(&kept); e != nil || kept != 1 {
		t.Fatalf("offline data lost: %d %v", kept, e)
	}
	// Both computers may export an offline branch; export must not make
	// that branch safe to overwrite with the other computer's child of A.
	if _, e = ExportCursorSDK(store, work, id, t.TempDir(), true); e != nil {
		t.Fatal(e)
	}
	remoteDB, e := sql.Open("sqlite", filepath.Join(target, "index.db"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = remoteDB.Exec("INSERT INTO runs VALUES('remote-turn',?)", id); e != nil {
		t.Fatal(e)
	}
	remoteDB.Close()
	remote, e := ExportCursorSDK(target, targetWork, id, t.TempDir(), true)
	if e != nil {
		t.Fatal(e)
	}
	if e = ImportCursorSDK(store, work, remote); e == nil || !errors.Is(e, ErrDiverged) {
		t.Fatalf("dual-export fork overwritten: %v", e)
	}
	// A malformed packet cannot call different file bytes an identical replay.
	forged := *remote
	localCurrent, e := ExportCursorSDK(store, work, id, t.TempDir(), false)
	if e != nil {
		t.Fatal(e)
	}
	forged.Snapshot = localCurrent.Snapshot
	if e = ImportCursorSDK(store, work, &forged); e == nil || !strings.Contains(e.Error(), "identity does not match") {
		t.Fatalf("forged replay accepted: %v", e)
	}
	// An older packet cannot roll back a destination that accepted a newer one.
	clean, cleanWork := t.TempDir(), t.TempDir()
	if e = ImportCursorSDK(clean, cleanWork, bundle); e != nil {
		t.Fatal(e)
	}
	if e = ImportCursorSDK(clean, cleanWork, remote); e != nil {
		t.Fatal(e)
	}
	if e = ImportCursorSDK(clean, cleanWork, bundle); e == nil || !errors.Is(e, ErrDiverged) {
		t.Fatalf("older snapshot rolled history back: %v", e)
	}
	// Refusal must come from the mixed-agent rule with a usable store.
	if _, e = db.Exec(`INSERT INTO agents VALUES('agent-other',?,'IDLE',NULL,'{}')`, work); e != nil {
		t.Fatal(e)
	}
	if _, e = ExportCursorSDK(store, work, id, t.TempDir(), false); e == nil || !strings.Contains(e.Error(), "another conversation") {
		t.Fatalf("foreign agent leaked or wrong refusal: %v", e)
	}
}

// PLAT-856-442: the terminal format keeps opaque native blobs, remaps only cwd,
// refuses schema code, and preserves a locally edited store on a return fork.
func TestCursorCLISnapshotPreservesBlobsAndRefusesSchemaAndFork(t *testing.T) {
	config, work, destination, targetWork := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	id := "11111111-1111-4111-8111-111111111111"
	root, e := CursorCLIPath(config, work, id)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, "meta.json"), []byte(`{"schemaVersion":1,"cwd":`+strconv.Quote(work)+`}`), 0600); e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("sqlite", filepath.Join(root, "store.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(`CREATE TABLE blobs(id TEXT PRIMARY KEY,data BLOB); INSERT INTO blobs VALUES('checkpoint',X'01020304')`); e != nil {
		t.Fatal(e)
	}
	// The private wrapper can link its admitted login. It is not chat state
	// and must never enter a native transfer (PLAT-856-442).
	login := filepath.Join(t.TempDir(), "auth.json")
	if e = os.WriteFile(login, []byte("private-login-fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(login, filepath.Join(config, "auth.json")); e != nil {
		t.Fatal(e)
	}
	b, e := ExportCursorCLI(config, work, id, t.TempDir(), true)
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := b.Files["auth.json"]; ok {
		t.Fatal("login was declared as native chat state")
	}
	if _, e = os.Lstat(filepath.Join(b.Root, "auth.json")); !os.IsNotExist(e) {
		t.Fatal("login entered native transfer staging")
	}
	escapedConfig, escaped := t.TempDir(), t.TempDir()
	linked, _ := CursorCLIPath(escapedConfig, targetWork, id)
	if e = os.MkdirAll(filepath.Dir(linked), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(escaped, linked); e != nil {
		t.Fatal(e)
	}
	if e = ImportCursorCLI(escapedConfig, targetWork, b); e == nil || !strings.Contains(e.Error(), "link") {
		t.Fatalf("chat-directory link accepted: %v", e)
	}
	if _, e = os.Stat(filepath.Join(escaped, "store.db")); !os.IsNotExist(e) {
		t.Fatal("import escaped account chat root")
	}
	if e = ImportCursorCLI(destination, targetWork, b); e != nil {
		t.Fatal(e)
	}
	back, e := ExportCursorCLI(destination, targetWork, id, t.TempDir(), true)
	if e != nil {
		t.Fatal(e)
	}
	targetRoot, _ := CursorCLIPath(destination, targetWork, id)
	check, e := sql.Open("sqlite", filepath.Join(targetRoot, "store.db"))
	if e != nil {
		t.Fatal(e)
	}
	var blob string
	e = check.QueryRow("SELECT hex(data) FROM blobs WHERE id='checkpoint'").Scan(&blob)
	check.Close()
	if e != nil || blob != "01020304" {
		t.Fatal("native blob value changed")
	}
	db.Close()
	if e = ImportCursorCLI(config, work, back); e != nil {
		t.Fatal(e)
	}
	db, e = sql.Open("sqlite", filepath.Join(root, "store.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(`INSERT INTO blobs VALUES('offline',X'05')`); e != nil {
		t.Fatal(e)
	}
	if e = ImportCursorCLI(config, work, back); e == nil || !errors.Is(e, ErrDiverged) {
		t.Fatalf("offline terminal fork accepted: %v", e)
	}
	// A link below the resolved account root must still be refused.
	original := filepath.Join(root, "meta.json")
	saved, e := os.ReadFile(original)
	if e != nil {
		t.Fatal(e)
	}
	outside := filepath.Join(t.TempDir(), "meta.json")
	if e = os.WriteFile(outside, saved, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(original); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(outside, original); e != nil {
		t.Fatal(e)
	}
	if _, e = ExportCursorCLI(config, work, id, t.TempDir(), false); e == nil || !strings.Contains(e.Error(), "link") {
		t.Fatalf("nested native link accepted: %v", e)
	}
	if e = os.Remove(original); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(original, saved, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`CREATE VIEW injected AS SELECT data FROM blobs`); e != nil {
		t.Fatal(e)
	}
	if _, e = ExportCursorCLI(config, work, id, t.TempDir(), false); e == nil || !strings.Contains(e.Error(), "triggers or views") {
		t.Fatalf("native view accepted: %v", e)
	}
}
