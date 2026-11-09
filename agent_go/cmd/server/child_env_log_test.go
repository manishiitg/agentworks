package server

import (
	"bytes"
	"log"

	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
)

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
