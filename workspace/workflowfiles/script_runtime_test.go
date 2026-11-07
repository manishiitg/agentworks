package workflowfiles

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-746-414: a script runtime must live beneath its Crew's private anchor,
// so strict workflow file opens work without accepting the retired directory.
func TestSavedScriptPrivateRuntimeMigrationPreservesDataAndStorageGuard(t *testing.T) {
	for _, attached := range []bool{false, true} {
		t.Run(fmt.Sprint(attached), func(t *testing.T) {
			base := t.TempDir()
			base, _ = filepath.EvalSymlinks(base)
			docs, state := filepath.Join(base, "docs"), filepath.Join(base, "state")
			project := filepath.Join(docs, "Crew/example")
			record := crewroot.Record{Product: "work", Folder: "example", ID: "crew", Owner: "owner"}
			if attached {
				project = filepath.Join(base, "project")
				record.LocalRoot = project
			}
			private := filepath.Join(docs, crewroot.PrivateDirectory, "crew")
			old := filepath.Join(docs, "Workflow/saved")
			code := filepath.Join(project, ".agentworks/code/saved")
			for _, dir := range []string{code, private, filepath.Join(state, "ownership/crew-executables"), filepath.Join(old, "runs/latest"), filepath.Join(old, "logs"), filepath.Join(old, "planning/changelog")} {
				if err := os.MkdirAll(dir, 0750); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, value string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(value), 0640); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("AGENTWORKS_STATE_ROOT", state)
			t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
			raw, _ := json.Marshal(map[string]any{"projects": map[string]crewroot.Record{"work/example": record}})
			write(filepath.Join(state, "ownership/projects.json"), string(raw))
			binding := crewroot.ExecutableBinding{ID: "saved", CrewID: "crew", CrewRoot: "Crew/example", Root: "Workflow/saved", Kind: "script"}
			raw, _ = json.Marshal(binding)
			write(filepath.Join(state, "ownership/crew-executables", fmt.Sprintf("%x.json", sha256.Sum256([]byte(binding.Root)))), string(raw))
			write(filepath.Join(code, "main.py"), "print('source unchanged')\n")
			write(filepath.Join(old, "runs/latest/result.json"), `{"real_result":7}`)
			write(filepath.Join(old, "planning/changelog/entry.json"), `{"change":"saved"}`)
			outside := filepath.Join(base, "outside")
			if err := os.Mkdir(outside, 0700); err != nil {
				t.Fatal(err)
			}
			write(filepath.Join(outside, "private.json"), `{"not_shared":true}`)
			if err := os.Symlink(outside, filepath.Join(old, "logs/external")); err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(base, "docs-alias")
			if err := os.Symlink(docs, alias); err != nil {
				t.Fatal(err)
			}
			view := WorkflowFS{Docs: alias, Root: binding.Root}
			if _, err := fs.ReadFile(view, "runs/latest/result.json"); err == nil {
				t.Fatal("retired runtime accepted before conversion")
			}
			count, backup, err := crewroot.MigrateCrewScripts(alias)
			if err != nil || count != 1 || backup == "" {
				t.Fatalf("migration: moves=%d backup=%q %v", count, backup, err)
			}
			for _, path := range []string{filepath.Join(backup, "000/runs/latest/result.json"), filepath.Join(private, "scripts/saved/runs/latest/result.json")} {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != `{"real_result":7}` {
					t.Fatalf("copy/readback: %s %v", path, err)
				}
			}
			if _, err := os.Lstat(old); !os.IsNotExist(err) {
				t.Fatalf("retired runtime remains: %v", err)
			}
			if data, err := fs.ReadFile(view, "runs/latest/result.json"); err != nil || string(data) != `{"real_result":7}` {
				t.Fatalf("guarded runtime read: %s %v", data, err)
			}
			if data, err := fs.ReadFile(view, "code/main.py"); err != nil || string(data) != "print('source unchanged')\n" {
				t.Fatalf("authored source changed: %s %v", data, err)
			}
			if data, err := fs.ReadFile(view, "planning/changelog/entry.json"); err != nil || string(data) != `{"change":"saved"}` {
				t.Fatalf("private script changelog read: %s %v", data, err)
			}
			if _, err := fs.ReadFile(view, "logs/external/private.json"); err == nil {
				t.Fatal("runtime symlink escaped the private storage guard")
			}
			if count, backup, err := crewroot.MigrateCrewScripts(alias); err != nil || count != 0 || backup != "" {
				t.Fatalf("repeat conversion: %d %q %v", count, backup, err)
			}
			// Relay bindings retain their existing unprojected storage.
			binding.Kind = "relay"
			raw, _ = json.Marshal(binding)
			write(filepath.Join(state, "ownership/crew-executables", fmt.Sprintf("%x.json", sha256.Sum256([]byte(binding.Root)))), string(raw))
			if _, found, err := crewroot.LookupExecutable(alias, binding.Root); err != nil || found {
				t.Fatalf("relay was adopted by the script layout: %v", err)
			}
		})
	}
}
