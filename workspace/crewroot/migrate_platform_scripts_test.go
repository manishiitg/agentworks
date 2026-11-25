package crewroot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #4009: dashboard, Watcher and motion scripts leave code/ for .agentworks/ once; saved pages and records that name
// the old paths are rewritten, everything moved is backed up first, and the person's own code/ files stay.
func TestPlatformScriptsMoveIntoAgentworksOnce(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	docs, state := filepath.Join(base, "docs"), filepath.Join(base, "state")
	crew := filepath.Join(docs, "Crew", "example")
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
	read := func(p string) string {
		t.Helper()
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	data, _ := json.Marshal(map[string]any{"projects": map[string]Record{"work/example": {Product: "work", Folder: "example", ID: "crew", Owner: "owner"}}})
	write(filepath.Join(state, "ownership", "projects.json"), string(data))
	write(filepath.Join(crew, "code/reports/managed/sales/r1/data.py"), "print(1)")
	write(filepath.Join(crew, "code/triggers/w1.py"), "print(2)")
	write(filepath.Join(crew, "code/motion/clip/index.html"), "<p>clip</p>")
	write(filepath.Join(crew, "code/mine.py"), "own")
	page := filepath.Join(crew, ".agentworks/db/reports/managed/sales/r1/index.html")
	write(page, `<script>report.run('code/reports/managed/sales/r1/data.py')</script>`)
	schedules := filepath.Join(crew, ".agentworks/settings/product-schedules.json")
	write(schedules, `{"script":"code/triggers/w1.py"}`)
	write(filepath.Join(docs, "Workflow/inv/code/triggers/x.py"), "print(3)")
	write(filepath.Join(docs, "Workflow/inv/schedules.json"), `{"script":"code/triggers/x.py"}`)

	moved, rewritten, backup, err := MigratePlatformScripts(docs)
	if err != nil || moved != 4 || rewritten != 3 {
		t.Fatalf("moved=%d rewritten=%d err=%v", moved, rewritten, err)
	}
	for _, p := range []string{".agentworks/dashboards/sales/r1/data.py", ".agentworks/watchers/w1.py", ".agentworks/motion/clip/index.html", "code/mine.py"} {
		if _, err := os.Stat(filepath.Join(crew, p)); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	for _, p := range []string{"code/reports", "code/triggers", "code/motion"} {
		if _, err := os.Stat(filepath.Join(crew, p)); !os.IsNotExist(err) {
			t.Fatalf("%s is still there: %v", p, err)
		}
	}
	if got := read(page); !strings.Contains(got, "report.run('dashboards/sales/r1/data.py')") {
		t.Fatalf("page: %s", got)
	}
	if got := read(schedules); got != `{"script":"watchers/w1.py"}` {
		t.Fatalf("watcher record: %s", got)
	}
	if got := read(filepath.Join(docs, "Workflow/inv/schedules.json")); got != `{"script":"watchers/x.py"}` || read(filepath.Join(docs, "Workflow/inv/watchers/x.py")) != "print(3)" {
		t.Fatalf("workflow watcher: %s", got)
	}
	if got := read(filepath.Join(backup, "000", "code/reports/managed/sales/r1/data.py")); got != "print(1)" {
		t.Fatalf("backup: %s", got)
	}
	// The new folders resolve into .agentworks/ for this project.
	roots, found, err := Lookup(docs, "Crew/example")
	if err != nil || !found || roots.ResourcePathRoot("watchers/w1.py") != roots.Automation {
		t.Fatalf("watchers/ resolves to %q, not .agentworks", roots.ResourcePathRoot("watchers/w1.py"))
	}

	write(filepath.Join(crew, "code/triggers/late.py"), "print(4)")
	if again, _, _, err := MigratePlatformScripts(docs); err != nil || again != 0 {
		t.Fatalf("second start moved=%d err=%v", again, err)
	}
}
