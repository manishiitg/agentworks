//go:build linux

package handlers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/security"
)

// The actual restricted launch must use the deployment CLI even when a stale
// system copy exists. Grant its installed package, never the service's home.
func TestSandboxUsesManagedBrowserCLI(t *testing.T) {
	buildLandlockRunner(t)
	t.Setenv("NATIVE_WORKSPACE", "")
	serviceHome, err := os.MkdirTemp("/var/tmp", "browser-cli-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(serviceHome) })
	bin := filepath.Join(serviceHome, ".local", "bin")
	packageBin := filepath.Join(serviceHome, ".local", "lib", "node_modules", "agent-browser", "bin")
	for _, dir := range []string{bin, packageBin} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	executable := filepath.Join(packageBin, "agent-browser-linux-x64")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf 'agent-browser 0.38.2\\n'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(executable, filepath.Join(bin, "agent-browser")); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(serviceHome, ".env")
	os.WriteFile(secret, []byte("SERVICE_SECRET"), 0600)
	t.Setenv("AGENT_BROWSER_CLI_DIR", bin)
	t.Setenv("WORKSPACE_API_TOKEN", "must-not-leak")
	work := t.TempDir()
	iso := security.Isolator{BaseDir: work, WorkDir: work, ReadPaths: []string{work}, WritePaths: []string{work}}
	cmd, cleanup, err := iso.ExecuteIsolated(context.Background(), "agent-browser --version; if cat '"+secret+"' 2>/dev/null; then echo HOME_LEAK; fi; if [ -n \"$WORKSPACE_API_TOKEN\" ]; then echo ENV_LEAK; fi", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sandbox: %v %s", err, out)
	}
	if !strings.Contains(string(out), "agent-browser 0.38.2") {
		t.Fatalf("managed CLI was not selected: %s", out)
	}
	for _, forbidden := range []string{"SERVICE_SECRET", "HOME_LEAK", "ENV_LEAK"} {
		if strings.Contains(string(out), forbidden) {
			t.Fatalf("sandbox exposed %s", forbidden)
		}
	}
}
