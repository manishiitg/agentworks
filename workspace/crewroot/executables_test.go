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
			t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
			data, _ := json.Marshal(map[string]any{"projects": map[string]Record{"work/example": rec}})
			os.WriteFile(filepath.Join(state, "ownership", "projects.json"), data, 0600)
			binding := ExecutableBinding{ID: "sequence", Root: "Workflow/sequence", CrewID: "crew", CrewRoot: "Crew/example", Kind: "workflow", RuntimeRoot: "workflows/sequence"}
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
				"planning/changelog/entry.json": filepath.Join(docs, PrivateDirectory, "crew/workflows/sequence/planning/changelog/entry.json"),
				"db/db.sqlite":                  filepath.Join(docs, PrivateDirectory, "crew/workflows/sequence/db/db.sqlite"),
				"code/step/main.py":             filepath.Join(project, ".agentworks/code/sequence/step/main.py"),
				"runs/iteration-10/output.json": filepath.Join(docs, PrivateDirectory, "crew/workflows/sequence", "runs/iteration-10/output.json"),
				"skills/custom/SKILL.md":        filepath.Join(docs, PrivateDirectory, "crew/workflows/sequence", "skills/custom/SKILL.md"),
				"knowledgebase/notes/note.md":   filepath.Join(docs, PrivateDirectory, "crew/workflows/sequence", "knowledgebase/notes/note.md"),
			} {
				got, err := resolve(binding.Root + "/" + rel)
				if err != nil || got != want {
					t.Fatalf("%s got %q %v want %q", rel, got, err, want)
				}
			}

			// PLAT-746-428: changing engine keeps all existing runtime identities;
			// Crew chat and automation database calls adopt the same whole DB.
			binding.Kind = "script"
			data, _ = json.Marshal(binding)
			os.WriteFile(filepath.Join(state, "ownership/crew-executables", fmt.Sprintf("%x.json", sha256.Sum256([]byte(binding.Root)))), data, 0600)
			for _, virtual := range []string{"Workflow/sequence/db/db.sqlite", "Crew/example/db/db.sqlite"} {
				if got, err := resolve(virtual); err != nil || got != filepath.Join(docs, PrivateDirectory, "crew/db/db.sqlite") {
					t.Fatalf("new script did not use the Crew DB: %q %v", got, err)
				}
			}
			rec.DatabaseRoot = "workflows/sequence/db"
			saveRouting := func() {
				t.Helper()
				data, _ := json.Marshal(map[string]any{"projects": map[string]Record{"work/example": rec}})
				if err := os.WriteFile(filepath.Join(state, "ownership/projects.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
				data, _ = json.Marshal(binding)
				if err := os.WriteFile(filepath.Join(state, "ownership/crew-executables", fmt.Sprintf("%x.json", sha256.Sum256([]byte(binding.Root)))), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			saveRouting()
			for _, virtual := range []string{"Workflow/sequence/db/db.sqlite", "Crew/example/db/db.sqlite", "Crew/example/db/migrations/001.sql"} {
				want := filepath.Join(docs, PrivateDirectory, "crew/workflows/sequence/db", filepath.Base(virtual))
				if filepath.Base(virtual) == "001.sql" {
					want = filepath.Join(filepath.Dir(want), "migrations/001.sql")
				}
				if got, err := resolve(virtual); err != nil || got != want {
					t.Fatalf("adopted database: %s %q %v want %q", virtual, got, err, want)
				}
			}
			if got, err := resolve("Workflow/sequence/runs/iteration-10/output.json"); err != nil || got != filepath.Join(docs, PrivateDirectory, "crew/workflows/sequence/runs/iteration-10/output.json") {
				t.Fatalf("runtime moved: %q %v", got, err)
			}
			for _, corrupt := range []string{"runtime", "owner", "database", "missing"} {
				switch corrupt {
				case "runtime":
					binding.RuntimeRoot = "../other"
				case "owner":
					binding.CrewID = "another-crew"
				case "database":
					rec.DatabaseRoot = "../another-crew/db"
				case "missing":
					binding.RuntimeRoot = ""
				}
				saveRouting()
				if _, err := resolve("Workflow/sequence/runs/iteration-10/output.json"); err == nil {
					t.Fatalf("%s routing corruption was admitted", corrupt)
				}
				binding.RuntimeRoot, binding.CrewID, rec.DatabaseRoot = "workflows/sequence", "crew", "workflows/sequence/db"
			}
			saveRouting()

			if _, err := os.Stat(filepath.Join(docs, binding.Root)); !os.IsNotExist(err) {
				t.Fatal("Resolving moved files recreated the retired directory")
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
			runtime := filepath.Join(docs, PrivateDirectory, "crew/workflows")
			os.MkdirAll(runtime, 0700)
			os.Symlink(outside, filepath.Join(runtime, "sequence"))
			if _, err := resolve(binding.Root + "/db/db.sqlite"); err == nil {
				t.Fatal("runtime symlink escaped private storage")
			}
			if _, err := resolve(binding.Root + "/planning/../../../../../outside"); err == nil {
				t.Fatal("traversal escaped source")
			}
		})
	}
}
