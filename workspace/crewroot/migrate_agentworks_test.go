package crewroot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PLAT-746-924: the one-off move puts a Crew's records in .agentworks/settings,
// its runs, database and workflow runtime in .agentworks, keeps chats private,
// stops before any change when both places hold differing copies of a file,
// keeps the .agentworks copy when both are identical or the file is a
// regenerated marker (a laptop refused to start on these), and can be put back.
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
	// Already in both places: identical bytes, and a regenerated marker that differs.
	write(filepath.Join(private, "workflows/wf/workflow.json.kb-lock"), "")
	write(filepath.Join(crew, ".agentworks/workflows/wf/workflow.json.kb-lock"), "")
	write(filepath.Join(private, "workflows/wf/.workspace-skills-v1"), "old marker")
	write(filepath.Join(crew, ".agentworks/workflows/wf/.workspace-skills-v1"), "Workspace skills migrated.")

	// The same file in both places stops the move before anything changes.
	write(filepath.Join(crew, ".agentworks/runs/run-1/log.txt"), "other")
	// The refusal names both copies with their size so a person can choose.
	if _, _, err := MigrateToAgentworks(docs); err == nil {
		t.Fatal("conflicting copies were merged")
	} else if msg := err.Error(); !strings.Contains(msg, filepath.Join(private, "runs/run-1/log.txt")+" (18 bytes") || !strings.Contains(msg, filepath.Join(crew, ".agentworks/runs/run-1/log.txt")+" (5 bytes") {
		t.Fatalf("refusal does not name both copies: %v", err)
	}
	if _, err := os.Stat(filepath.Join(private, "product.json")); err != nil {
		t.Fatalf("a refused move changed files: %v", err)
	}
	os.RemoveAll(filepath.Join(crew, ".agentworks/runs"))
	// A clash through an earlier planned move names the file that really holds
	// the other copy: the private database and the Crew folder's db/ both go to .agentworks/db.
	write(filepath.Join(crew, "db/db.sqlite"), "other db")
	if _, _, err := MigrateToAgentworks(docs); err == nil {
		t.Fatal("two different databases were merged")
	} else if msg := err.Error(); !strings.Contains(msg, filepath.Join(private, "db/db.sqlite")+" (12 bytes") || !strings.Contains(msg, filepath.Join(crew, "db/db.sqlite")+" (8 bytes") || strings.Contains(msg, ".agentworks/db/db.sqlite") {
		t.Fatalf("refusal does not name both real database files: %v", err)
	}
	os.Remove(filepath.Join(crew, "db/db.sqlite"))

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
	for dup, want := range map[string]string{"workflows/wf/workflow.json.kb-lock": "", "workflows/wf/.workspace-skills-v1": "Workspace skills migrated."} {
		if got, err := os.ReadFile(filepath.Join(crew, ".agentworks", dup)); err != nil || string(got) != want {
			t.Fatalf("duplicate %s: kept %q %v", dup, got, err)
		}
		if _, err := os.Lstat(filepath.Join(private, dup)); !os.IsNotExist(err) {
			t.Fatalf("duplicate %s left in the private root", dup)
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
	if _, err := os.Stat(filepath.Join(private, "workflows/wf/workflow.json.kb-lock")); err != nil {
		t.Fatalf("restore did not put the removed duplicate back: %v", err)
	}
}
