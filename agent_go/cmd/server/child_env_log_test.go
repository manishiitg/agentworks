package server

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
)

func TestChildEnvLogEnabled(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", true},
		{"1", true},
		{"true", true},
		{"yes", true},
		{"0", false},
		{"false", false},
		{"FALSE", false},
		{"off", false},
		{"no", false},
	}
	for _, tc := range cases {
		t.Setenv("LOG_CHILD_ENV", tc.value)
		if got := childEnvLogEnabled(); got != tc.want {
			t.Fatalf("LOG_CHILD_ENV=%q enabled=%v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestChildEnvLogEnabledByDefault(t *testing.T) {
	value, had := os.LookupEnv("LOG_CHILD_ENV")
	_ = os.Unsetenv("LOG_CHILD_ENV")
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("LOG_CHILD_ENV", value)
		}
	})
	if !childEnvLogEnabled() {
		t.Fatal("child-env logging should be on unless explicitly disabled")
	}
}

func TestDiffEnvNames(t *testing.T) {
	stripped, added := diffEnvNames(
		[]string{"KEEP=1", "DROP=2", "SAME=old"},
		[]string{"SAME=new", "KEEP=1", "NEW=3"},
	)
	if strings.Join(stripped, ",") != "DROP" {
		t.Fatalf("stripped=%v, want [DROP]", stripped)
	}
	if strings.Join(added, ",") != "NEW" {
		t.Fatalf("added=%v, want [NEW]", added)
	}
}

// The audit log must show which names a child inherits without ever printing
// a value: names are safe to grep, values in a log file would be a leak.
func TestProviderConnectionSetupEnvironmentLogsNamesNotValues(t *testing.T) {
	t.Setenv("LOG_CHILD_ENV", "1")
	t.Setenv("PROBE_INHERITED_VAR", "probe-secret-value-abc123")
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })

	token := "fake-oauth-token-xyz789"
	keys := &llm.ProviderAPIKeys{ClaudeCodeOAuthToken: &token}
	_ = providerConnectionSetupEnvironment("provider-connection provider=claude-code account=test", keys)

	out := buf.String()
	if !strings.Contains(out, "[CHILD_ENV]") {
		t.Fatal("expected a [CHILD_ENV] audit line")
	}
	if !strings.Contains(out, "PROBE_INHERITED_VAR") {
		t.Fatal("expected the inherited variable name in the audit log")
	}
	if strings.Contains(out, "probe-secret-value-abc123") || strings.Contains(out, "fake-oauth-token-xyz789") {
		t.Fatal("audit log leaked a secret value")
	}
}

func TestLogChildEnvDisabled(t *testing.T) {
	t.Setenv("LOG_CHILD_ENV", "0")
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })

	logChildEnv("test", []string{"A=1"}, []string{"A=1", "B=2"})
	if buf.Len() != 0 {
		t.Fatalf("expected no output with LOG_CHILD_ENV=0, got %q", buf.String())
	}
}
