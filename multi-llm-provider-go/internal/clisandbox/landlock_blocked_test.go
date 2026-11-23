package clisandbox

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestSplitAroundBlocked(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wf := filepath.Join(root, "Workflow", "w")
	for _, dir := range []string{"planning", "code/step", "runs", "db"} {
		if err := os.MkdirAll(filepath.Join(wf, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"workflow.json", "AGENTS.md", "db/db.sqlite", "db/notes.md", "planning/plan.json"} {
		if err := os.WriteFile(filepath.Join(wf, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	other := filepath.Join(root, "Other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}

	read, write := splitAroundBlocked(
		[]string{filepath.Join(root, "Workflow")},
		[]string{wf, other},
		[]string{filepath.Join(wf, "db", "db.sqlite")},
		[]string{filepath.Join(wf, "planning"), filepath.Join(wf, "AGENTS.md")},
	)
	has := func(list []string, p string) bool {
		i := sort.SearchStrings(list, p)
		return i < len(list) && list[i] == p
	}
	for _, p := range []string{filepath.Join(wf, "code"), filepath.Join(wf, "runs"), filepath.Join(wf, "workflow.json"), filepath.Join(wf, "db", "notes.md"), other} {
		if !has(write, p) {
			t.Errorf("write is missing %s: %v", p, write)
		}
	}
	for _, p := range []string{wf, filepath.Join(wf, "planning"), filepath.Join(wf, "AGENTS.md"), filepath.Join(wf, "db"), filepath.Join(wf, "db", "db.sqlite")} {
		if has(write, p) {
			t.Errorf("write still grants %s", p)
		}
	}
	// Blocked-write paths stay readable; the read-blocked database is cut out
	// of every read grant, so neither the workflow folder nor its parent is
	// readable as a whole any more (listing still works through ListPaths).
	for _, p := range []string{filepath.Join(wf, "planning"), filepath.Join(wf, "AGENTS.md"), filepath.Join(wf, "workflow.json"), filepath.Join(wf, "db", "notes.md")} {
		if !has(read, p) {
			t.Errorf("read is missing %s: %v", p, read)
		}
	}
	for _, p := range []string{filepath.Join(wf, "db", "db.sqlite"), filepath.Join(wf, "db"), wf, filepath.Join(root, "Workflow")} {
		if has(read, p) {
			t.Errorf("read still grants %s (contains or is the read-blocked database)", p)
		}
	}
}
