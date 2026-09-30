//go:build linux

package security

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLandlockLinkedProjectModes(t *testing.T) {
	if abi, err := landlockABI(); err != nil || abi < 1 {
		t.Skip("Landlock unavailable")
	}
	for _, builder := range []bool{false, true} {
		name := "run"
		if builder {
			name = "builder"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			project := filepath.Join(root, "project-data")
			runtime := filepath.Join(root, "runtime")
			other := filepath.Join(root, "other")
			for _, dir := range []string{project, runtime, other} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			for _, file := range []string{filepath.Join(project, "brief"), filepath.Join(other, "secret")} {
				if err := os.WriteFile(file, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(project, filepath.Join(runtime, "project")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, filepath.Join(project, "escape")); err != nil {
				t.Fatal(err)
			}
			script := `cat project/brief >/dev/null || exit 10
printf runtime > runtime-file || exit 11
if cat project/escape/secret >/dev/null 2>&1; then exit 12; fi
if (printf escaped > project/escape/secret) 2>/dev/null; then exit 13; fi
`
			writes := []string{runtime}
			if builder {
				writes = append(writes, project)
				script += `printf changed > project/brief || exit 20
printf created > project/new || exit 21
mv project/new project/renamed || exit 22
rm project/renamed || exit 23
`
			} else {
				script += `if (printf changed > project/brief) 2>/dev/null; then exit 30; fi
if touch project/new 2>/dev/null; then exit 31; fi
if rm project/brief 2>/dev/null; then exit 32; fi
if mv project/brief project/renamed 2>/dev/null; then exit 33; fi
`
			}
			script += `printf allowed`
			policy := LandlockPolicy{ReadPaths: []string{project}, WritePaths: writes, WorkDir: runtime}
			raw, err := json.Marshal(policy)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestDirectoryDiscoveryLauncherChild$")
			cmd.Env = append(os.Environ(), "CODEX_TEST_DIRECTORY_POLICY="+string(raw), "CODEX_TEST_DIRECTORY_SCRIPT="+script, "SANDBOX_EXTRA_SYSTEM_PATHS=", "AGENT_BROWSER_SHARED_PROFILE=")
			if output, err := cmd.CombinedOutput(); err != nil || string(output) != "allowed" {
				t.Fatalf("linked mode boundary: %v, %s", err, output)
			}
			want := "original"
			if builder {
				want = "changed"
			}
			if got, err := os.ReadFile(filepath.Join(project, "brief")); err != nil || string(got) != want {
				t.Fatalf("real project result = %q, %v", got, err)
			}
		})
	}
}
