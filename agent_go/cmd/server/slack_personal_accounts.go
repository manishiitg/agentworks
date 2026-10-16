package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
)

const slackAccountNotice = "Messages sent with your Slack show as from you. Anyone who can use this item can send as you through it, but only you can read or search your Slack."

// Slack uses the existing sealed personal MCP login and explicit attachment
// index. No tokens are copied into a product, tool reply or chat.
func isPersonalSlack(server placeMCPServer) bool {
	return server.Catalog == "Slack" && server.URL == "https://mcp.slack.com/mcp" && server.OAuth != nil && server.OAuth.AuthURL == "https://slack.com/oauth/v2_user/authorize" && server.OAuth.TokenURL == "https://slack.com/api/oauth.v2.user.access"
}

type slackAccountRow struct {
	personalConnectionPermissions
	ID        string `json:"id"`
	Owner     string `json:"owner"`
	Label     string `json:"label"`
	Selected  bool   `json:"selected"`
	Connected bool   `json:"connected"`
	// Mine marks the caller's own account; others appear only when added here.
	Mine bool `json:"mine"`
}

type slackAccountsResponse struct {
	Accounts   []slackAccountRow `json:"accounts"`
	CanConnect bool              `json:"can_connect"`
	Notice     string            `json:"notice"`
	// SignInReady is false while the deployment's Slack sign-in app is not set up.
	SignInReady bool `json:"sign_in_ready"`
}

func slackSelected(owner, id, root string) bool {
	attachments, _ := placeMCPAttachmentsFor(root)
	for _, a := range attachments {
		if a.Owner == owner && a.Server == id && a.Scope == "user" {
			return true
		}
	}
	return false
}

func personalSlackServer(owner, id string) (placeMCPServer, error) {
	servers, err := listPlaceMCPServers(owner)
	if err != nil {
		return placeMCPServer{}, err
	}
	for _, server := range servers {
		if server.Name == id && isPersonalSlack(server) {
			return server, nil
		}
	}
	return placeMCPServer{}, fmt.Errorf("Choose a saved Slack account from this item's Slack settings.")
}

// ownPersonalSlack returns the person's saved Slack account (one per person,
// owner 2026-10-16), preferring a signed-in one, or "" when they have none.
func ownPersonalSlack(person string) (string, bool, error) {
	servers, err := listPlaceMCPServers(person)
	if err != nil {
		return "", false, err
	}
	dir, err := placeMCPDir(person)
	if err != nil {
		return "", false, err
	}
	id := ""
	for _, server := range servers {
		if !isPersonalSlack(server) {
			continue
		}
		if placeMCPServerConnected(dir, person, server) {
			return server.Name, true, nil
		}
		if id == "" {
			id = server.Name
		}
	}
	return id, false, nil
}

// slackSignInReady reports whether people can sign in to Slack: the
// deployment's Slack sign-in app is set up, or the catalog brings its own.
func (api *StreamingAPI) slackSignInReady() bool {
	for _, group := range api.mcpAppGroups() {
		if group.Key == "slack" {
			return group.Configured
		}
	}
	return true
}

func (api *StreamingAPI) personalSlackAccounts(r *http.Request, workspace string) (slackAccountsResponse, error) {
	out := slackAccountsResponse{Accounts: []slackAccountRow{}, Notice: slackAccountNotice}
	actor := personalConnectionActor(r.Context())
	if actor == "" || !canManagePersonalConnection(r, actor) {
		return out, fmt.Errorf("Sign in to manage your Slack accounts in Integrations → Slack.")
	}
	root := personalConnectionItemRoot(actor, workspace)
	_, buildErr := personalConnectionItemScope(r, workspace, true, "Slack", "Integrations → Slack")
	out.CanConnect = buildErr == nil
	out.SignInReady = api.slackSignInReady()
	// A person's own saved accounts stay private. A product Builder may also
	// inspect and detach accounts explicitly selected for this item.
	owners := map[string]bool{actor: true}
	if buildErr == nil {
		attached, err := placeMCPAttachmentsFor(root)
		if err != nil {
			return out, err
		}
		for _, a := range attached {
			if a.Scope == "user" {
				owners[a.Owner] = true
			}
		}
	}
	for owner := range owners {
		servers, err := listPlaceMCPServers(owner)
		if err != nil {
			return out, err
		}
		dir, err := placeMCPDir(owner)
		if err != nil {
			return out, err
		}
		for _, server := range servers {
			selected := slackSelected(owner, server.Name, root)
			if !isPersonalSlack(server) || owner != actor && !selected {
				continue
			}
			label := "Your Slack"
			if owner != actor {
				label = shareOwnerLabel(owner) + "'s Slack"
			}
			out.Accounts = append(out.Accounts, slackAccountRow{personalConnectionPermissions: personalConnectionAccess(r, owner, workspace, selected, "Slack", "Integrations → Slack"), ID: server.Name, Owner: owner, Label: label, Selected: selected, Connected: placeMCPServerConnected(dir, owner, server), Mine: owner == actor})
		}
	}
	sort.Slice(out.Accounts, func(i, j int) bool {
		if out.Accounts[i].Label != out.Accounts[j].Label {
			return out.Accounts[i].Label < out.Accounts[j].Label
		}
		return out.Accounts[i].Owner+out.Accounts[i].ID < out.Accounts[j].Owner+out.Accounts[j].ID
	})
	return out, nil
}

// One operation path is used by the app and the native Builder/MCP tool.
func (api *StreamingAPI) personalSlackOperation(r *http.Request, workspace string, args map[string]interface{}) (any, error) {
	actor := personalConnectionActor(r.Context())
	if actor == "" || !canManagePersonalConnection(r, actor) {
		return nil, fmt.Errorf("Sign in to manage your Slack accounts in Integrations → Slack.")
	}
	action, _ := args["action"].(string)
	if action == "list_saved" {
		return api.personalSlackAccounts(r, workspace)
	}
	id, _ := args["connection_id"].(string)
	owner, _ := args["connection_owner"].(string)
	if owner == "" {
		owner = actor
	}
	if owner != sanitizeUserIDForPath(owner) {
		return nil, fmt.Errorf("Choose a saved Slack account from list_saved.")
	}
	root := personalConnectionItemRoot(actor, workspace)
	if action == "connect" {
		scope, err := personalConnectionItemScope(r, workspace, true, "Slack", "Integrations → Slack")
		if err != nil {
			return nil, err
		}
		root, owner = scope.Workspace, actor
		label, _ := args["label"].(string)
		if strings.TrimSpace(label) == "" {
			label = "My Slack account"
		}
		// One Slack account per person (owner, 2026-10-16): Connect reuses the
		// saved one, so repeated clicks never pile up empty accounts.
		var signedIn bool
		id, signedIn, err = ownPersonalSlack(actor)
		if err != nil {
			return nil, err
		}
		if signedIn {
			if err := attachPersonalLoginToPlace(actor, id, root); err != nil {
				return nil, err
			}
			log.Printf("[SLACK_ACCOUNTS] actor=%s account=%s owner=%s item=%s reason=connect_existing", actor, id, actor, root)
			return map[string]any{"connection_id": id, "selected": true, "message": "Your Slack is now used here.", "notice": slackAccountNotice}, nil
		}
		if id == "" {
			saved, _, err := api.ensurePersonalMCP(r.Context(), actor, placeMCPServer{Label: label}, "Slack")
			if err != nil {
				return nil, err
			}
			id = saved.Name
		}
		if err := attachPersonalLoginToPlace(actor, id, root); err != nil {
			return nil, err
		}
		log.Printf("[SLACK_ACCOUNTS] actor=%s account=%s owner=%s item=%s reason=connect_and_select", actor, id, owner, root)
	}
	if _, err := personalSlackServer(owner, id); err != nil {
		return nil, err
	}
	selected := slackSelected(owner, id, root)
	permissions := personalConnectionAccess(r, owner, workspace, selected, "Slack", "Integrations → Slack")
	switch action {
	case "attach":
		if !permissions.CanAdd {
			return nil, fmt.Errorf("Add your own saved Slack account to an item you build, from Integrations → Slack.")
		}
		if err := attachPersonalLoginToPlace(owner, id, root); err != nil {
			return nil, err
		}
	case "detach":
		if !permissions.CanDetach {
			return nil, fmt.Errorf("Ask this item's Builder or the account owner to remove it in Integrations → Slack.")
		}
		if err := forgetPlaceMCP(owner, id, root); err != nil {
			return nil, err
		}
	case "disconnect":
		if !permissions.CanManage {
			return nil, fmt.Errorf("Ask the account owner or an administrator to disconnect this saved Slack account.")
		}
		// Removing the saved server makes every current and future item lookup
		// stop resolving it. Keep the private store's other accounts untouched.
		if err := removePlaceMCPServer(owner, id); err != nil {
			return nil, err
		}
		closePlaceMCPConnection(owner, id)
		if err := forgetPlaceMCPLogin(owner, id); err != nil {
			return nil, err
		}
		placeMCPMu.Lock()
		all, err := readPlaceMCPAttachmentsLocked()
		if err == nil {
			for item, list := range all {
				kept := list[:0]
				for _, a := range list {
					if a.Owner != owner || a.Server != id || a.Scope != "user" {
						kept = append(kept, a)
					}
				}
				if len(kept) == 0 {
					delete(all, item)
				} else {
					all[item] = kept
				}
			}
			err = writePlaceMCPAttachmentsLocked(all)
		}
		placeMCPMu.Unlock()
		if err != nil {
			return nil, err
		}
	case "connect", "reconnect":
		if !permissions.CanReconnect {
			return nil, fmt.Errorf("Only the account owner can sign in again. Ask them to reconnect this saved Slack account in Integrations → Slack.")
		}
		authURL, discovery, _, err := api.startPlaceMCPSignIn(owner, id, deriveOAuthRedirectURI(r), "", nil)
		if err != nil {
			return nil, err
		}
		if discovery != nil {
			return map[string]any{"connection_id": id, "status": discovery.Status, "message": "Ask an administrator to set up the Slack sign-in app in Integrations → Slack. Then choose Sign in again. Slack's app must allow its MCP server; the app name should be AgentWorks.", "redirect_uri": discovery.RedirectURI, "notice": slackAccountNotice}, nil
		}
		return map[string]any{"connection_id": id, "auth_url": authURL, "next": "Open this link and sign in with Slack, then return here. This account is used only in the items you explicitly add it to.", "notice": slackAccountNotice}, nil
	default:
		return nil, fmt.Errorf("Choose list_saved, connect, attach, detach, reconnect or disconnect.")
	}
	log.Printf("[SLACK_ACCOUNTS] actor=%s account=%s owner=%s item=%s reason=%s", actor, id, owner, root, action)
	return map[string]any{"connection_id": id, "selected": action == "attach", "notice": slackAccountNotice}, nil
}

func (api *StreamingAPI) handlePersonalSlackAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	args := map[string]interface{}{"action": "list_saved"}
	workspace := r.URL.Query().Get("workspace_path")
	if r.Method == http.MethodPost {
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&args) != nil {
			writeAgentProfileError(w, 400, "Choose a Slack account and action.")
			return
		}
		workspace, _ = args["workspace_path"].(string)
	}
	result, err := api.personalSlackOperation(r, workspace, args)
	if err != nil {
		writeAgentProfileError(w, 403, err.Error())
		return
	}
	writeAgentProfileJSON(w, 200, result)
}

func (api *StreamingAPI) registerPersonalSlackAccountTool(reg definitionToolRegistrar, workspace string) error {
	return reg.RegisterCustomTool("manage_slack_account", "Manage personal Slack accounts for this item, separate from its bot. list_saved: saved accounts and those added here, with current permissions. connect: connect a new account and explicitly add it here, returning a sign-in link. attach/detach: add a saved account here or remove it only here. reconnect: sign in again. disconnect: remove the saved account everywhere. Current Builders, including workflow editors, can add their own account. Only the account owner signs in or reconnects; account owners or administrators can disconnect the saved connection. Shared runs use this item's selected account, with the caller's existing access. "+slackAccountNotice, map[string]interface{}{"type": "object", "additionalProperties": false, "required": []string{"action"}, "properties": map[string]interface{}{
		"action":           map[string]interface{}{"type": "string", "enum": []string{"list_saved", "connect", "attach", "detach", "reconnect", "disconnect"}},
		"connection_id":    map[string]interface{}{"type": "string", "description": "Exact saved account ID returned by list_saved."},
		"connection_owner": map[string]interface{}{"type": "string", "description": "Owner returned by list_saved; omit for your own account."},
		"label":            map[string]interface{}{"type": "string", "description": "Name for a new Slack account, for example Work Slack."},
	}}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		r := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
		result, err := api.personalSlackOperation(r, workspace, args)
		if err != nil {
			return "", err
		}
		raw, err := json.Marshal(result)
		return string(raw), err
	}, "mcp_server_tools")
}

// slackSendSideTools are the only tools of a personal Slack account that someone other than its owner may use: send a
// message or DM as the owner, and look people up. Everything else (read, history, search of channels, DMs, files and
// canvases) is the owner's alone, and any tool not named here is treated the same way.
var slackSendSideTools = map[string]bool{
	"slack_send_message":         true,
	"slack_send_message_draft":   true,
	"slack_schedule_message":     true,
	"slack_search_users":         true,
	"slack_read_user_profile":    true,
	"slack_get_user_profile":     true,
	"slack_lookup_user_by_email": true,
}

// personalSlackToolAllowed is the whole rule: the account's owner may use every tool, anyone else only the send side.
func personalSlackToolAllowed(caller, owner, tool string) bool {
	return (caller != "" && caller == owner) || slackSendSideTools[strings.ToLower(strings.TrimSpace(tool))]
}

// personalSlackCallError is the plain refusal for a read-side tool called by someone who does not own the account.
const personalSlackCallError = "Only the person who connected this Slack can read or search it. You can send messages with it and look people up."

// guardPersonalSlackCall decides one tool call on a personal Slack account that is added to the item (owner decision
// 2026-10-16, the decisions log): its owner may use every tool; anyone else only the send-side tools. It returns an error
// for a refused call and logs the attempt or the refusal. Calls on any other server pass unchanged.
func (api *StreamingAPI) guardPersonalSlackCall(ctx context.Context, session, server, tool string) error {
	if strings.TrimSpace(tool) == "" || !isPlaceMCPInternalName(server) && !strings.HasPrefix(strings.ToLower(server), "slack") {
		return nil
	}
	root := api.mcpPlaceRoot(ctx, session)
	attachments, _ := placeMCPAttachmentsFor(root)
	for _, a := range attachments {
		if a.Scope != "user" || server != placeMCPInternalName(a.Owner, a.Server) && !strings.EqualFold(server, a.Server) {
			continue
		}
		if _, err := personalSlackServer(a.Owner, a.Server); err != nil {
			continue
		}
		caller := mcpCaller(ctx)
		if caller == "" && session != "" && api.eventStore != nil {
			caller = api.mcpSessionPerson(session)
		}
		if personalSlackToolAllowed(caller, a.Owner, tool) {
			log.Printf("[SLACK_ACCOUNTS] account=%s owner=%s item=%s caller=%s session=%s tool=%s event=tool_attempt", a.Server, a.Owner, root, caller, session, tool)
			continue
		}
		log.Printf("[SLACK_ACCOUNTS] account=%s owner=%s item=%s caller=%s session=%s tool=%s event=tool_refused reason=not_account_owner", a.Server, a.Owner, root, caller, session, tool)
		return fmt.Errorf("%s", personalSlackCallError)
	}
	return nil
}

// Resolution logs a send attempt, not delivery. Slack's reply determines delivery.
func (api *StreamingAPI) logPersonalSlackCall(ctx context.Context, session, server, tool, stage string) {
	if strings.TrimSpace(tool) == "" || !isPlaceMCPInternalName(server) && !strings.HasPrefix(strings.ToLower(server), "slack") {
		return
	}
	root := api.mcpPlaceRoot(ctx, session)
	attachments, _ := placeMCPAttachmentsFor(root)
	for _, a := range attachments {
		if a.Scope != "user" || server != placeMCPInternalName(a.Owner, a.Server) && !strings.EqualFold(server, a.Server) {
			continue
		}
		if _, err := personalSlackServer(a.Owner, a.Server); err != nil {
			continue
		}
		caller := mcpCaller(ctx)
		if caller == "" && session != "" && api.eventStore != nil {
			caller = api.mcpSessionPerson(session)
		}
		log.Printf("[SLACK_ACCOUNTS] account=%s owner=%s item=%s caller=%s session=%s tool=%s event=tool_%s", a.Server, a.Owner, root, caller, session, tool, stage)
	}
}
