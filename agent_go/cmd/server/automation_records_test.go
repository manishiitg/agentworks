package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// #3156: an automation record moves from its file into the server state
// database on first use (the file deleted, its bytes kept in the private
// backup), later reads and saves never reach the workspace, and a deleted and
// recreated folder does not inherit the old schedules.
func TestAutomationRecordsMoveInAndStayWithTheirFolder(t *testing.T) {
	base := t.TempDir()
	docs, state := filepath.Join(base, "docs"), filepath.Join(base, "state")
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	folder := filepath.Join(docs, "Workflow", "goal")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	const old = `{"schedules":[]}`
	file := filepath.Join(folder, "schedules.json")
	if err := os.WriteFile(file, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	var recordCalls atomic.Int32
	workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/schedules.json") {
			http.NotFound(w, r)
			return
		}
		recordCalls.Add(1)
		switch r.Method {
		case http.MethodGet:
			raw, err := os.ReadFile(file)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"data":{"content":` + quoteJSON(string(raw)) + `}}`))
		case http.MethodDelete:
			_ = os.Remove(file)
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("automation record written to the workspace: %s", r.Method)
		}
	}))
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	ctx := context.Background()
	const virtual = "Workflow/goal/schedules.json"
	if raw, found, err := readFileFromWorkspace(ctx, virtual); err != nil || !found || raw != old {
		t.Fatalf("first read: %q %v %v", raw, found, err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("old file still in place: %v", err)
	}
	dbFile, err := serverStateDatabasePath()
	if err != nil {
		t.Fatal(err)
	}
	if backup, err := os.ReadFile(filepath.Join(filepath.Dir(dbFile), "automation-records.imported.jsonl")); err != nil || !strings.Contains(string(backup), quoteJSON(old)) {
		t.Fatalf("backup: %q %v", backup, err)
	}
	const saved = `{"schedules":[{"id":"daily"}]}`
	if err := writeFileToWorkspace(ctx, virtual, saved); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if raw, found, err := readFileFromWorkspace(ctx, virtual); err != nil || !found || raw != saved {
			t.Fatalf("saved read: %q %v %v", raw, found, err)
		}
	}
	if n := recordCalls.Load(); n != 2 { // the one-off GET and DELETE
		t.Fatalf("workspace calls for the record = %d, want 2", n)
	}
	// #4009: a Watcher record stored here still named code/triggers/ after the scripts moved, and the scheduler
	// skipped the project ("script must be a Python file under watchers/"); the startup rewrite fixes the row.
	if err := writeFileToWorkspace(ctx, virtual, `{"schedules":[{"condition":{"script":"code/triggers/check.py"}}]}`); err != nil {
		t.Fatal(err)
	}
	rewriteAutomationRecordScriptPaths(ctx)
	if raw, _, err := readFileFromWorkspace(ctx, virtual); err != nil || raw != `{"schedules":[{"condition":{"script":"watchers/check.py"}}]}` {
		t.Fatalf("Watcher record not rewritten: %q %v", raw, err)
	}
	if backup, err := os.ReadFile(filepath.Join(filepath.Dir(dbFile), "automation-records.platform-scripts.jsonl")); err != nil || !strings.Contains(string(backup), "code/triggers/check.py") {
		t.Fatalf("record backup: %q %v", backup, err)
	}
	// A person's own Watcher lives in their chat_history/product-schedules.json, which the scheduler also skipped.
	personal := filepath.Join(docs, "_users", "person", "chat_history", productScheduleStateFile)
	if err := os.MkdirAll(filepath.Dir(personal), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(personal, []byte(`{"w":{"script":"code/triggers/cond-1.py"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rewritePersonalWatcherScriptPaths(docs)
	if raw, err := os.ReadFile(personal); err != nil || string(raw) != `{"w":{"script":"watchers/cond-1.py"}}` {
		t.Fatalf("personal Watcher not rewritten: %q %v", raw, err)
	}
	if backups, _ := filepath.Glob(personal + ".before-platform-scripts-*.bak"); len(backups) != 1 {
		t.Fatalf("personal Watcher backup: %v", backups)
	}
	if err := os.RemoveAll(folder); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	forgetAutomationRecordsUnder(ctx, folder) // what deleting the folder through the platform does
	if raw, found, err := readFileFromWorkspace(ctx, virtual); err != nil || found {
		t.Fatalf("recreated folder inherited %q %v", raw, err)
	}
}

func quoteJSON(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}
