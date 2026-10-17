//go:build linux

package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	"github.com/spf13/viper"
)

// TestExecuteShellCommandUsesRealLandlockLauncher reaches the real HTTP handler
// and child launcher. It deliberately does not mock a successful Isolator:
// PLAT-118 is a deployment-boundary failure, not a policy-construction unit.
func TestExecuteShellCommandUsesRealLandlockLauncher(t *testing.T) {
	buildLandlockRunner(t)

	root, err := os.MkdirTemp(".", "landlock-handler-test-*")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	readDir := filepath.Join(root, "read")
	writeDir := filepath.Join(root, "write")
	blockedDir := filepath.Join(root, "blocked")
	for _, dir := range []string{readDir, writeDir, blockedDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(readDir, "input.txt"), []byte("read-ok\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blockedDir, "secret.txt"), []byte("must-not-read\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(blockedDir, "secret.txt"), filepath.Join(readDir, "escape")); err != nil {
		t.Fatal(err)
	}

	previousDocsDir := viper.GetString("docs-dir")
	viper.Set("docs-dir", root)
	t.Cleanup(func() { viper.Set("docs-dir", previousDocsDir) })

	command := fmt.Sprintf(
		"cat %q && printf write-ok > %q && if printf denied > %q 2>/dev/null; then exit 41; fi && if cat %q >/dev/null 2>&1; then exit 42; fi && if cat %q >/dev/null 2>&1; then exit 43; fi",
		filepath.Join(readDir, "input.txt"),
		filepath.Join(writeDir, "output.txt"),
		filepath.Join(readDir, "must-not-write.txt"),
		filepath.Join(blockedDir, "secret.txt"),
		filepath.Join(readDir, "escape"),
	)
	request := models.ExecuteShellRequest{
		Command: command,
		// A published script runs from immutable source while saving results
		// elsewhere. Selecting that cwd must not make it writable.
		WorkingDirectory: "read",
		FolderGuard: &models.FolderGuardConfig{
			Enabled:      true,
			ReadPaths:    []string{"read", "write"},
			WritePaths:   []string{"write"},
			BlockedPaths: []string{"blocked"},
		},
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.ReleaseMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/execute", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ExecuteShellCommand(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response models.APIResponse[models.ExecuteShellResponse]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", response.Data.ExitCode, response.Data.Stderr, response.Data.Stdout)
	}
	if response.Data.Stdout != "read-ok\n" {
		t.Fatalf("stdout=%q", response.Data.Stdout)
	}
	if got, err := os.ReadFile(filepath.Join(writeDir, "output.txt")); err != nil || string(got) != "write-ok" {
		t.Fatalf("allowed write=%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(readDir, "must-not-write.txt")); !os.IsNotExist(err) {
		t.Fatalf("read-only write unexpectedly exists: %v", err)
	}
}

func buildLandlockRunner(t *testing.T) string {
	t.Helper()
	if runner := os.Getenv("AGENTWORKS_LANDLOCK_RUNNER"); runner != "" {
		if info, err := os.Stat(runner); err != nil || info.IsDir() || info.Mode()&0111 == 0 {
			t.Fatalf("configured Landlock runner is not executable: %q (%v)", runner, err)
		}
		if capability := security.CurrentSandboxCapability(); !capability.Available || capability.Backend != "landlock" {
			t.Skipf("Landlock is unavailable on this Linux test host: %+v", capability)
		}
		return runner
	}
	workspaceRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(t.TempDir(), "video-studio-landlock-runner")
	cmd := exec.Command("go", "build", "-o", runner, "./cmd/landlock-runner")
	cmd.Dir = workspaceRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build Landlock runner: %v\n%s", err, output)
	}
	t.Setenv("AGENTWORKS_LANDLOCK_RUNNER", runner)
	if capability := security.CurrentSandboxCapability(); !capability.Available || capability.Backend != "landlock" {
		t.Skipf("Landlock is unavailable on this Linux test host: %+v", capability)
	}
	return runner
}
