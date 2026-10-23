package dashboardjson

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise a real saved draft/publication and its exact-revision grant paths.
// Re-run is a no-op; the migration executes no data reads, scripts or functions.
func TestSavedJSONMigrationPreservesIdentityAndHistory(t *testing.T) {
	docs := filepath.Join(t.TempDir(), "workspace-docs")
	project := filepath.Join(docs, "Crew/example")
	rev := strings.Repeat("a", 64)
	state := `{"id":"overview","draft":"` + rev + `","published":"` + rev + `","history":["` + rev + `"]}`
	stateDir := filepath.Join(project, "db/reports/.dashboard-state/overview")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile("testdata/dashboard.json")
	if err != nil {
		t.Fatal(err)
	}
	snap := dashboards.Snapshot{Title: "Recorded sales", Files: map[string]string{"index.json": string(source), "index.html": "<html>Old JSON renderer</html>", "scripts/summary.py": "print('unchanged')"}}
	original, _ := json.Marshal(snap)
	snapshotPath := filepath.Join(stateDir, rev+".json")
	if err := os.WriteFile(snapshotPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	count, backup, err := Run(docs)
	if err != nil || count != 1 {
		t.Fatalf("migration count=%d err=%v", count, err)
	}
	after, err := os.ReadFile(filepath.Join(stateDir, "state.json"))
	if err != nil || string(after) != state {
		t.Fatal("publication or history changed")
	}
	saved, err := os.ReadFile(filepath.Join(backup, "Crew/example/db/reports/.dashboard-state/overview", rev+".json"))
	if err != nil || string(saved) != string(original) {
		t.Fatal("backup is not exact")
	}
	out, err := dashboards.Execute(context.Background(), docs, dashboards.Request{Root: "Crew/example", Action: "get", ID: "overview", Revision: rev})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out.Snapshot.Files["index.json"]; ok {
		t.Fatal("JSON source retained")
	}
	if !strings.Contains(out.Snapshot.Files["index.tsx"], "<C.LineChart") || out.Snapshot.Files["scripts/summary.py"] != "print('unchanged')" {
		t.Fatal("components or script changed")
	}
	if _, err := dashboards.Execute(context.Background(), docs, dashboards.Request{Root: "Crew/example", Action: "restore", ID: "overview", ExpectedRevision: rev, Revision: rev, IncludeDraft: true}); err != nil {
		t.Fatal(err)
	}
	if count, _, err := Run(docs); err != nil || count != 0 {
		t.Fatalf("migration rerun: %d %v", count, err)
	}
	if fixture := os.Getenv("DASHBOARD_MIGRATION_TEST_FIXTURE"); fixture != "" {
		html, err := os.ReadFile(filepath.Join(project, "db/reports/managed/overview", rev, "index.html"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixture, html, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
