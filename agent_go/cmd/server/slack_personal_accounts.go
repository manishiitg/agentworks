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

	"github.com/manishiitg/mcpagent/mcpclient"
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
	// Problem is why the account last failed to connect, in Slack's own words; empty once it connects again.
	Problem string `json:"problem,omitempty"`
}

type slackAccountsResponse struct {
	Accounts   []slackAccountRow `json:"accounts"`
	CanConnect bool              `json:"can_connect"`
	Notice     string            `json:"notice"`
	// SignInReady is false while the deployment's Slack sign-in app is not set up.
	SignInReady bool `json:"sign_in_ready"`
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
	return reg.RegisterCustomTool("manage_slack_account", "Manage personal Slack accounts saved in your Vault, separate from the item’s bot. Without an item, connect saves the account without selecting it anywhere. list_saved: saved accounts and those added here, with current permissions. connect: connect a new account and explicitly add it here, returning a sign-in link. attach/detach: add a saved account here or remove it only here. reconnect: sign in again. disconnect: remove the saved account everywhere. Current Builders, including workflow editors, can add their own account. Only the account owner signs in or reconnects; account owners or administrators can disconnect the saved connection. Shared runs use this item's selected account, with the caller's existing access. "+slackAccountNotice, map[string]interface{}{"type": "object", "additionalProperties": false, "required": []string{"action"}, "properties": map[string]interface{}{
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
	if strings.TrimSpace(tool) == "" || !isOwnerScopedMCPName(server) && !strings.HasPrefix(strings.ToLower(server), "slack") {
		return nil
	}
	root := api.mcpProjectRoot(ctx, session)
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
	if strings.TrimSpace(tool) == "" || !isOwnerScopedMCPName(server) && !strings.HasPrefix(strings.ToLower(server), "slack") {
		return
	}
	root := api.mcpProjectRoot(ctx, session)
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

// personalSlackSendOnlyEntries lists, as "server:tool" selections, the send-side tools of every personal Slack account
// added to root that does not belong to caller, with the account's internal server names. A turn for someone who is not
// the account's owner offers only these tools of that server (the read side is also refused on the server).
func personalSlackSendOnlyEntries(root, caller string) (entries []string, servers map[string]bool) {
	attachments, _ := placeMCPAttachmentsFor(projectRootOf(root))
	servers = map[string]bool{}
	for _, a := range attachments {
		if a.Scope != "user" || a.Owner == caller {
			continue
		}
		if _, err := personalSlackServer(a.Owner, a.Server); err != nil {
			continue
		}
		internal := placeMCPInternalName(a.Owner, a.Server)
		servers[internal] = true
		names := make([]string, 0, len(slackSendSideTools))
		for tool := range slackSendSideTools {
			names = append(names, tool)
		}
		sort.Strings(names)
		for _, tool := range names {
			entries = append(entries, internal+":"+tool)
		}
	}
	return entries, servers
}

// restrictSelectedToolsForSlack returns the turn's tool selection with each non-owned personal Slack server cut down to
// its send side. A selection that was empty (every tool of every server) becomes explicit "server:*" entries for the
// other servers, so only the Slack server narrows.
func restrictSelectedToolsForSlack(selected, serverNames []string, entries []string, restricted map[string]bool) []string {
	if len(entries) == 0 {
		return selected
	}
	out := make([]string, 0, len(selected)+len(serverNames)+len(entries))
	for _, tool := range selected {
		server, _, _ := strings.Cut(tool, ":")
		if !restricted[server] {
			out = append(out, tool)
		}
	}
	for _, name := range serverNames {
		if name == "" || name == mcpclient.NoServers || restricted[name] {
			continue
		}
		has := false
		for _, tool := range out {
			if strings.HasPrefix(tool, name+":") {
				has = true
				break
			}
		}
		if !has {
			out = append(out, name+":*")
		}
	}
	return append(out, entries...)
}
