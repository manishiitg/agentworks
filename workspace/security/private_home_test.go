package security

import (
	"path/filepath"
	"strings"
	"testing"
)

func homeEnvValue(env []string, key string) string {
	value := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			value = strings.TrimPrefix(kv, key+"=")
		}
	}
	return value
}

// HOME=/tmp was one home for every sandboxed command, so git credentials and
// CLI logins one user's agent wrote were read by every other agent (RTS
// 2026-09-28). Each workflow or Crew now gets its own.
func TestSandboxHomeIsPrivateToTheWorkflowOrCrew(t *testing.T) {
	base := []string{"HOME=/tmp", "PATH=/usr/bin", "XDG_CONFIG_HOME=/tmp/.xdg-config"}

	workflow := t.TempDir()
	persistent := filepath.Join(workflow, SandboxPersistentDirName)
	env := sandboxToolEnv(base, filepath.Join(workflow, "code", "step"), []string{persistent, workflow})
	if got := homeEnvValue(env, "HOME"); got != filepath.Join(persistent, "home") {
		t.Fatalf("workflow step HOME = %q", got)
	}
	if got := homeEnvValue(env, "XDG_CONFIG_HOME"); got != filepath.Join(persistent, "home", ".config") {
		t.Fatalf("workflow step XDG_CONFIG_HOME = %q", got)
	}

	crew := t.TempDir()
	env = sandboxToolEnv(base, crew, []string{crew})
	if got := homeEnvValue(env, "HOME"); got != filepath.Join(crew, SandboxPersistentDirName, "home") {
		t.Fatalf("Crew HOME = %q", got)
	}

	if got := homeEnvValue(env, "AGENT_BROWSER_SOCKET_DIR"); got != browserSocketDir {
		t.Fatalf("browser sockets must stay in a short folder, got %q", got)
	}
	for _, kv := range env {
		if kv == "HOME=/tmp" || strings.HasPrefix(kv, "XDG_CONFIG_HOME=/tmp") {
			t.Fatalf("shared /tmp home survived: %q", kv)
		}
	}
}

func TestSandboxHomeKeepsANativeHostHome(t *testing.T) {
	crew := t.TempDir()
	env := sandboxToolEnv([]string{"HOME=/Users/someone", "PATH=/usr/bin"}, crew, []string{crew})
	if got := homeEnvValue(env, "HOME"); got != "/Users/someone" {
		t.Fatalf("native HOME replaced: %q", got)
	}
}

func TestSandboxHomeWithoutAnyWriteGrantIsTheCommandScratch(t *testing.T) {
	scratch := t.TempDir()
	env := privateSandboxHome([]string{"HOME=/tmp"}, filepath.Join(scratch, "home"))
	if got := homeEnvValue(env, "HOME"); got != filepath.Join(scratch, "home") {
		t.Fatalf("HOME = %q", got)
	}
}
