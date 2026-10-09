package services

import (
	"path/filepath"
	"testing"
)

// The refresh-token directory follows XDG_CONFIG_HOME like the MCP connector
// tokens do: on server A ~/.config is root-owned and only the XDG tree is writable.
func TestGmailOAuthTokenDirHonoursXDGConfigHome(t *testing.T) {
	t.Setenv("GMAIL_OAUTH_TOKEN_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "/srv/xdg")
	if got, want := gmailOAuthTokenDir(), filepath.Join("/srv/xdg", "agentworks", "gmail-oauth"); got != want {
		t.Fatalf("token dir = %q, want %q", got, want)
	}
	t.Setenv("GMAIL_OAUTH_TOKEN_DIR", "/explicit/dir")
	if got := gmailOAuthTokenDir(); got != "/explicit/dir" {
		t.Fatalf("explicit override must win, got %q", got)
	}
}

// The named-client secrets directory follows the identical rule, and must
// stay host-level (never under the workspace docs root) — a client_secret.json
// is a real credential, and workspace content is readable by anything that
// can read the workspace.
func TestGmailOAuthClientsBaseDirHonoursXDGConfigHome(t *testing.T) {
	t.Setenv("GMAIL_OAUTH_CLIENTS_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "/srv/xdg")
	if got, want := gmailOAuthClientsBaseDir(), filepath.Join("/srv/xdg", "agentworks", "gmail-oauth-clients"); got != want {
		t.Fatalf("clients dir = %q, want %q", got, want)
	}
	t.Setenv("GMAIL_OAUTH_CLIENTS_DIR", "/explicit/dir")
	if got := gmailOAuthClientsBaseDir(); got != "/explicit/dir" {
		t.Fatalf("explicit override must win, got %q", got)
	}
}
