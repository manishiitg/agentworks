package shelllaunch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandUsesLoginShellByDefault(t *testing.T) {
	shell := writeExecutableShell(t, "zsh")
	t.Setenv(EnvShellPath, shell)
	t.Setenv("SHELL", "")
	t.Setenv(EnvShellMode, "")

	got := Command([]string{"claude", "--flag", "value"}, "/tmp/user chat")

	if !strings.HasPrefix(got, Quote(shell)+" '-ilc' ") {
		t.Fatalf("command = %q, want login shell -ilc prefix", got)
	}
	for _, want := range []string{
		Quote(`cd "$1" || exit; shift; exec "$@"`),
		"'coding-agent'",
		"'/tmp/user chat'",
		"'claude'",
		"'--flag'",
		"'value'",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("command = %q, missing %s", got, want)
		}
	}
}

func TestCommandUsesFishSyntaxForFishLoginShell(t *testing.T) {
	shell := writeExecutableShell(t, "fish")
	t.Setenv(EnvShellPath, shell)
	t.Setenv("SHELL", "")
	t.Setenv(EnvShellMode, "")

	got := Command([]string{"gemini"}, "/tmp/work")

	if !strings.HasPrefix(got, Quote(shell)+" '-lic' ") {
		t.Fatalf("command = %q, want fish -lic prefix", got)
	}
	if !strings.Contains(got, Quote("cd $argv[1]; or exit; exec $argv[2..-1]")) {
		t.Fatalf("command = %q, missing fish script", got)
	}
}

func TestCommandDirectModeBypassesLoginShell(t *testing.T) {
	shell := writeExecutableShell(t, "zsh")
	t.Setenv(EnvShellPath, shell)
	t.Setenv(EnvShellMode, "direct")

	got := Command([]string{"codex", "--no-alt-screen"}, "/tmp/user chat")

	if !strings.HasPrefix(got, "cd '/tmp/user chat' && exec ") {
		t.Fatalf("command = %q, want direct cd/exec form", got)
	}
	if strings.Contains(got, "-ilc") || strings.Contains(got, Quote(shell)) {
		t.Fatalf("command = %q, direct mode should not invoke login shell", got)
	}
}

func TestCommandWithEnvKeepsSecretsOutOfCommandString(t *testing.T) {
	t.Setenv(EnvShellMode, "direct")

	got, cleanup, err := CommandWithEnv(
		[]string{"gemini", "--model", "gemini-3.1"},
		"/tmp/user chat",
		[]string{"GEMINI_API_KEY=secret-value", "GOOGLE_API_KEY=google-secret"},
	)
	if err != nil {
		t.Fatalf("CommandWithEnv error = %v", err)
	}
	defer cleanup()

	if strings.Contains(got, "secret-value") || strings.Contains(got, "google-secret") {
		t.Fatalf("command string leaked secret: %q", got)
	}
	if strings.Contains(got, "GEMINI_API_KEY=") || strings.Contains(got, "GOOGLE_API_KEY=") {
		t.Fatalf("command string leaked env names/values: %q", got)
	}
	if !strings.Contains(got, Quote("/bin/sh")) {
		t.Fatalf("command = %q, want /bin/sh wrapper", got)
	}

	parts := strings.Split(got, "'")
	var scriptPath string
	for _, part := range parts {
		if strings.Contains(part, "mlp-coding-agent-launch-") {
			scriptPath = part
			break
		}
	}
	if scriptPath == "" {
		t.Fatalf("command = %q, missing launch script path", got)
	}
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("stat launch script: %v", err)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o600 {
		t.Fatalf("launch script mode = %#o, want 0600", gotMode)
	}
	body, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read launch script: %v", err)
	}
	if !strings.Contains(string(body), "secret-value") || !strings.Contains(string(body), "rm -f \"$0\"") {
		t.Fatalf("launch script missing expected secret export/self-delete: %s", string(body))
	}
}

func TestCommandWithEnvRejectsInvalidKey(t *testing.T) {
	_, cleanup, err := CommandWithEnv([]string{"cmd"}, "/tmp/work", []string{"BAD-KEY=value"})
	if cleanup != nil {
		cleanup()
	}
	if err == nil {
		t.Fatal("CommandWithEnv error = nil, want invalid key error")
	}
}

func TestCommandWithFinalEnvAppliesSanitizationAfterLoginShell(t *testing.T) {
	shell := writeExecutableShell(t, "zsh")
	t.Setenv(EnvShellPath, shell)
	t.Setenv(EnvShellMode, "")

	got, cleanup, err := CommandWithFinalEnv(
		[]string{"claude", "--model", "sonnet"},
		"/tmp/work",
		[]string{"CLAUDE_CODE_OAUTH_TOKEN=workflow-secret"},
		[]string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"},
	)
	if err != nil {
		t.Fatalf("CommandWithFinalEnv error = %v", err)
	}
	defer cleanup()
	if strings.Contains(got, "workflow-secret") || strings.Contains(got, "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Fatalf("command string leaked final environment: %q", got)
	}

	parts := strings.Split(got, "'")
	var scriptPath string
	for _, part := range parts {
		if strings.Contains(part, "mlp-coding-agent-launch-") {
			scriptPath = part
			break
		}
	}
	body, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read launch script: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "workflow-secret") {
		t.Fatalf("launch script missing final credential: %s", text)
	}
	innerUnset := "unset ANTHROPIC_API_KEY; unset ANTHROPIC_AUTH_TOKEN; unset CLAUDE_CODE_OAUTH_TOKEN;"
	if !strings.Contains(text, innerUnset) {
		t.Fatalf("launch script does not sanitize after shell initialization: %s", text)
	}
	if !strings.Contains(text, `export CLAUDE_CODE_OAUTH_TOKEN="$__MLP_CODING_AGENT_ENV_0"`) {
		t.Fatalf("launch script does not apply workflow token after sanitization: %s", text)
	}
}

func TestCommandWithFinalEnvRejectsInvalidUnsetKey(t *testing.T) {
	_, cleanup, err := CommandWithFinalEnv([]string{"cmd"}, "/tmp/work", nil, []string{"BAD-KEY"})
	if cleanup != nil {
		cleanup()
	}
	if err == nil {
		t.Fatal("CommandWithFinalEnv error = nil, want invalid unset key error")
	}
}

func TestCommandWithFinalEnvSanitizesCredentialsRestoredByLoginShell(t *testing.T) {
	shellDir := t.TempDir()
	shell := filepath.Join(shellDir, "zsh")
	shellBody := `#!/bin/sh
export ANTHROPIC_API_KEY=startup-api-key
export ANTHROPIC_AUTH_TOKEN=startup-auth-token
export ANTHROPIC_BASE_URL=https://startup.example
export CLAUDE_CODE_OAUTH_TOKEN=startup-oauth-token
if [ "$1" = "-ilc" ]; then
  command=$2
  shift 2
  exec /bin/sh -c "$command" "$@"
fi
exit 2
`
	if err := os.WriteFile(shell, []byte(shellBody), 0o755); err != nil {
		t.Fatalf("write fake login shell: %v", err)
	}
	t.Setenv(EnvShellPath, shell)
	t.Setenv(EnvShellMode, "")

	workDir := t.TempDir()
	capturePath := filepath.Join(workDir, "captured-env.txt")
	captureCommand := filepath.Join(workDir, "capture-env")
	captureBody := `#!/bin/sh
printf '%s|%s|%s|%s' "$ANTHROPIC_API_KEY" "$ANTHROPIC_AUTH_TOKEN" "$ANTHROPIC_BASE_URL" "$CLAUDE_CODE_OAUTH_TOKEN" > "$1"
`
	if err := os.WriteFile(captureCommand, []byte(captureBody), 0o755); err != nil {
		t.Fatalf("write capture command: %v", err)
	}

	got, cleanup, err := CommandWithFinalEnv(
		[]string{captureCommand, capturePath},
		workDir,
		[]string{"CLAUDE_CODE_OAUTH_TOKEN=workflow-secret"},
		[]string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN"},
	)
	if err != nil {
		t.Fatalf("CommandWithFinalEnv error = %v", err)
	}
	defer cleanup()
	// This test validates final environment sanitization, not launch latency.
	// Parallel adapter packages can briefly starve the fake login shell on small
	// CI runners, so keep the guard bounded without making scheduler load a
	// functional failure.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "/bin/sh", "-c", got).CombinedOutput(); err != nil {
		t.Fatalf("run final-env command: %v\n%s", err, output)
	}
	captured, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read captured environment: %v", err)
	}
	if string(captured) != "|||workflow-secret" {
		t.Fatalf("final environment = %q, want only workflow OAuth token", string(captured))
	}
}

func TestCommandSkipsUnsupportedShellOverride(t *testing.T) {
	unsupported := writeExecutableShell(t, "tcsh")
	supported := writeExecutableShell(t, "zsh")
	t.Setenv(EnvShellPath, unsupported)
	t.Setenv("SHELL", supported)
	t.Setenv(EnvShellMode, "")

	got := Command([]string{"claude"}, "/tmp/work")

	if strings.Contains(got, Quote(unsupported)) {
		t.Fatalf("command = %q, should not use unsupported shell", got)
	}
	if !strings.HasPrefix(got, Quote(supported)+" '-ilc' ") {
		t.Fatalf("command = %q, want supported shell fallback", got)
	}
}

func writeExecutableShell(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write shell: %v", err)
	}
	return path
}
