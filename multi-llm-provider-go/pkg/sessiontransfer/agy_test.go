package sessiontransfer

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

const agyTestID = "11111111-aaaa-4bbb-8ccc-222222222222"
const agyTestTrajectory = "33333333-aaaa-4bbb-8ccc-444444444444"

// writeAgyConversation makes a conversation file with agy 1.5.2's layout.
func writeAgyConversation(t *testing.T, store, id string, steps ...int) string {
	t.Helper()
	p := filepath.Join(store, id+".db")
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		"CREATE TABLE trajectory_meta (trajectory_id text, cascade_id text, trajectory_type integer, source integer, PRIMARY KEY (trajectory_id))",
		"CREATE TABLE steps (idx integer, step_type integer NOT NULL DEFAULT 0, status integer NOT NULL DEFAULT 0, has_subtrajectory numeric NOT NULL DEFAULT false, metadata blob, error_details blob, permissions blob, task_details blob, render_info blob, step_payload blob, step_format integer NOT NULL DEFAULT 0, PRIMARY KEY (idx))",
		"CREATE TABLE parent_references (idx integer, data blob, PRIMARY KEY (idx))",
		"CREATE TABLE battle_mode_infos (idx integer, data blob, PRIMARY KEY (idx))",
		"CREATE TABLE gen_metadata (idx integer, data blob, size integer, PRIMARY KEY (idx))",
		"CREATE TABLE executor_metadata (idx integer, data blob, PRIMARY KEY (idx))",
		"CREATE TABLE trajectory_metadata_blob (id text, data blob, PRIMARY KEY (id))",
	} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Exec("INSERT INTO trajectory_meta VALUES (?,?,4,17)", agyTestTrajectory, id)
	db.Exec("INSERT INTO executor_metadata VALUES (0, ?)", []byte("file:///work/a "+id))
	db.Exec("INSERT INTO trajectory_metadata_blob VALUES ('main', ?)", []byte(agyTestTrajectory))
	appendAgySteps(t, p, steps...)
	return p
}

func appendAgySteps(t *testing.T, path string, kinds ...int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var next int
	db.QueryRow("SELECT COALESCE(MAX(idx)+1,0) FROM steps").Scan(&next)
	for i, kind := range kinds {
		if _, err = db.Exec("INSERT INTO steps (idx, step_type, status, step_payload) VALUES (?,?,3,?)", next+i, kind, []byte{byte(kind), byte(next + i), 'x'}); err != nil {
			t.Fatal(err)
		}
	}
}

// An agy conversation moves with its steps intact, resumes under the destination's
// folder without rewriting, and a copy that only the other side extended is an
// ancestor; two sides that both continued are kept apart. A fork is independent.
func TestAgyNativeRoundTripAncestorDivergeAndFork(t *testing.T) {
	storeA, storeB, cwdA, cwdB := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	pathA := writeAgyConversation(t, storeA, agyTestID, 14, 15)
	first, err := ExportAgy("exec", storeA, cwdA, agyTestID, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := ImportAgy(storeB, cwdB, first); err != nil {
		t.Fatal(err)
	}
	second, err := ExportAgy("tmux", storeB, cwdB, agyTestID, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := Ancestor(first, second); err != nil {
		t.Fatalf("round trip is not its own ancestor: %v", err)
	}
	appendAgySteps(t, filepath.Join(storeB, agyTestID+".db"), 14, 15)
	grown, err := ExportAgy("exec", storeB, cwdB, agyTestID, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := ImportAgy(storeA, cwdA, grown); err != nil {
		t.Fatalf("hand back over the older copy: %v", err)
	}
	// Both sides continue from the same point: neither import may overwrite.
	appendAgySteps(t, pathA, 14)
	if err := ImportAgy(storeA, cwdA, grown); err != ErrDiverged {
		t.Fatalf("diverged copy: %v", err)
	}
	appendAgySteps(t, filepath.Join(storeB, agyTestID+".db"), 15)
	if err := ImportAgy(storeB, cwdB, mustExport(t, storeA, cwdA)); err != ErrDiverged {
		t.Fatalf("diverged copy on the other side: %v", err)
	}

	fork, err := ForkJSONL(grown, t.TempDir(), cwdB)
	if err != nil {
		t.Fatal(err)
	}
	if fork.SessionID == agyTestID {
		t.Fatal("fork shared the source identity")
	}
	if err := ImportAgy(storeB, cwdB, fork); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(storeB, agyTestID+".db")); err != nil {
		t.Fatal("fork replaced the original")
	}
	copied, err := os.ReadFile(filepath.Join(storeB, fork.SessionID+".db"))
	if err != nil || bytes.Contains(copied, []byte(agyTestID)) || bytes.Contains(copied, []byte(agyTestTrajectory)) {
		t.Fatal("fork still names the source conversation")
	}
}

func mustExport(t *testing.T, store, cwd string) *Bundle {
	t.Helper()
	b, err := ExportAgy("exec", store, cwd, agyTestID, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// An agy bundle cannot reach outside the store, carry another conversation, an unknown table,
// a login in its configuration blobs, or a reply that is still running.
func TestAgyNativeRejectsLinksForeignStateAndUnfinishedTurns(t *testing.T) {
	store, cwd := t.TempDir(), t.TempDir()
	path := writeAgyConversation(t, store, agyTestID, 14, 15)
	b := mustExport(t, store, cwd)
	b.Files["../../auth.json"] = b.Files[agyRoot]
	if b.Validate() == nil {
		t.Fatal("accepted a path outside the conversation")
	}
	if _, err := ExportAgy("exec", store, cwd, "../"+agyTestID, t.TempDir()); err == nil {
		t.Fatal("accepted a path as a conversation id")
	}
	const other = "55555555-aaaa-4bbb-8ccc-666666666666"
	raw, _ := os.ReadFile(path)
	os.WriteFile(filepath.Join(store, other+".db"), raw, 0600)
	if _, err := ExportAgy("exec", store, cwd, other, t.TempDir()); err == nil {
		t.Fatal("exported a file that holds another conversation")
	}
	link := t.TempDir()
	if err := os.Symlink(path, filepath.Join(link, agyTestID+".db")); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportAgy("exec", link, cwd, agyTestID, t.TempDir()); err == nil {
		t.Fatal("followed a link")
	}
	db, _ := sql.Open("sqlite", "file:"+path)
	defer db.Close()
	db.Exec("UPDATE steps SET status=1 WHERE idx=1")
	if _, err := ExportAgy("exec", store, cwd, agyTestID, t.TempDir()); err == nil {
		t.Fatal("exported a conversation whose reply is still running")
	}
	db.Exec("UPDATE steps SET status=3")
	db.Exec("UPDATE executor_metadata SET data=?", []byte("token=ya29.secret"))
	if _, err := ExportAgy("exec", store, cwd, agyTestID, t.TempDir()); err == nil {
		t.Fatal("exported a login")
	}
	db.Exec("UPDATE executor_metadata SET data=?", []byte("ok"))
	db.Exec("CREATE TABLE accounts (token text)")
	if _, err := ExportAgy("exec", store, cwd, agyTestID, t.TempDir()); err == nil {
		t.Fatal("exported an unknown table")
	}
}
