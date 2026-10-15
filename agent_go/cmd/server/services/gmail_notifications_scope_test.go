package services

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the actual manager, connector and CLI transport together. A shared
// runner uses the account added to the item even with company defaults off.
func TestPersonalGmailNotificationDelivery(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOG_HOME", filepath.Join(dir, "store"))
	t.Setenv("GMAIL_OAUTH_TOKEN_DIR", filepath.Join(dir, "tokens"))
	calls := filepath.Join(dir, "calls")
	binary := filepath.Join(dir, "gog")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> '" + calls + "'\nprintf '{\"id\":\"sent-id\"}'\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	conn := GmailConnection{ID: "personal", Personal: true, OwnerID: "builder", Workspaces: []string{"Workflow/mail"}, Enabled: true, AuthBackend: "gog", Email: "builder@example.com", ClientName: "app"}
	g := &GmailService{config: &GmailConfig{ManuallyDisabled: true, Connections: []GmailConnection{conn, {ID: "company", Enabled: true}}}, gogPath: binary}
	manager := &NotificationManager{connectors: map[string]NotificationConnector{"gmail": g}}
	dest := &NotificationDestination{UserID: "runner", Gmail: &GmailDest{Workspace: "Workflow/mail", Email: "recipient@example.com", ConnectionIDs: []string{"personal"}}}
	results := manager.SendUserNotificationSync(context.Background(), "Status", "", dest)
	if len(results) != 1 || !results[0].OK || results[0].MsgID != "sent-id" {
		t.Fatalf("delivery: %+v", results)
	}
	argv, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(argv), "--account\nbuilder@example.com\n--client\napp\n") || !strings.Contains(string(argv), gogHomeForConnection(conn)) {
		t.Fatalf("wrong selected identity: %s", argv)
	}
	if g.IsEnabled() || len(manager.ListEnabledConnectors()) != 0 {
		t.Fatal("personal delivery enabled company defaults")
	}
	for _, tc := range []struct {
		name, id, item    string
		disabled, revoked bool
	}{
		{name: "removed", id: "missing", item: "Workflow/mail"},
		{name: "wrong item", id: "personal", item: "Workflow/other"},
		{name: "unbound", id: "personal"},
		{name: "disabled", id: "personal", item: "Workflow/mail", disabled: true},
		{name: "revoked OAuth", id: "personal", item: "Workflow/mail", revoked: true},
		{name: "company off", id: "company", item: "Workflow/mail"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g.config.Connections[0] = conn
			if tc.disabled {
				g.config.Connections[0].Enabled = false
			}
			if tc.revoked {
				g.config.Connections[0].AuthBackend = ""
			}
			dest.Gmail.Workspace = tc.item
			dest.Gmail.ConnectionIDs = []string{tc.id}
			result := manager.SendUserNotificationSync(context.Background(), "Status", "", dest)
			if len(result) != 1 || result[0].OK || result[0].Err == "" {
				t.Fatalf("expected visible refusal: %+v", result)
			}
			after, _ := os.ReadFile(calls)
			if string(after) != string(argv) {
				t.Fatal("refused sender invoked transport or fell back")
			}
		})
	}
}
