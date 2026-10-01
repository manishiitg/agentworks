package server

import (
	"strings"
	"testing"
)

func childEnvMap(t *testing.T, env []string) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, kv := range env {
		key, value, _ := strings.Cut(kv, "=")
		m[key] = value
	}
	return m
}

func TestMinimalChildEnvDropsSecrets(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/tmp/fake-home")
	t.Setenv("AUTH_SECRET", "fake-auth-secret")
	t.Setenv("MCP_SERVER_API_TOKEN", "fake-token")
	t.Setenv("OPENAI_API_KEY", "fake-key")
	m := childEnvMap(t, minimalChildEnv("TERM=xterm-256color"))
	if m["PATH"] != "/usr/bin:/bin" || m["HOME"] != "/tmp/fake-home" {
		t.Fatalf("base vars missing: %v", m)
	}
	if m["TERM"] != "xterm-256color" {
		t.Fatalf("extra not appended: %v", m)
	}
	for _, leaked := range []string{"AUTH_SECRET", "MCP_SERVER_API_TOKEN", "OPENAI_API_KEY"} {
		if _, ok := m[leaked]; ok {
			t.Errorf("%s leaked into the child environment", leaked)
		}
	}
}

func TestPassthroughChildEnvCopiesOnlyNamedSetVars(t *testing.T) {
	t.Setenv("CURSOR_API_KEY", "fake-cursor-key")
	m := childEnvMap(t, passthroughChildEnv("CURSOR_API_KEY", "DEFINITELY_UNSET_VAR"))
	if m["CURSOR_API_KEY"] != "fake-cursor-key" {
		t.Fatalf("named var not passed through: %v", m)
	}
	if _, ok := m["DEFINITELY_UNSET_VAR"]; ok {
		t.Fatalf("unset var fabricated: %v", m)
	}
	if len(m) != 1 {
		t.Fatalf("unexpected entries: %v", m)
	}
}
