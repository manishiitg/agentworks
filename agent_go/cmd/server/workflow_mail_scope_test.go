package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

// PLAT-746: actual saved script admission and mail delivery must use the same
// scoped account as Google tools, never another mailbox's shared default.
func TestScriptMailKeepsScopedSenderAndConvertedRecipientRules(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("NATIVE_WORKSPACE", "true")
	t.Setenv("AGENT_PRODUCTS", "work,agentworks")
	f.api.projectActions = NewProjectActionDispatcher(f.api, f.api.agentProfiles)
	owner := crewWriter("owner")
	owner.AccessToken.AllWorkflows = true
	crewCapabilityHTTP(t, f, owner, "workspace", map[string]any{"action": "automation_create", "kind": "script", "name": "Mail report"}, 200)
	bindings, err := readCrewExecutables()
	if err != nil || len(bindings) != 1 {
		t.Fatal(bindings, err)
	}
	var binding crewExecutableBinding
	for _, item := range bindings {
		binding = item
	}
	dir := t.TempDir()
	calls, binary := filepath.Join(dir, "calls"), filepath.Join(dir, "gog")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> '"+calls+"'\nprintf '{\"id\":\"sent-fixture\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GMAIL_CONNECTIONS_DIR", filepath.Join(dir, "connections"))
	t.Setenv("GMAIL_OAUTH_TOKEN_DIR", filepath.Join(dir, "tokens"))
	t.Setenv("GOG_HOME", filepath.Join(dir, "store"))
	previous := services.GetGmailService()
	manager := services.GetNotificationManager()
	oldConnector, _ := manager.GetConnector("gmail")
	t.Cleanup(func() {
		services.SetGmailService(previous)
		manager.UnregisterConnector("gmail")
		if oldConnector != nil {
			manager.RegisterConnector(oldConnector)
		}
	})
	shared := services.GmailConnection{ID: "shared", Enabled: true, AuthBackend: "gog", Email: "company@example.com", ClientName: "app"}
	private := services.GmailConnection{ID: "personal", Personal: true, OwnerID: "owner", Workspaces: []string{binding.Root}, Enabled: true, AuthBackend: "gog", Email: "builder@example.com", ClientName: "app"}
	for _, tc := range []struct {
		name         string
		connections  []services.GmailConnection
		capabilities map[string]any
		want         string
		refused      bool
	}{
		{name: "selected private", connections: []services.GmailConnection{shared, private}, want: private.Email},
		{name: "shared default", connections: []services.GmailConnection{shared}, want: shared.Email},
		{name: "explicit shared sender", connections: []services.GmailConnection{shared, private}, capabilities: map[string]any{"gmail_connection_id": "shared"}, want: shared.Email},
		{name: "missing explicit sender", connections: []services.GmailConnection{shared, private}, capabilities: map[string]any{"gmail_connection_id": "missing"}, refused: true},
		{name: "no account", refused: true},
		{name: "ambiguous", connections: []services.GmailConnection{shared, private, {ID: "second", Personal: true, OwnerID: "owner", Workspaces: []string{binding.Root}, Enabled: true, AuthBackend: "gog", Email: "second@example.com"}}, refused: true},
		{name: "converted block retained", connections: []services.GmailConnection{shared}, capabilities: map[string]any{"gmail_connection_id": "shared", "gmail_blocked_recipients": []string{"recipient@example.com"}}, refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, _ := json.Marshal(services.GmailConfig{Enabled: len(tc.connections) > 0, GogPath: binary, UseGogBackend: true, DefaultConnectionID: "shared", Connections: tc.connections, HostAccountDismissed: true})
			f.write(t, "config/gmail-config.json", string(config))
			g, err := services.InitGmailService()
			if err != nil {
				t.Fatal(err)
			}
			manager.RegisterConnector(g)
			definition, err := readCrewScript(t.Context(), binding.Root)
			if err != nil {
				t.Fatal(err)
			}
			definition.Capabilities, _ = json.Marshal(tc.capabilities)
			if tc.capabilities == nil {
				definition.Capabilities = json.RawMessage(`{}`)
			}
			content, _ := json.Marshal(definition)
			crewCapabilityHTTP(t, f, owner, "workspace", map[string]any{"action": "automation_write", "workspace_path": binding.Root, "file": "script.json", "content": string(content)}, 200)
			ctx := adminRequest(http.MethodPost, "/api/query", "", owner, nil).Context()
			req := QueryRequest{AgentMode: "workflow", SelectedFolder: binding.Root, PresetQueryID: binding.ID}
			if _, _, err := f.api.admitQueryTarget(ctx, &req, "owner", "mail-fixture"); err != nil {
				t.Fatal(err)
			}
			dest := notificationDestinationFromQuery(req, "owner")
			if dest == nil || dest.Gmail == nil || dest.Gmail.Workspace == "" {
				t.Fatal("admitted scope missing from mail destination")
			}
			before, _ := os.ReadFile(calls)
			output, sendErr := virtualtools.CreateHumanToolExecutors()["send_email"](context.WithValue(ctx, virtualtools.BotNotificationDestinationKey, dest), map[string]interface{}{"to": []string{"recipient@example.com"}, "subject": "Report", "body": "Ready"})
			after, _ := os.ReadFile(calls)
			if tc.refused {
				if sendErr == nil && strings.Contains(output, `"status":"delivered"`) {
					t.Fatalf("refused mail reported delivered: %s", output)
				}
				if string(before) != string(after) {
					t.Fatal("refused send reached external transport or fell back")
				}
				if tc.name != "converted block retained" && (sendErr == nil || !strings.Contains(sendErr.Error(), "mail account")) {
					t.Fatalf("missing sender remedy: %s %v", output, sendErr)
				}
			} else if sendErr != nil || !strings.Contains(output, "sent-fixture") || !strings.Contains(string(after[len(before):]), "--account\n"+tc.want+"\n") {
				t.Fatalf("wrong delivery account: %s %v argv=%s", output, sendErr, after[len(before):])
			}
		})
	}
}
