package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the real launch path with a harmless executable, so gateway-only
// startup cannot skip product credential provisioning before spawning the host.
func TestRunServerPrivateProductCredentialsBeforeLaunch(t *testing.T) {
	script, err := os.ReadFile("run_server_with_logging.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	start := strings.Index(text, "prepare_gateway_product_credentials() {")
	end := strings.Index(text, "\nif [ \"$ONLY_GATEWAY\" = true ]; then")
	if start < 0 || end <= start {
		t.Fatal("gateway launch helpers missing")
	}
	for _, supplied := range []bool{false, true} {
		t.Run(map[bool]string{false: "generated", true: "operator supplied"}[supplied], func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, "composed-host")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\n[ -n \"${AGENTWORKS_PRODUCT_LLM_GATEWAY_SERVICE_TOKEN:-}\" ] || [ -s \"${AGENTWORKS_PRODUCT_LLM_GATEWAY_SERVICE_TOKEN_FILE:-}\" ]\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			body := text[start:end] + `
AGENTWORKS_GATEWAY_BINARY="$1"
GATEWAY_STATE_DIR="$2"
GATEWAY_DIR="$2"
GATEWAY_LOG_PATH="$2/launch.log"
BACKGROUND_MODE=false
export AGENTWORKS_PRODUCT_LLM_GATEWAY_SERVICE_TOKEN="$3"
export AGENTWORKS_PRODUCT_LLM_GATEWAY_SERVICE_TOKEN_FILE=""
launch_gateway_process || exit 1
wait "$GATEWAY_PID" || exit 1
printf '%s' "$AGENTWORKS_PRODUCT_LLM_GATEWAY_SERVICE_TOKEN_FILE"
`
			token := ""
			if supplied {
				token = strings.Repeat("s", 32)
			}
			output, err := exec.Command("bash", "-c", body, "launch-test", binary, dir, token).CombinedOutput()
			if err != nil {
				t.Fatalf("composed host did not receive credentials before launch: %v: %s", err, output)
			}
			file := filepath.Join(dir, "llm-gateway-service-token")
			if supplied {
				if len(output) != 0 {
					t.Fatal("operator credential was replaced")
				}
				if _, err := os.Stat(file); !os.IsNotExist(err) {
					t.Fatal("unnecessary credential generated")
				}
				return
			}
			data, err := os.ReadFile(file)
			if err != nil || len(strings.TrimSpace(string(data))) < 32 || string(output) != file {
				t.Fatal("dedicated credential was not exported")
			}
			info, err := os.Stat(file)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatal("product credential permissions are not private")
			}
		})
	}
}

// Exercise the launcher's actual profile helper without starting any service.
// The installed private product is visible in the full local profile only.
func TestRunServerPrivateProductSurfaceDefaults(t *testing.T) {
	script, err := os.ReadFile("run_server_with_logging.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	start := strings.Index(text, "configure_local_product_profile() {")
	end := strings.Index(text, "\nconfigure_local_product_profile\n")
	if start < 0 || end <= start {
		t.Fatal("local product profile helper missing")
	}
	for _, tc := range []struct {
		name, optIn, binary, configured string
		wantLLM                         bool
	}{
		{"minimal opt-out", "false", "/composed-host", "", false},
		{"public default", "true", "", "", false},
		{"private default", "true", "/composed-host", "", true},
		{"explicit exclusion", "true", "/composed-host", `["agentworks", "work"]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := text[start:end] + `
LOCAL_SERVER_PRODUCTS="$1"
AGENTWORKS_GATEWAY_BINARY="$2"
AGENTWORKS_ENABLED_PRODUCT_SURFACES="$3"
VAULT_AUDIT_PROVIDER=""
configure_local_product_profile
printf '%s\n' "$ENABLED_PRODUCT_SURFACES_JSON"
`
			output, err := exec.Command("bash", "-c", body, "profile-test", tc.optIn, tc.binary, tc.configured).CombinedOutput()
			if err != nil {
				t.Fatalf("profile failed: %v: %s", err, output)
			}
			var surfaces []string
			if err := json.Unmarshal(output, &surfaces); err != nil {
				t.Fatalf("invalid runtime surfaces: %v: %s", err, output)
			}
			hasLLM := false
			for _, surface := range surfaces {
				hasLLM = hasLLM || surface == "llm-gateway"
			}
			if hasLLM != tc.wantLLM {
				t.Fatalf("LLM visibility = %v, want %v: %s", hasLLM, tc.wantLLM, output)
			}
			if tc.optIn == "true" && tc.configured != "" && strings.TrimSpace(string(output)) != tc.configured {
				t.Fatalf("explicit surface configuration changed: %s", output)
			}
		})
	}
}

// Reapply the real local profile after simulating .env loading, including
// legacy audit configuration that previously prevented Vault from starting.
func TestRunServerLocalAuditDefaultsAfterEnvLoading(t *testing.T) {
	script, err := os.ReadFile("run_server_with_logging.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	start := strings.Index(text, "configure_local_product_profile() {")
	end := strings.Index(text, "\nconfigure_local_product_profile\n")
	if start < 0 || end <= start {
		t.Fatal("local product profile helper missing")
	}
	for _, tc := range []struct{ configured, want string }{
		{"", "sqlite"},
		{"clickhouse", "sqlite"},
		{"sqlite", "sqlite"},
		{"off", "off"},
		{"typo", "typo"},
	} {
		t.Run("provider="+tc.configured, func(t *testing.T) {
			body := text[start:end] + `
LOCAL_SERVER_PRODUCTS=true
AGENTWORKS_GATEWAY_BINARY=""
AGENTWORKS_ENABLED_PRODUCT_SURFACES=""
VAULT_AUDIT_PROVIDER=""
configure_local_product_profile
VAULT_AUDIT_PROVIDER="$1"
configure_local_product_profile
printf '%s' "$VAULT_AUDIT_PROVIDER"
`
			output, err := exec.Command("bash", "-c", body, "audit-test", tc.configured).Output()
			if err != nil {
				t.Fatalf("profile failed: %v", err)
			}
			if string(output) != tc.want {
				t.Fatalf("audit provider = %q, want %q", output, tc.want)
			}
		})
	}
}

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
