//go:build linux

package security

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Deployment preflight must test the same sanitized, guarded execution path
// as agent_browser, rather than just the service's interactive PATH.
func TestConfiguredBrowserCLIInSandbox(t *testing.T) {
	if os.Getenv("AGENTWORKS_BROWSER_CLI_CHECK") != "1" {
		t.Skip("deployment-only managed CLI qualification")
	}
	dir := configuredBrowserCLIDir()
	if dir == "" {
		t.Fatal("AGENT_BROWSER_CLI_DIR must name the managed install")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	expected, err := exec.CommandContext(ctx, filepath.Join(dir, "agent-browser"), "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("managed CLI: %v %s", err, expected)
	}
	var major, minor, patch int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(expected)), "agent-browser %d.%d.%d", &major, &minor, &patch); err != nil || (major == 0 && (minor < 38 || minor == 38 && patch < 2)) {
		t.Fatalf("agent-browser >=0.38.2 required, got %q", expected)
	}
	t.Setenv("NATIVE_WORKSPACE", "")
	work := t.TempDir()
	iso := Isolator{BaseDir: work, WorkDir: work, ReadPaths: []string{work}, WritePaths: []string{work}}
	cmd, cleanup, err := iso.ExecuteIsolated(ctx, "agent-browser --version", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	actual, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(actual)) != strings.TrimSpace(string(expected)) {
		t.Fatalf("sandbox CLI mismatch: want %q, got %q (err=%v)", expected, actual, err)
	}
	t.Logf("guarded execution uses %s", strings.TrimSpace(string(actual)))
}
