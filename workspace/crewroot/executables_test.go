package crewroot

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// PLAT-746-265: automation source cannot collide with app code or capture
// runtime data, and a project-planted symlink cannot redirect file operations.
func TestCrewAutomationSeparatesSourceAndRuntime(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(fmt.Sprint(local), func(t *testing.T) {
			base := t.TempDir()
			base, _ = filepath.EvalSymlinks(base)
			docs, state := filepath.Join(base, "docs"), filepath.Join(base, "state")
			project := filepath.Join(docs, "Crew", "example")
			rec := Record{Product: "work", Folder: "example", ID: "crew", Owner: "owner"}
			if local {
				project = filepath.Join(base, "app")
				rec.LocalRoot = project
			}
			for _, p := range []string{filepath.Join(project, ".agentworks"), filepath.Join(docs, PrivateDirectory, "crew"), filepath.Join(state, "ownership", "crew-executables")} {
				if err := os.MkdirAll(p, 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("AGENTWORKS_STATE_ROOT", state)
			t.Setenv("LOCAL_MODE", "true")
			data, _ := json.Marshal(map[string]any{"projects": map[string]Record{"work/example": rec}})
			os.WriteFile(filepath.Join(state, "ownership", "projects.json"), data, 0600)
			binding := ExecutableBinding{ID: "sequence", Root: "Workflow/sequence", CrewID: "crew", CrewRoot: "Crew/example", Kind: "workflow"}
			data, _ = json.Marshal(binding)
			os.WriteFile(filepath.Join(state, "ownership", "crew-executables", fmt.Sprintf("%x.json", sha256.Sum256([]byte(binding.Root)))), data, 0600)
			resolve := func(virtual string) (string, error) {
				p, bound, err := Resolve(docs, virtual)
				if err != nil || bound {
					return p, err
				}
				return filepath.Join(docs, virtual), nil
			}
			for rel, want := range map[string]string{
				"planning/plan.json":            filepath.Join(project, ".agentworks/workflows/sequence/planning/plan.json"),
				"workflow.json":                 filepath.Join(project, ".agentworks/workflows/sequence/workflow.json"),
				"variables/variables.json":      filepath.Join(project, ".agentworks/workflows/sequence/variables/variables.json"),
				"code/step/main.py":             filepath.Join(project, ".agentworks/code/sequence/step/main.py"),
				"runs/iteration-10/output.json": filepath.Join(docs, binding.Root, "runs/iteration-10/output.json"),
				"skills/custom/SKILL.md":        filepath.Join(docs, binding.Root, "skills/custom/SKILL.md"),
				"knowledgebase/notes/note.md":   filepath.Join(docs, binding.Root, "knowledgebase/notes/note.md"),
			} {
				got, err := resolve(binding.Root + "/" + rel)
				if err != nil || got != want {
					t.Fatalf("%s got %q %v want %q", rel, got, err, want)
				}
			}

			// The one-off converter preserves authored bytes, run history and app files.
			write := func(p, body string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, rel := range []string{"workflow.json", "planning/plan.json", "variables/variables.json", "code/step/main.py"} {
				write(filepath.Join(docs, binding.Root, rel), rel)
			}
			history := filepath.Join(docs, binding.Root, "runs/iteration-10/output.json")
			write(history, "history")
			appCode := filepath.Join(project, "code/main.py")
			write(appCode, "app source")
			skill := filepath.Join(docs, binding.Root, "skills/custom/SKILL.md")
			write(skill, "skill")
			knowledge := filepath.Join(docs, binding.Root, "knowledgebase/notes/note.md")
			write(knowledge, "knowledge")
			count, backup, err := MigrateExecutableSources(docs)
			if err != nil || count != 4 || backup == "" {
				t.Fatalf("migration count=%d backup=%s err=%v", count, backup, err)
			}
			for p, want := range map[string]string{history: "history", appCode: "app source", skill: "skill", knowledge: "knowledge", filepath.Join(project, ".agentworks/code/sequence/step/main.py"): "code/step/main.py"} {
				raw, err := os.ReadFile(p)
				if err != nil || string(raw) != want {
					t.Fatalf("preserved %s: %q %v", p, raw, err)
				}
			}
			if _, err := os.Stat(filepath.Join(docs, binding.Root, "workflow.json")); !os.IsNotExist(err) {
				t.Fatal("old source still present")
			}
			if n, _, err := MigrateExecutableSources(docs); err != nil || n != 0 {
				t.Fatal("repeat conversion", n, err)
			}
			if err := os.RemoveAll(filepath.Join(project, ".agentworks/code")); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(base, "outside")
			os.Mkdir(outside, 0700)
			os.Symlink(outside, filepath.Join(project, ".agentworks/code"))
			if _, err := resolve(binding.Root + "/code/main.py"); err == nil {
				t.Fatal("source symlink escaped project")
			}
			if _, err := resolve(binding.Root + "/planning/../../../../../outside"); err == nil {
				t.Fatal("traversal escaped source")
			}
		})
	}
}
