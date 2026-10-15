package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

func (api *StreamingAPI) registerPersonalGoogleAccountTool(reg definitionToolRegistrar, workspace string) error {
	return reg.RegisterCustomTool("manage_google_account",
		"Manage your personal Google accounts in this item. list_saved shows your reusable saved accounts; connect creates your own connection, adds it here, and returns a link for you to sign in with Google; attach adds one of your saved accounts here; detach removes it here and leaves other items connected; disconnect removes your saved connection everywhere; reconnect returns a sign-in link. Only existing Builders can add an account, including workflow editors. Only the account owner or an administrator manages the saved account or its Google access. Company accounts remain administrator-managed. "+googleAccountSharingNotice,
		map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"action":                   map[string]interface{}{"type": "string", "enum": []string{"list_saved", "connect", "attach", "detach", "disconnect", "reconnect"}},
			"connection_id":            map[string]interface{}{"type": "string", "description": "Account returned by list_saved or list_gmail_connections."},
			"allow_read_access":        map[string]interface{}{"type": "boolean", "description": "Allow the agent to read Gmail when connecting."},
			"allow_agent_write_access": map[string]interface{}{"type": "boolean", "description": "Allow Gmail drafts and send/reply when connecting."},
			"services":                 map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"service": map[string]interface{}{"type": "string", "enum": []string{"drive", "sheets", "docs", "slides", "calendar"}}, "write": map[string]interface{}{"type": "boolean"}}, "required": []string{"service"}}},
		}, "required": []string{"action"}}, func(ctx context.Context, args map[string]interface{}) (string, error) {
			req := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
			action, _ := args["action"].(string)
			id, _ := args["connection_id"].(string)
			if _, err := googleItemScope(req, workspace, true); err != nil && action != "detach" {
				return "", err
			}
			svc, err := ensureGmailService()
			if err != nil {
				return "", err
			}
			switch action {
			case "list_saved":
				accounts := []map[string]any{}
				for _, c := range svc.ListConnections() {
					if c.IsPrivate() && c.OwnerID == googleAccountActor(ctx) {
						accounts = append(accounts, map[string]any{"id": c.ID, "email": c.Email, "display_name": c.DisplayName, "added_here": c.UsableFrom(gmailToolScope(workspace))})
					}
				}
				out, _ := json.Marshal(map[string]any{"accounts": accounts, "notice": googleAccountSharingNotice})
				return string(out), nil
			case "attach", "detach":
				if err := selectGoogleAccount(req, id, workspace, action == "attach"); err != nil {
					return "", err
				}
				out, _ := json.Marshal(map[string]any{"selected": action == "attach", "notice": googleAccountSharingNotice})
				return string(out), nil
			case "connect":
				body := map[string]any{"workspace_path": workspace, "services": args["services"], "allow_read_access": args["allow_read_access"], "allow_agent_write_access": args["allow_agent_write_access"]}
				raw, _ := json.Marshal(body)
				connect := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(raw))).WithContext(ctx)
				rec := httptest.NewRecorder()
				googleAppConnectHandler(api)(rec, connect)
				if rec.Code != http.StatusCreated {
					return "", fmt.Errorf("%s", strings.TrimSpace(rec.Body.String()))
				}
				var created GmailConnectionResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
					return "", err
				}
				id = created.ID
			case "disconnect", "reconnect":
			default:
				return "", fmt.Errorf("Choose list_saved, connect, attach, detach, reconnect or disconnect.")
			}
			c, ok := svc.GetConnection(id)
			if !ok || !c.IsPrivate() || !canManageGoogleAccount(req, c) {
				return "", fmt.Errorf("Ask the account owner to manage this connection in Google apps.")
			}
			if action == "disconnect" {
				if err := svc.DeleteConnection(ctx, id); err != nil {
					return "", err
				}
				log.Printf("[GOOGLE_ACCOUNTS] actor=%s connection=%s items=%v reason=disconnect_everywhere", googleAccountActor(ctx), id, c.Workspaces)
				return "Google account disconnected from all items. Other saved accounts are unchanged.", nil
			}
			redirect, err := gmailOAuthRedirectURIFromEnv(c.ClientName)
			if err != nil {
				return "", err
			}
			auth, err := services.BeginGmailOAuth(c.ID, c.ClientName, redirect, c.AllowReadAccess, c.AllowAgentWriteAccess, services.GoogleServiceScopeURIs(c.Services))
			if err != nil {
				return "", err
			}
			out, _ := json.Marshal(map[string]any{"connection_id": c.ID, "auth_url": auth, "next": "Open this link and finish signing in with Google, then return to this chat.", "notice": googleAccountSharingNotice})
			return string(out), nil
		}, "gmail_connection_management")
}
