package mcpagent

import "testing"

func TestUserOAuthTokenFileHonorsServiceConfigDirectory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/service/config")
	if got := userOAuthTokenFile("alice", "Notion"); got != "/service/config/mcpagent/tokens/alice/Notion.json" {
		t.Fatal(got)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := userOAuthTokenFile("alice", "Notion"); got != "~/.config/mcpagent/tokens/alice/Notion.json" {
		t.Fatal(got)
	}
}
