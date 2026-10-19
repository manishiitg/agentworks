package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
)

// Personal inbox management reuses My Vault's account-owner and item Builder
// rules. It does not connect another Google account or grant it to a product.
func (api *StreamingAPI) personalGmailInboxOperation(r *http.Request, workspace string, args map[string]interface{}) (any, error) {
	action, _ := args["action"].(string)
	if err := personalAccountManagementScope(r, workspace, action != "get_inbox"); err != nil {
		return nil, err
	}
	if api.gmailInbound == nil {
		return nil, fmt.Errorf("Incoming email is unavailable on this server. Ask Builder to check the server setup.")
	}
	gmailTriggerConfigMu.Lock()
	defer gmailTriggerConfigMu.Unlock()
	owner := personalConnectionActor(r.Context())
	inbox, err := api.gmailInbound.Store.Inbox(r.Context(), owner)
	if err != nil {
		return nil, err
	}
	if action == "get_inbox" {
		return api.personalGmailInboxState(r, inbox)
	}
	if action != "select_inbox" && action != "rotate_inbox" {
		return nil, fmt.Errorf("Choose get_inbox, select_inbox or rotate_inbox.")
	}
	if action == "rotate_inbox" && inbox == nil {
		return nil, fmt.Errorf("Choose a connected Gmail account for your inbox in My Vault first.")
	}
	id, _ := args["connection_id"].(string)
	if action == "rotate_inbox" {
		id = inbox.ConnectionID
	}
	svc := services.GetGmailService()
	if svc == nil {
		return nil, fmt.Errorf("Connect Gmail in My Vault first.")
	}
	conn, found := svc.GetConnection(id)
	// Administrator access to manage a colleague's saved account does not let
	// that administrator turn it into their own personal receiving identity.
	if !found || conn.IsPrivate() && conn.OwnerID != owner || !conn.IsPrivate() && (!currentUserIsAdmin(r) || !canRemoveGmailConnection(r, conn)) {
		return nil, fmt.Errorf("Choose your own connected Gmail account in My Vault.")
	}
	if !conn.Enabled || !conn.AllowReadAccess {
		return nil, fmt.Errorf("This account needs Gmail read access. Change access in My Vault, then finish Google sign-in.")
	}
	status, _ := svc.AuthStatusForConnectionBlocking(r.Context(), id)
	if !status.Authenticated || !services.GoogleScopesGrant(status.Scopes, services.GmailReadonlyScope) {
		return nil, fmt.Errorf("Google has not granted Gmail read access. Change access for this account in My Vault.")
	}
	if inbox == nil {
		inbox = &gmailinbound.Inbox{ID: uuid.NewString(), OwnerID: owner}
	}
	if action == "rotate_inbox" || inbox.ConnectionID != id || inbox.Address == "" {
		// The random tag is independent of the inbox ID, owner and every target ID.
		address, err := inboundAddress(conn.Email, uuid.NewString())
		if err != nil {
			return nil, err
		}
		inbox.ConnectionID, inbox.Address = id, address
		if err := api.gmailInbound.Store.SaveInbox(r.Context(), *inbox); err != nil {
			return nil, err
		}
	}
	return api.personalGmailInboxState(r, inbox)
}

func (api *StreamingAPI) personalGmailInboxState(r *http.Request, inbox *gmailinbound.Inbox) (any, error) {
	bindings := []map[string]any{}
	if inbox != nil {
		routes, err := api.gmailInbound.Store.Routes(r.Context())
		if err != nil {
			return nil, err
		}
		for _, route := range routes {
			if route.OwnerID != inbox.OwnerID {
				continue
			}
			target, err := api.inboundTarget(r.Context(), route.OwnerID, route.WorkspacePath)
			if err != nil {
				continue
			}
			consent, err := api.gmailInbound.Store.SenderConsentStatus(r.Context(), route, gmailOwnerEmail(route))
			if err != nil {
				return nil, err
			}
			bindings = append(bindings, map[string]any{"name": api.gmailInboundTargetLabel(r.Context(), target), "workspace_path": route.WorkspacePath, "profile_id": route.ProfileID, "project_id": route.ProjectID, "enabled": route.Enabled, "current_address": inbox.Matches(route), "approved": consent.Approved})
		}
	}
	result := map[string]any{"inbox": inbox, "bindings": bindings, "notice": "Mail reaching your address can start every trigger you enable and approve. Manage what reaches it in Gmail. A new address stops all old bindings; ask Builder to reconnect and review each one."}
	if inbox != nil {
		if svc := services.GetGmailService(); svc != nil {
			if c, found := svc.GetConnection(inbox.ConnectionID); found {
				result["receiving_account"] = strings.ToLower(c.Email)
			}
		}
		if box, err := api.gmailInbound.Store.MailboxStatus(r.Context(), inbox.ConnectionID); err == nil {
			result["watch_ready"], result["error"] = box.Cursor != "", box.LastError
		}
	}
	return result, nil
}
