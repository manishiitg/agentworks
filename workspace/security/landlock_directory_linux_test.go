//go:build linux

package security

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Run the real launcher in a child: Landlock is irreversible for its thread.
func TestDirectoryDiscoveryPreservesFileIsolation(t *testing.T) {
	if abi, err := landlockABI(); err != nil || abi < 1 {
		t.Skip("Landlock unavailable")
	}
	t.Setenv("SANDBOX_EXTRA_SYSTEM_PATHS", "")
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", "")
	root := t.TempDir()
	work := filepath.Join(root, "allowed")
	other := filepath.Join(root, "other")
	for _, dir := range []string{work, other} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(other, "secret")
	if err := os.WriteFile(secret, []byte("secret"), 0o700); err != nil {
		t.Fatal(err)
	}
	script := `ls / >/dev/null || exit 10
ls "$1" >/dev/null || exit 11
if cat "$2" >/dev/null 2>&1; then exit 12; fi
if (printf changed > "$2") 2>/dev/null; then exit 13; fi
if "$2" >/dev/null 2>&1; then exit 14; fi
printf allowed > local-file
cat local-file`
	policy := LandlockPolicy{ListPaths: []string{"/"}, WritePaths: []string{work}, WorkDir: work}
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDirectoryDiscoveryLauncherChild$")
	cmd.Env = append(os.Environ(), "CODEX_TEST_DIRECTORY_POLICY="+string(raw), "CODEX_TEST_DIRECTORY_SCRIPT="+script, "CODEX_TEST_DIRECTORY_OTHER="+other, "CODEX_TEST_DIRECTORY_SECRET="+secret)
	if output, err := cmd.CombinedOutput(); err != nil || string(output) != "allowed" {
		t.Fatalf("directory grant broke the file boundary: %v, %s", err, output)
	}
	if got, err := os.ReadFile(secret); err != nil || string(got) != "secret" {
		t.Fatalf("outside file changed: %v, %q", err, got)
	}
}

func TestDirectoryDiscoveryLauncherChild(t *testing.T) {
	raw := os.Getenv("CODEX_TEST_DIRECTORY_POLICY")
	if raw == "" {
		return
	}
	var policy LandlockPolicy
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		t.Fatal(err)
	}
	if err := RunLandlockLauncher(policy, []string{"/bin/sh", "-c", os.Getenv("CODEX_TEST_DIRECTORY_SCRIPT"), "probe", os.Getenv("CODEX_TEST_DIRECTORY_OTHER"), os.Getenv("CODEX_TEST_DIRECTORY_SECRET")}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
