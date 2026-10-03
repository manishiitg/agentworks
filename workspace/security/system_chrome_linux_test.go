//go:build linux

package security

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// The system Chrome (/usr/bin/google-chrome -> /opt/google/chrome/google-chrome) must be runnable inside the sandbox: without the
// /opt/google/chrome grant every browser start failed with "Failed to launch Chrome at /usr/bin/google-chrome: Permission denied".
func TestSystemChromeRunsInsideTheSandbox(t *testing.T) {
	if _, err := os.Stat("/opt/google/chrome/chrome"); err != nil {
		t.Skip("no system Chrome on this host")
	}
	found := false
	for _, path := range landlockSystemReadPaths() {
		if strings.HasSuffix(path, "/opt/google/chrome") {
			found = true
		}
	}
	if !found {
		t.Fatal("/opt/google/chrome is not in the sandbox's system read paths")
	}
	if _, err := landlockRunnerPath(); err != nil {
		t.Skip("needs the Landlock launcher (AGENTWORKS_LANDLOCK_RUNNER)")
	}
	project := t.TempDir()
	iso := &Isolator{ReadPaths: []string{project}, WritePaths: []string{project}, WorkDir: project, BaseDir: project}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd, cleanup, err := iso.ExecuteIsolated(ctx, "/usr/bin/google-chrome --version", nil)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Fatalf("ExecuteIsolated: %v", err)
	}
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Chrome") {
		t.Fatalf("Chrome did not run in the sandbox: %v %s", err, out)
	}
}
