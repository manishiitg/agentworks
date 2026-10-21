package server

import (
	"errors"
	"strings"
	"testing"
)

// A Slack app that is not allowed to use Slack's MCP server is a Slack setting, so the person is told that, with the link.
func TestVaultSyncProblemSaysWhatTheServerSaid(t *testing.T) {
	slack := errors.New("Vault operation failed (400): upstream refused the connection: App is not enabled for Slack MCP server access. Please enable it here: https://api.slack.com/apps/A0B82LP3Z6Y/app-assistant")
	shown := vaultSyncProblem("Slack", slack)
	if !strings.Contains(shown, "has not allowed this app") || !strings.Contains(shown, "https://api.slack.com/apps/A0B82LP3Z6Y/app-assistant") || strings.Contains(shown, "transport") {
		t.Fatal("Slack refusal was not explained plainly:", shown)
	}
	other := vaultSyncProblem("Notion", errors.New("Vault operation failed (400): upstream refused the connection: Workspace suspended"))
	if !strings.Contains(other, "refused the connection: Workspace suspended") {
		t.Fatal("another server's reason was not shown:", other)
	}
}
