package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A failed install used to start Vite even when a newly declared UI package was
// absent. Run the actual launcher helpers with a failing npm, without starting
// any server or installing dependencies.
func TestRunServerDependencyFallbackRequiresDeclaredPackages(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required for the frontend dependency check")
	}
	script, err := os.ReadFile("run_server_with_logging.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	start := strings.Index(text, "frontend_deps_present() {")
	end := strings.Index(text, "\nport_in_use() {")
	if start < 0 || end <= start {
		t.Fatal("frontend dependency helpers are missing")
	}
	for _, installed := range []bool{false, true} {
		name := "missing package with matching stamp"
		if installed {
			name = "complete install with stale stamp"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "node_modules", ".bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			write := func(path, body string, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(path, []byte(body), mode); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(dir, "package.json"), `{"dependencies":{"required-ui":"1.0.0"}}`, 0o644)
			write(filepath.Join(dir, "package-lock.json"), `{}`, 0o644)
			write(filepath.Join(bin, "vite"), "#!/bin/sh\nexit 0\n", 0o755)
			write(filepath.Join(bin, "npm"), "#!/bin/sh\nprintf attempted >> \"$NPM_CALL_LOG\"\nexit 1\n", 0o755)
			if installed {
				pkg := filepath.Join(dir, "node_modules", "required-ui")
				if err := os.MkdirAll(pkg, 0o755); err != nil {
					t.Fatal(err)
				}
				write(filepath.Join(pkg, "package.json"), `{}`, 0o644)
			}
			callLog := filepath.Join(dir, "npm-calls")
			fixture := filepath.Join(dir, "check.sh")
			stamp := `cat "$1/package.json" "$1/package-lock.json" | shasum | awk '{print $1}' > "$1/node_modules/.agentworks-deps-stamp"` + "\n"
			if installed {
				stamp = `printf stale > "$1/node_modules/.agentworks-deps-stamp"` + "\n"
			}
			write(fixture, text[start:end]+"\n"+stamp+`ensure_frontend_deps "$1" "$2"`+"\n", 0o644)
			cmd := exec.Command("bash", fixture, dir, filepath.Join(dir, "install.log"))
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "NPM_CALL_LOG="+callLog)
			output, err := cmd.CombinedOutput()
			if (err == nil) != installed {
				t.Fatalf("installed=%v: launcher result %v; output %s", installed, err, output)
			}
			calls, err := os.ReadFile(callLog)
			if err != nil || string(calls) != "attempted" {
				t.Fatalf("launcher must attempt dependency refresh: calls=%q, error=%v", calls, err)
			}
		})
	}
}

// Ctrl+C normally gives the Go server time to close its registered provider
// sessions. The launcher's tmux sweep is the backstop for a compiled `go run`
// child that exits before graceful cleanup, so its provider list must stay in
// sync with the adapters. This pins the exact omission that left Muse panes
// alive after stopping a local stack.
func TestRunServerCtrlCCleanupCoversEveryCodingCLI(t *testing.T) {
	script, err := os.ReadFile("run_server_with_logging.sh")
	if err != nil {
		t.Fatalf("read launcher: %v", err)
	}
	text := string(script)
	for _, pattern := range []string{
		"mlp-claude-code-*",
		"mlp-codex-cli-*",
		"mlp-cursor-cli-*",
		"mlp-pi-cli-*",
		"mlp-muse-*",
	} {
		if !strings.Contains(text, pattern) {
			t.Errorf("Ctrl+C cleanup is missing provider tmux pattern %q", pattern)
		}
	}
}

func TestRunServerRuntimeDiagnosticsAreControlledOnlyByFlag(t *testing.T) {
	script, err := os.ReadFile("run_server_with_logging.sh")
	if err != nil {
		t.Fatalf("read launcher: %v", err)
	}
	text := string(script)

	// The gate must be applied after .env loading and explicitly pin both
	// variables to 0 in the ordinary (no-flag) path. Otherwise a legacy .env
	// entry silently enables the diagnostic terminal rail: a plain unset is
	// not enough because the server's godotenv.Load re-sets unset variables
	// from .env at boot, while an explicit 0 is preserved (and reads off).
	envLoad := strings.Index(text, "source_exported_env_file ../agent_go/.env")
	gate := strings.Index(text, "# Runtime diagnostics are controlled only by the explicit command-line switch.")
	if envLoad < 0 || gate < 0 || gate <= envLoad {
		t.Fatal("runtime diagnostics gate must be applied after loading .env")
	}
	gateText := text[gate:]
	for _, expected := range []string{
		"if [ \"$ENABLE_CHAT_TERMINAL_DEBUGS\" = true ]; then",
		"export AGENTWORKS_RUNTIME_DEBUG=1",
		"export VITE_RUNTIME_DEBUG=1",
		"export AGENTWORKS_RUNTIME_DEBUG=0",
		"export VITE_RUNTIME_DEBUG=0",
	} {
		if !strings.Contains(gateText, expected) {
			t.Errorf("runtime diagnostics gate is missing %q", expected)
		}
	}
}
