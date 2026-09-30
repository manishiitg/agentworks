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

// A step reads the execution scope, but writes only its own output leaf. The
// private cwd grant must not turn a directory link into sibling write access.
func TestLandlockLinkedStepOutput(t *testing.T) {
	if abi, err := landlockABI(); err != nil || abi < 1 {
		t.Skip("Landlock unavailable")
	}
	root := t.TempDir()
	runtime := filepath.Join(root, "runtime")
	execution := filepath.Join(root, "iteration-0", "group-a", "execution")
	output := filepath.Join(execution, "step-1")
	sibling := filepath.Join(execution, "step-2")
	outside := filepath.Join(root, "iteration-1")
	for _, dir := range []string{runtime, output, sibling, outside} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{sibling, outside} {
		if err := os.WriteFile(filepath.Join(dir, "result"), []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{"output": output, "inputs": execution} {
		if err := os.Symlink(target, filepath.Join(runtime, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(output, "escape")); err != nil {
		t.Fatal(err)
	}
	script := `cat inputs/step-2/result >/dev/null || exit 10
printf scratch > scratch || exit 11
printf first > output/result || exit 12
printf final > output/staged || exit 13
mv output/staged output/result || exit 14
mkdir output/nested || exit 15
printf child > output/nested/child || exit 16
rm output/nested/child || exit 17
if (printf stolen > inputs/step-2/result) 2>/dev/null; then exit 20; fi
if rm inputs/step-2/result 2>/dev/null; then exit 21; fi
if cat output/escape/result >/dev/null 2>&1; then exit 22; fi
if (printf stolen > output/escape/result) 2>/dev/null; then exit 23; fi
printf allowed`
	raw, err := json.Marshal(LandlockPolicy{ReadPaths: []string{execution}, WritePaths: []string{runtime, output}, WorkDir: runtime})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDirectoryDiscoveryLauncherChild$")
	cmd.Env = append(os.Environ(), "CODEX_TEST_DIRECTORY_POLICY="+string(raw), "CODEX_TEST_DIRECTORY_SCRIPT="+script, "SANDBOX_EXTRA_SYSTEM_PATHS=", "AGENT_BROWSER_SHARED_PROFILE=")
	if got, err := cmd.CombinedOutput(); err != nil || string(got) != "allowed" {
		t.Fatalf("linked step boundary: %v, %s", err, got)
	}
	if got, err := os.ReadFile(filepath.Join(output, "result")); err != nil || string(got) != "final" {
		t.Fatalf("missing real output: %q %v", got, err)
	}
	for _, dir := range []string{sibling, outside} {
		if got, err := os.ReadFile(filepath.Join(dir, "result")); err != nil || string(got) != "original" {
			t.Fatalf("changed unrelated output: %q %v", got, err)
		}
	}
}
