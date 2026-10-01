package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"os"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

// gmailOAuthRedirectURIFromEnv mirrors deriveOAuthRedirectURIFromEnv
// (oauth_routes.go)'s pattern for the generic MCP OAuth flow, but for
// Gmail's own fixed callback path. Needed because a tool call (unlike the
// browser-driven /auth/start route in gmail_oauth_routes.go) has no
// *http.Request to derive the host from -- PUBLIC_URL is this deployment's
// own known public base URL instead.
func gmailOAuthRedirectURIFromEnv(clientName ...string) (string, error) {
	publicURL := strings.TrimSpace(os.Getenv("PUBLIC_URL"))
	if publicURL == "" {
		return "", fmt.Errorf("PUBLIC_URL is not configured on this server; cannot generate a Gmail reconnect link from chat")
	}
	callback := gmailOAuthCallbackPath
	if len(clientName) > 0 && clientName[0] == services.PlatformGoogleClientName {
		callback = "/api/oauth/callback"
	}
	return strings.TrimRight(publicURL, "/") + callback, nil
}

// gmailGrantMismatchNote is included in both list_gmail_connections' tool
// description and its result payload, so the agent sees the diagnostic
// decision tree whether it reads the description up front or only the
// result -- and so it doesn't need to re-derive this reasoning itself.
const gmailGrantMismatchNote = "granted_scopes reflects the last SUCCESSFUL connection checked against Google, unlike allow_read_access/services which are only the stored request. stored_but_not_granted lists requested access missing from that last successful connection. A reconnect counts only when its browser callback says 'Gmail connected'. If the callback says 'Sign-in failed' (including a gog import error), the old granted_scopes remain visible: report that local callback failure and retry reconnect; do NOT infer a Google consent-screen scope problem from the stale scope list. If a successful reconnect still omits requested access, report the exact missing scope and investigate the returned grant, OAuth-client policy, organization policy, and consent restrictions. Do not claim that every scope must be manually registered in Google Cloud or that Google silently drops unregistered scopes; that is not a general requirement and must be supported by the concrete OAuth response before presenting it as the cause."

// registerGmailConnectionManagementTools gives the workflow/builder agent a
// chat-driven path to the same grant change the "Sending accounts" settings
// panel offers, so a user can say "increase my scope to include Drive"
// instead of opening the panel and clicking checkboxes themselves.
//
// This only ever changes STORED intent (GmailConnection.AllowReadAccess /
// AllowAgentWriteAccess / Services) and returns a reconnect link -- it never talks to Google or
// changes what the account can actually do. Google fixes a token's scope at
// consent time; there is no API to widen it after the fact. The agent must
// tell the user to open the link and reconnect, and should also open the
// Integrations panel (workflowWorkspaceViews "mcp", Gmail tab) so they can see the
// updated request and click Reconnect there too if the link doesn't suit.
// gmailToolScope is where Gmail tools registered for workspacePath act: a
// Code's own private accounts (as its owner, whose chats alone register these
// tools there), or the shared accounts everywhere else.
func gmailToolScope(workspacePath string) services.GmailUseScope {
	root := common.CodeProjectRoot("", workspacePath)
	if root == "" {
		return services.GmailUseScope{}
	}
	owner, _ := crewProjectOwnerID(root)
	return services.GmailUseScope{CodeWorkspace: root, UserID: owner}
}

func (api *StreamingAPI) registerGmailConnectionManagementTools(registrar definitionToolRegistrar, sessionID, workspacePath string) error {
	scope := gmailToolScope(workspacePath)
	serviceNames := services.GoogleServiceCatalog()
	serviceKeys := make([]string, 0, len(serviceNames))
	for key := range serviceNames {
		serviceKeys = append(serviceKeys, key)
	}
	if err := registrar.RegisterCustomTool(
		"list_gmail_connections",
		"List Gmail/Google-Workspace connections and, for each, both the STORED request (allow_read_access, allow_agent_write_access, services) and what Google has ACTUALLY granted (granted_scopes, from the last live check) -- plus a stored_but_not_granted diff that tells you exactly what to say when a user reports something isn't working ('why can't it write to Drive', 'is this connected'). Always check this before answering a scope/permission question or before calling update_gmail_connection_grants blind -- passing services without first reading the current list silently drops every service not repeated. "+gmailGrantMismatchNote,
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"connection_id": map[string]interface{}{"type": "string", "description": "Omit to list every connection."},
			},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			return api.listGmailConnectionsFromTool(ctx, args, scope)
		},
		"gmail_connection_management",
	); err != nil {
		return err
	}
	description := fmt.Sprintf(
		"Change what a Gmail connection is authorized for -- Gmail read access, agent draft/send/reply access, and/or Google Workspace services (%s) -- and get back a reconnect link for the user. "+
			"This only updates the STORED request; it does NOT change what Google has already granted. Google fixes a token's scope at the moment the user consents, "+
			"so after this call succeeds you MUST tell the user to open the returned reconnect_url and complete Google's consent screen -- the change has no effect until they do. "+
			"Also call perform_ui_action(action=\"open\", view=\"bots\") right after this so the Sending accounts panel is visible with the updated request. "+
			"Pass connection_id to target a specific account; omitted, the account's default connection is used. services replaces the full existing service list for this connection "+
			"(pass every service that should remain authorized, not just the one being added) -- omit it entirely to leave services unchanged.",
		strings.Join(serviceKeys, ", "),
	)
	params := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"connection_id": map[string]interface{}{"type": "string", "description": "Which Gmail connection to change. Omit to use the account's default connection."},
			"allow_read_access": map[string]interface{}{
				"type":        "boolean",
				"description": "Whether this connection may read/search the mailbox (gmail.readonly), in addition to the always-granted send. Omit to leave unchanged.",
			},
			"allow_agent_write_access": map[string]interface{}{
				"type":        "boolean",
				"description": "Whether agents may create Gmail drafts and send/reply (gmail.compose). Off by default. Omit to leave unchanged.",
			},
			"services": map[string]interface{}{
				"type": "array",
				"description": fmt.Sprintf(
					"Complete replacement list of Google Workspace services this connection should be authorized for, beyond Gmail. Omit entirely to leave services unchanged. Pass an empty array to remove every service grant, going back to Gmail-only. Each service is one of: %s.",
					strings.Join(serviceKeys, ", "),
				),
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"service": map[string]interface{}{"type": "string", "enum": serviceKeys},
						"write":   map[string]interface{}{"type": "boolean", "description": "true for read+write, false/omitted for read-only."},
					},
					"required": []string{"service"},
				},
			},
		},
	}
	if err := registrar.RegisterCustomTool("update_gmail_connection_grants", description, params, func(ctx context.Context, args map[string]interface{}) (string, error) {
		return api.updateGmailConnectionGrantsFromTool(ctx, sessionID, workspacePath, args, scope)
	}, "gmail_connection_management"); err != nil {
		return err
	}
	return api.registerGmailTriggerTools(registrar, sessionID, workspacePath)
}

func (api *StreamingAPI) updateGmailConnectionGrantsFromTool(ctx context.Context, sessionID, workspacePath string, args map[string]interface{}, scope services.GmailUseScope) (string, error) {
	svc := services.GetGmailService()
	if svc == nil {
		return "", fmt.Errorf("gmail is not configured on this deployment")
	}

	connectionID, _ := args["connection_id"].(string)
	connectionID = strings.TrimSpace(connectionID)
	conn, scopeErr := svc.ConnectionForScope(connectionID, scope)
	found := scopeErr == nil
	if !found {
		return "", fmt.Errorf("gmail connection %q not found; call list_gmail_connections or check the Sending accounts panel for valid IDs", connectionID)
	}

	input := services.GmailConnectionInput{}
	if raw, ok := args["allow_read_access"]; ok {
		v, ok := raw.(bool)
		if !ok {
			return "", fmt.Errorf("allow_read_access must be a boolean")
		}
		input.AllowReadAccess = &v
	}
	if raw, ok := args["allow_agent_write_access"]; ok {
		v, ok := raw.(bool)
		if !ok {
			return "", fmt.Errorf("allow_agent_write_access must be a boolean")
		}
		input.AllowAgentWriteAccess = &v
	}
	if raw, ok := args["services"]; ok {
		list, ok := raw.([]interface{})
		if !ok {
			return "", fmt.Errorf("services must be an array")
		}
		grants := make([]services.GoogleServiceGrant, 0, len(list))
		for _, item := range list {
			obj, ok := item.(map[string]interface{})
			if !ok {
				return "", fmt.Errorf("each services entry must be an object with a service field")
			}
			name, _ := obj["service"].(string)
			name = strings.TrimSpace(name)
			if name == "" {
				return "", fmt.Errorf("each services entry requires service")
			}
			write, _ := obj["write"].(bool)
			grants = append(grants, services.GoogleServiceGrant{Service: name, Write: write})
		}
		input.Services = grants
		input.ServicesSet = true
	}
	if input.AllowReadAccess == nil && input.AllowAgentWriteAccess == nil && !input.ServicesSet {
		return "", fmt.Errorf("pass allow_read_access, allow_agent_write_access, and/or services -- nothing to change")
	}

	updated, err := svc.UpdateConnection(ctx, conn.ID, input)
	if err != nil {
		return "", err
	}
	updated, err = svc.EnsureConnectionOAuthClient(ctx, updated.ID)
	if err != nil {
		return "", fmt.Errorf("saved the new request, but could not prepare this legacy connection for reconnect: %w", err)
	}

	redirectURI, err := gmailOAuthRedirectURIFromEnv(updated.ClientName)
	if err != nil {
		return "", fmt.Errorf("saved the new request, but could not build a reconnect link: %w. Tell the user to open the Sending accounts panel and click Reconnect themselves", err)
	}
	extraScopes := services.GoogleServiceScopeURIs(updated.Services)
	authURL, err := services.BeginGmailOAuth(updated.ID, updated.ClientName, redirectURI, updated.AllowReadAccess, updated.AllowAgentWriteAccess, extraScopes)
	if err != nil {
		return "", fmt.Errorf("saved the new request, but could not start the reconnect flow: %w. Tell the user to open the Sending accounts panel and click Reconnect themselves", err)
	}

	if event, viewErr := workspaceViewPresentation("mcp", workspacePath); viewErr == nil {
		api.emitAgentProfileEvent(sessionID, event)
	}

	response := map[string]interface{}{
		"connection_id":            updated.ID,
		"display_name":             updated.DisplayName,
		"allow_read_access":        updated.AllowReadAccess,
		"allow_agent_write_access": updated.AllowAgentWriteAccess,
		"services":                 updated.Services,
		"reconnect_url":            authURL,
		"note":                     "The stored request is saved, but Google has not granted anything new yet. The user must open reconnect_url and complete Google's consent screen for this to take effect.",
	}
	encoded, encodeErr := json.Marshal(response)
	if encodeErr != nil {
		return "", fmt.Errorf("encode result: %w", encodeErr)
	}
	return string(encoded), nil
}

type gmailServiceGrantStatus struct {
	Service string `json:"service"`
	Write   bool   `json:"write_requested"`
	Scope   string `json:"scope"`
	Granted bool   `json:"granted_by_google"`
}

type gmailConnectionStatusReport struct {
	ID                       string                    `json:"id"`
	DisplayName              string                    `json:"display_name"`
	Email                    string                    `json:"email,omitempty"`
	Status                   string                    `json:"status,omitempty"`
	Enabled                  bool                      `json:"enabled"`
	GmailReadRequested       bool                      `json:"gmail_read_requested"`
	GmailReadGranted         bool                      `json:"gmail_read_granted_by_google"`
	GmailAgentWriteRequested bool                      `json:"gmail_agent_write_requested"`
	GmailAgentWriteGranted   bool                      `json:"gmail_agent_write_granted_by_google"`
	Services                 []gmailServiceGrantStatus `json:"services,omitempty"`
	GrantedScopes            []string                  `json:"granted_scopes,omitempty"`
	StoredButNotGranted      []string                  `json:"stored_but_not_granted,omitempty"`
}

func (api *StreamingAPI) listGmailConnectionsFromTool(_ context.Context, args map[string]interface{}, scope services.GmailUseScope) (string, error) {
	svc := services.GetGmailService()
	if svc == nil {
		return "", fmt.Errorf("gmail is not configured on this deployment")
	}

	connectionID, _ := args["connection_id"].(string)
	connectionID = strings.TrimSpace(connectionID)

	var conns []services.GmailConnection
	if connectionID != "" {
		conn, found := svc.GetConnection(connectionID)
		if !found || !conn.UsableFrom(scope) {
			return "", fmt.Errorf("gmail connection %q not found", connectionID)
		}
		conns = []services.GmailConnection{conn}
	} else {
		conns = svc.ConnectionsUsableFrom(scope)
	}

	reports := make([]gmailConnectionStatusReport, 0, len(conns))
	for _, conn := range conns {
		report := gmailConnectionStatusReport{
			ID:                       conn.ID,
			DisplayName:              conn.DisplayName,
			Email:                    conn.Email,
			Status:                   string(conn.Status),
			Enabled:                  conn.Enabled,
			GmailReadRequested:       conn.AllowReadAccess,
			GmailReadGranted:         services.GoogleScopesGrant(conn.Scopes, services.GmailReadonlyScope),
			GmailAgentWriteRequested: conn.AllowAgentWriteAccess,
			GmailAgentWriteGranted:   services.GoogleScopesGrant(conn.Scopes, services.GmailComposeScope),
			GrantedScopes:            conn.Scopes,
		}
		if conn.AllowReadAccess && !report.GmailReadGranted {
			report.StoredButNotGranted = append(report.StoredButNotGranted,
				"Gmail: read access requested but Google has not granted gmail.readonly yet")
		}
		if conn.AllowAgentWriteAccess && !report.GmailAgentWriteGranted {
			report.StoredButNotGranted = append(report.StoredButNotGranted,
				"Gmail: agent draft/send/reply access requested but Google has not granted gmail.compose yet")
		}
		for _, g := range conn.Services {
			scope, display, ok := services.GoogleServiceScopeURI(g.Service, g.Write)
			if !ok {
				continue
			}
			gs := gmailServiceGrantStatus{Service: g.Service, Write: g.Write, Scope: scope, Granted: services.GoogleScopesGrant(conn.Scopes, scope)}
			report.Services = append(report.Services, gs)
			if !gs.Granted {
				level := "read-only"
				if g.Write {
					level = "write"
				}
				report.StoredButNotGranted = append(report.StoredButNotGranted,
					fmt.Sprintf("%s: %s access requested (%s) but Google has not granted it yet", display, level, scope))
			}
		}
		reports = append(reports, report)
	}

	encoded, err := json.Marshal(map[string]interface{}{
		"connections": reports,
		"note":        gmailGrantMismatchNote,
	})
	if err != nil {
		return "", fmt.Errorf("encode result: %w", err)
	}
	return string(encoded), nil
}
