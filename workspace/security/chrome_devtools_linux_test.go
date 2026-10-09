//go:build linux

package security

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// managedChromeSandbox sets up what the deploy installs (the launcher beside a `chrome` symlink to the system Chrome) and a native-mode
// server environment, and returns the isolator, launcher, and a fresh profile folder inside the project grant.
func managedChromeSandbox(t *testing.T) (*Isolator, string, string) {
	chrome := "/opt/google/chrome/chrome"
	if _, err := os.Stat(chrome); err != nil {
		t.Skip("no system Chrome on this host")
	}
	if _, err := landlockRunnerPath(); err != nil {
		t.Skip("needs the Landlock launcher (AGENTWORKS_LANDLOCK_RUNNER)")
	}
	wrapperSource, err := os.ReadFile(filepath.Join("..", "..", "deploy", "rootless-linux", "chrome-agentworks"))
	if err != nil {
		t.Fatal(err)
	}
	// Same layout as <app>/tools/chrome/current: the wrapper next to a `chrome` symlink.
	toolDir := t.TempDir()
	if err := os.Symlink(chrome, filepath.Join(toolDir, "chrome")); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(toolDir, "chrome-agentworks")
	if err := os.WriteFile(wrapper, wrapperSource, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_BROWSER_EXECUTABLE_PATH", wrapper)
	// The service's own HOME (outside every grant, so not writable inside the sandbox) on a native-mode server (server B, server A): the
	// shape that killed Chrome.
	t.Setenv("NATIVE_WORKSPACE", "true")
	t.Setenv("HOME", t.TempDir())

	project := t.TempDir()
	profile := filepath.Join(project, "profile")
	if err := os.Mkdir(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	iso := &Isolator{ReadPaths: []string{project}, WritePaths: []string{project}, WorkDir: project, BaseDir: project, AllowNetwork: true}
	return iso, wrapper, profile
}

// A managed Chrome must get as far as DevTools inside the sandbox, started the way the app starts it (the headless arguments of
// browserconfig.HeadlessArgsForSession, through the chrome-agentworks wrapper the deploy installs beside the system Chrome).
// server B 2026-10-03: Chrome launched but "exited early (exit code: 1) without writing DevToolsActivePort".
// Skips without a system Chrome or the Landlock launcher (AGENTWORKS_LANDLOCK_RUNNER).
func TestManagedChromeReachesDevToolsInsideTheSandbox(t *testing.T) {
	iso, wrapper, profile := managedChromeSandbox(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := strings.Join([]string{
		wrapper, "--headless=new", "--no-sandbox", "--disable-gpu", "--disable-blink-features=AutomationControlled", "--lang=en-US",
		"--user-data-dir=" + profile, "--remote-debugging-port=0", "about:blank",
	}, " ")
	cmd, cleanup, err := iso.ExecuteIsolated(ctx, command, nil)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Fatalf("ExecuteIsolated: %v", err)
	}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer func() {
		// Chrome's helper processes carry the profile path; stop them all.
		_ = exec.Command("pkill", "-f", "--", "--user-data-dir="+profile).Run()
		_ = cmd.Process.Kill()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
		}
	}()
	port := filepath.Join(profile, "DevToolsActivePort")
	deadline := time.After(40 * time.Second)
	for {
		select {
		case err := <-exited:
			t.Fatalf("Chrome exited before DevTools was up: %v\n%s", err, output.String())
		case <-deadline:
			t.Fatalf("no DevToolsActivePort after 40s\n%s", output.String())
		case <-time.After(250 * time.Millisecond):
			if data, err := os.ReadFile(port); err == nil && len(data) > 0 {
				return
			}
		}
	}
}

// A screenshot needs the compositor: through the `chrome -> /opt/google/chrome/chrome` symlink Chrome looked for libvulkan next to the
// symlink, SwANGLE failed to initialise and the browser died (SIGTRAP, agent-browser "CDP response channel closed") on the first
// capture. The launcher must run the resolved binary.
func TestManagedChromeScreenshotsInsideTheSandbox(t *testing.T) {
	iso, wrapper, profile := managedChromeSandbox(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	shot := filepath.Join(profile, "shot.png")
	command := strings.Join([]string{wrapper, "--headless=new", "--no-sandbox", "--disable-gpu", "--user-data-dir=" + profile,
		"--window-size=800,600", "--screenshot=" + shot, "data:text/html,screenshot-test"}, " ")
	cmd, cleanup, err := iso.ExecuteIsolated(ctx, command, nil)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Fatalf("ExecuteIsolated: %v", err)
	}
	output, runErr := cmd.CombinedOutput()
	_ = exec.Command("pkill", "-f", "--", "--user-data-dir="+profile).Run()
	data, readErr := os.ReadFile(shot)
	if readErr != nil || len(data) < 8 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("no PNG screenshot (run: %v, read: %v)\n%s", runErr, readErr, lastBytes(output, 1500))
	}
}

func lastBytes(data []byte, n int) []byte {
	if len(data) > n {
		return data[len(data)-n:]
	}
	return data
}
