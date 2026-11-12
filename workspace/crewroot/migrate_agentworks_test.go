package crewroot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// PLAT-746-924: the one-off move puts a Crew's records in .agentworks/settings,
// its runs, database and workflow runtime in .agentworks, keeps chats private,
// stops before any change when both places hold the same file, and can be put back.
func TestCrewFilesMoveIntoAgentworksOnce(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	docs, state := filepath.Join(base, "docs"), filepath.Join(base, "state")
	crew := filepath.Join(docs, "Crew", "example")
	private := filepath.Join(docs, PrivateDirectory, "crew")
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	write := func(p, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := json.Marshal(map[string]any{"projects": map[string]Record{"work/example": {Product: "work", Folder: "example", ID: "crew", Owner: "owner"}}})
	write(filepath.Join(state, "ownership", "projects.json"), string(data))
	sources := map[string]string{
		"product.json":                           ".agentworks/settings/product.json",
		"schedules.json":                         ".agentworks/settings/schedules.json",
		"triggers/deliveries/d.json":             ".agentworks/settings/triggers/deliveries/d.json",
		"runs/run-1/log.txt":                     ".agentworks/runs/run-1/log.txt",
		"db/db.sqlite":                           ".agentworks/db/db.sqlite",
		"workflows/wf/runs/run-2/log.txt":        ".agentworks/workflows/wf/runs/run-2/log.txt",
		"workflows/wf/learnings/note.md":         ".agentworks/workflows/wf/learnings/note.md",
		"workflows/wf/schedules.json":            ".agentworks/settings/workflows/wf/schedules.json",
		"workflows/wf/config/history.json":       ".agentworks/settings/workflows/wf/config/history.json",
		"workflows/wf/planning/changelog/c.json": ".agentworks/workflows/wf/planning/changelog/c.json",
	}
	for rel := range sources {
		write(filepath.Join(private, rel), rel)
	}
	write(filepath.Join(private, "builder/conversation/chat.json"), "chat")
	write(filepath.Join(crew, ".agentworks/workflows/wf/workflow.json"), "definition")
	write(filepath.Join(crew, "db/assets/report.html"), "asset")

	// The same file in both places stops the move before anything changes.
	write(filepath.Join(crew, ".agentworks/runs/run-1/log.txt"), "other")
	if _, _, err := MigrateToAgentworks(docs); err == nil {
		t.Fatal("conflicting copies were merged")
	}
	if _, err := os.Stat(filepath.Join(private, "product.json")); err != nil {
		t.Fatalf("a refused move changed files: %v", err)
	}
	os.RemoveAll(filepath.Join(crew, ".agentworks/runs"))

	count, journal, err := MigrateToAgentworks(docs)
	if err != nil || count == 0 {
		t.Fatalf("move: %d %v", count, err)
	}
	for rel, target := range sources {
		if got, err := os.ReadFile(filepath.Join(crew, target)); err != nil || string(got) != rel {
			t.Fatalf("%s -> %s: %q %v", rel, target, got, err)
		}
	}
	for _, kept := range []string{filepath.Join(private, "builder/conversation/chat.json"), filepath.Join(crew, ".agentworks/workflows/wf/workflow.json"), filepath.Join(crew, ".agentworks/db/assets/report.html"), filepath.Join(crew, ".agentworks/.gitignore")} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("missing after move: %s", kept)
		}
	}
	if again, _, err := MigrateToAgentworks(docs); err != nil || again != 0 {
		t.Fatalf("second start moved again: %d %v", again, err)
	}
	if err := RestoreAgentworksMove(journal); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(private, "workflows/wf/schedules.json")); err != nil || string(got) != "workflows/wf/schedules.json" {
		t.Fatalf("restore: %q %v", got, err)
	}
}
