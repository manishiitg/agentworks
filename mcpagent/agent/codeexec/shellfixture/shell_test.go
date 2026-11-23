package shellfixture

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecuteShellCommandUsesSafeEnvironmentByDefault(t *testing.T) {
	t.Setenv("RUNLOOP_TEST_SECRET_TOKEN", "should-not-leak")

	got, err := ExecuteShellCommand(context.Background(), map[string]interface{}{
		"command": "printf '%s' \"${RUNLOOP_TEST_SECRET_TOKEN:-missing}\"",
	}, nil)
	if err != nil {
		t.Fatalf("ExecuteShellCommand() error = %v", err)
	}
	if !strings.Contains(got, "stdout:\nmissing") {
		t.Fatalf("ExecuteShellCommand() output leaked parent env or missed stdout; got:\n%s", got)
	}
}

func TestExecuteShellCommandUsesWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("from-workdir"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := ExecuteShellCommand(context.Background(), map[string]interface{}{
		"command":           "cat marker.txt",
		"working_directory": dir,
	}, BuildSafeEnvironment())
	if err != nil {
		t.Fatalf("ExecuteShellCommand() error = %v", err)
	}
	if !strings.Contains(got, "stdout:\nfrom-workdir") {
		t.Fatalf("ExecuteShellCommand() did not run inside working_directory; got:\n%s", got)
	}
}

func TestExecuteShellCommandRejectsInvalidWorkingDirectory(t *testing.T) {
	_, err := ExecuteShellCommand(context.Background(), map[string]interface{}{
		"command":           "pwd",
		"working_directory": filepath.Join(t.TempDir(), "missing"),
	}, BuildSafeEnvironment())
	if err == nil {
		t.Fatal("ExecuteShellCommand() error = nil, want invalid working_directory error")
	}
}
