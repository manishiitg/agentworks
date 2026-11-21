package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// A run's files are the plain files in its own run folder, without bookkeeping, hidden files, links or folders.
func TestListCrewRunFilesShowsOnlyWhatTheRunMade(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	docs, state := filepath.Join(base, "docs"), filepath.Join(base, "state")
	project := filepath.Join(docs, "Crew", "example")
	for _, dir := range []string{filepath.Join(project, ".agentworks"), filepath.Join(docs, crewroot.PrivateDirectory, "crew"), filepath.Join(state, "ownership", "crew-executables")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	records, _ := json.Marshal(map[string]any{"projects": map[string]crewroot.Record{"work/example": {Product: "work", Folder: "example", ID: "crew", Owner: "owner"}}})
	if err := os.WriteFile(filepath.Join(state, "ownership", "projects.json"), records, 0o600); err != nil {
		t.Fatal(err)
	}
	binding := crewExecutableBinding{ID: "report", CrewID: "crew", CrewRoot: "Crew/example", Root: "Workflow/report", Kind: "script", RuntimeRoot: "automations/report"}
	raw, _ := json.Marshal(binding)
	if err := os.WriteFile(filepath.Join(state, "ownership", "crew-executables", fmt.Sprintf("%x.json", sha256.Sum256([]byte(binding.Root)))), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	run := filepath.Join(project, ".agentworks", "automations", "report", "runs", "run-1")
	for name, content := range map[string]string{
		"report.md": "# Report", "snapshot.json": "{}", "inputs.json": "{}", "run_metadata.json": "{}", "webhook_progress.json": "{}", ".webhook-run-id": "x",
		"execution/script-output.log": "log", "logs/a.json": "{}",
	} {
		file := filepath.Join(run, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(run, "report.md"), filepath.Join(run, "linked.md")); err != nil {
		t.Fatal(err)
	}
	// Another run's file must not show up.
	other := filepath.Join(project, ".agentworks", "automations", "report", "runs", "run-2", "other.md")
	if err := os.MkdirAll(filepath.Dir(other), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	files, err := listCrewRunFiles(binding, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, file := range files {
		got = append(got, file.Name+"="+file.Path)
	}
	want := []string{"report.md=Workflow/report/runs/run-1/report.md", "snapshot.json=Workflow/report/runs/run-1/snapshot.json"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("run files: got %v, want %v", got, want)
	}
	if files[0].Size != int64(len("# Report")) {
		t.Fatalf("size of report.md: %d", files[0].Size)
	}
	if none, err := listCrewRunFiles(binding, "run-missing"); err != nil || len(none) != 0 {
		t.Fatalf("a run with no folder has no files: %v %v", none, err)
	}
	for _, bad := range []string{"", ".", "..", "../run-1", "run-1/execution", `run-1\execution`, ".hidden"} {
		if _, err := listCrewRunFiles(binding, bad); err == nil {
			t.Errorf("run folder %q must be refused", bad)
		}
	}
}
