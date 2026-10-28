package store

import "testing"

// A pasted token never becomes a connection's visible name (PLAT-856-400), and ordinary names are kept.
func TestConnectionLabelNeverHoldsACredential(t *testing.T) {
	for label, credential := range map[string]bool{
		"ntn_" + "0123456789abcdefABCDEF": true, "xoxb-" + "1234567890-abcdef": true, "ghp_" + "abcdefghijklmnop1234": true,
		"Q2xpY2tVcEFwaUtleUZvclRlc3RpbmcxMjM0NQ": true,
		"Notion": false, "Notion · Engineering": false, "my-notion-workspace-2026": false, "Sales team ClickUp account for 2026 planning": false,
	} {
		if LooksLikeCredential(label) != credential {
			t.Errorf("LooksLikeCredential(%q) = %v", label, !credential)
		}
	}
	s := NewMemoryStore()
	s.AddWorkspace(Workspace{ID: "w", Name: "w"})
	s.AddConnector(Connector{ID: "c-1", WorkspaceID: "w", Provider: "notion", Label: "ntn_" + "0123456789abcdefABCDEF", VaultID: "v-1"})
	if got := s.connectors["c-1"].Label; got != "notion" {
		t.Fatalf("a credential-looking label was stored: %q", got)
	}
	s.connectors["c-2"] = Connector{ID: "c-2", WorkspaceID: "w", Provider: "notion", Label: "ntn_" + "9876543210abcdefABCDEF", VaultID: "v-1"}
	if err := s.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	if got := s.connectors["c-2"].Label; got != "notion" {
		t.Fatalf("a stored credential-looking label survived start: %q", got)
	}
}
