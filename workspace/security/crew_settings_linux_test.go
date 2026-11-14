//go:build linux

package security

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-746-924: a Crew's records sit in its .agentworks/settings, inside the folder a Builder chat may write. Through
// the real shell path (registry, grants, launcher) the records stay read-only while the Crew folder, its runs and a
// workflow run's database stay writable, including new files at the top of the Crew.
func TestCrewSettingsAreReadOnlyToItsShells(t *testing.T) {
	if _, err := landlockRunnerPath(); err != nil || !landlockNamespacesAvailable() {
		t.Skip("needs the Landlock launcher with its namespaces (AGENTWORKS_LANDLOCK_RUNNER)")
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	docs, state := filepath.Join(base, "docs"), filepath.Join(base, "state")
	crew := filepath.Join(docs, "Crew", "example")
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	write := func(p, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	registry, _ := json.Marshal(map[string]any{"projects": map[string]crewroot.Record{"work/example": {Product: "work", Folder: "example", ID: "crew", Owner: "owner"}}})
	write(filepath.Join(state, "ownership", "projects.json"), string(registry))
	binding := crewroot.ExecutableBinding{ID: "wf", Root: "Workflow/wf", CrewID: "crew", CrewRoot: "Crew/example", Kind: "workflow", RuntimeRoot: "workflows/wf"}
	raw, _ := json.Marshal(binding)
	write(filepath.Join(state, "ownership", "crew-executables", fmt.Sprintf("%x.json", sha256.Sum256([]byte(binding.Root)))), string(raw))
	write(filepath.Join(crew, "notes.md"), "notes")
	if err := os.MkdirAll(filepath.Join(docs, crewroot.PrivateDirectory, "crew"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, _, err := crewroot.Lookup(docs, "Crew/example"); err != nil { // creates .agentworks/settings
		t.Fatal(err)
	}
	schedules := filepath.Join(crew, ".agentworks", "settings", "schedules.json")
	write(schedules, "ORIGINAL")
	db := filepath.Join(crew, ".agentworks", "workflows", "wf", "db", "db.sqlite")
	write(db, "")
	run := func(t *testing.T, iso *Isolator, script string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd, cleanup, err := iso.ExecuteIsolated(ctx, "sh", []string{"-c", script})
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			t.Fatalf("ExecuteIsolated: %v", err)
		}
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	t.Run("Builder chat", func(t *testing.T) {
		iso := &Isolator{BaseDir: docs, WorkDir: crew, ReadPaths: []string{"Crew/example"}, WritePaths: []string{"Crew/example"}}
		got := run(t, iso, fmt.Sprintf(`cat %q >/dev/null && echo READ_SETTINGS
echo changed > %q 2>/dev/null && echo WROTE_SETTINGS
echo new > %q 2>/dev/null && echo NEW_SETTINGS
mkdir -p %q && echo log > %q && echo WROTE_RUNS
echo top > %q && echo NEW_TOP`, schedules, schedules, filepath.Join(filepath.Dir(schedules), "webhooks.json"),
			filepath.Join(crew, ".agentworks", "runs", "run-1"), filepath.Join(crew, ".agentworks", "runs", "run-1", "log.txt"), filepath.Join(crew, "new.md")))
		for _, want := range []string{"READ_SETTINGS", "WROTE_RUNS", "NEW_TOP"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s missing: %s", want, got)
			}
		}
		for _, refused := range []string{"WROTE_SETTINGS", "NEW_SETTINGS"} {
			if strings.Contains(got, refused) {
				t.Errorf("records were writable (%s): %s", refused, got)
			}
		}
		if raw, _ := os.ReadFile(schedules); string(raw) != "ORIGINAL" {
			t.Errorf("schedules.json changed: %q", raw)
		}
	})
	t.Run("workflow run", func(t *testing.T) {
		iso := &Isolator{BaseDir: docs, WorkDir: filepath.Dir(db), ReadPaths: []string{"Workflow/wf"}, WritePaths: []string{"Workflow/wf/db"}}
		got := run(t, iso, fmt.Sprintf(`echo row >> %q && echo WROTE_DB
echo j > %q && echo WROTE_JOURNAL
echo changed > %q 2>/dev/null && echo WROTE_SETTINGS`, db, db+"-journal", schedules))
		for _, want := range []string{"WROTE_DB", "WROTE_JOURNAL"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s missing: %s", want, got)
			}
		}
		if strings.Contains(got, "WROTE_SETTINGS") {
			t.Errorf("a workflow run wrote the Crew records: %s", got)
		}
	})
}
