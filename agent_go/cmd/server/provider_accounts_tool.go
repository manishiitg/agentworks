package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"
)

// ai_accounts: the AI accounts screen as one tool, for MCP and for the project's own chat. Every action goes through
// the screen's own handler with the caller's identity, so the same ownership and admin rules apply; no login or key
// value is ever returned. Signing in still happens in the person's browser.
const providerAccountsTool = "ai_accounts"

func providerAccountsToolDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add(providerAccountsTool, "Your AI accounts (Claude, Codex, Cursor, Muse, Pi and the others), as on the AI accounts screen. "+
		"list: every account you can see, with whose it is, how it signs in, whether it is set up, who it is signed in as, its health (Ready, Sign-in needed, Limit reached), and (on the Mac app) your connected servers. "+
		"status: run the account's free sign-in check now (verify=true also sends one small model request; managers only). "+
		"sign_in: start the sign-in; returns a link (and code) for the person to approve in their browser. "+
		"sign_out: sign the account's program out; the account stays. remove: delete one of your own accounts. "+
		"add_on_server (Mac app): add one of your accounts on this Mac to your private accounts on a connected server; a key is copied, a browser login needs one sign-in there (the returned link). "+
		"move_to_server (Mac app, Codex browser login): move the login to your server so the Mac and your online agents share one login. "+
		"You can change only your own accounts; the company (server) accounts need an administrator. Use the account id from list.", true, false, map[string]any{
		"action":     map[string]any{"type": "string", "enum": []any{"list", "status", "sign_in", "sign_out", "remove", "add_on_server", "move_to_server"}},
		"account_id": externalString("Account id from list; required except for list."),
		"server":     externalString("add_on_server/move_to_server: server id from list (servers); optional when only one is connected."),
		"verify":     map[string]any{"type": "boolean", "description": "status: also send one small model request to prove the login works."},
	}, "action")
}

// providerAccountsToolAllowed: any signed-in person manages their own accounts. A token bound to an agent never does;
// other tokens need crews:read to look and crews:write to change.
func providerAccountsToolAllowed(c *UserClaims) bool {
	if c == nil || c.Scope != "" {
		return false
	}
	return c.AccessToken == nil || c.AccessToken.BoundAgentID == "" && (c.AccessToken.Allows("crews:read") || c.AccessToken.Allows("crews:write"))
}

func (api *StreamingAPI) externalProviderAccountsCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if !providerAccountsToolAllowed(claims) {
		externalError(w, http.StatusForbidden, "forbidden", "This connection cannot manage AI accounts. Use AI accounts in Settings.")
		return
	}
	action := externalArg(args, "action")
	id := strings.TrimSpace(externalArg(args, "account_id"))
	if action != "list" && action != "status" && claims.AccessToken != nil && !claims.AccessToken.Allows("crews:write") {
		externalError(w, http.StatusForbidden, "forbidden", "This connection can look at AI accounts but not change them. Use AI accounts in Settings.")
		return
	}
	if action != "list" && id == "" {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "Give account_id from ai_accounts action=list.")
		return
	}
	vars := map[string]string{"connectionID": id}
	path := "/api/provider-connections/" + url.PathEscape(id)
	var status int
	var body string
	switch action {
	case "list":
		api.providerAccountsList(w, r)
		return
	case "status":
		verify := ""
		if v, _ := args["verify"].(bool); v {
			verify = "?verify=1"
		}
		status, body = api.providerAccountsForward(r, http.MethodGet, path+"/status"+verify, vars, nil, api.handleProviderAccountStatus)
	case "sign_in":
		if _, ok := ownServerLoginAccountByID(r.Context(), claims.UserID, id); ok {
			status, body = api.providerAccountsForward(r, http.MethodPost, path+"/server-login", vars, map[string]string{"action": "sign_in"}, api.handleServerLogin)
			break
		}
		provider := ""
		if target, err := api.resolveProviderAccountTarget(r.Context(), id, ""); err == nil {
			provider = target.Provider
		}
		link := getBaseURL(r) + "/settings/ai-accounts?" + url.Values{"provider": {provider}, "sign_in": {id}}.Encode()
		externalJSON(w, map[string]any{"sign_in_url": link, "next": "Open the link and approve the sign-in in your browser; the account is ready once it shows signed in. On the Mac app, open AI accounts in Settings and press Sign in on this account."})
		return
	case "sign_out":
		status, body = api.providerAccountsForward(r, http.MethodPost, path+"/sign-out", vars, nil, api.handleProviderAccountSignOut)
	case "remove":
		status, body = api.providerAccountsForward(r, http.MethodDelete, path, vars, nil, api.handleProviderConnection)
	case "add_on_server", "move_to_server":
		server, err := providerAccountsServer(claims.UserID, externalArg(args, "server"))
		if err != nil {
			externalError(w, http.StatusBadRequest, "invalid_arguments", err.Error())
			return
		}
		if action == "move_to_server" {
			status, body = api.providerAccountsForward(r, http.MethodPost, path+"/move-to-server", vars, map[string]string{"server": server}, api.handleMoveLoginToServer)
			break
		}
		status, body = api.providerAccountsForward(r, http.MethodPost, "/api/desktop-connections/"+url.PathEscape(server)+"/provider-accounts", nil, map[string]string{"account_id": id}, func(w http.ResponseWriter, r *http.Request) {
			api.copyProviderAccountToServer(w, r, server)
		})
	default:
		externalError(w, http.StatusBadRequest, "invalid_arguments", "Use list, status, sign_in, sign_out, remove, add_on_server or move_to_server.")
		return
	}
	if status >= 300 {
		externalError(w, status, "refused", laptopActionErrorText(body))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

// providerAccountsForward runs one AI accounts handler as the caller, keeping any query string.
func (api *StreamingAPI) providerAccountsForward(r *http.Request, method, target string, vars map[string]string, payload any, handler http.HandlerFunc) (int, string) {
	path, query, _ := strings.Cut(target, "?")
	status, body := api.externalForward(r, method, path, vars, payload, func(w http.ResponseWriter, sub *http.Request) {
		sub.URL.RawQuery = query
		handler(w, sub)
	})
	if status == 0 {
		status = http.StatusOK
	}
	return status, body
}

// providerAccountsServer picks the connected server for add/move: the one named, or the only one.
func providerAccountsServer(owner, named string) (string, error) {
	if desktopBootstrapHost == "" {
		return "", fmt.Errorf("Only the Mac app adds or moves an account to a server. On the server, add the account in AI accounts there.")
	}
	desktopConnectionsMu.Lock()
	connections, err := readDesktopConnections(owner)
	desktopConnectionsMu.Unlock()
	if err != nil || len(connections) == 0 {
		return "", fmt.Errorf("This Mac is not connected to a server yet. Connect one in Settings, then try again.")
	}
	for _, c := range connections {
		if c.ID == named || strings.EqualFold(c.Name, named) {
			return c.ID, nil
		}
	}
	if named == "" && len(connections) == 1 {
		return connections[0].ID, nil
	}
	return "", fmt.Errorf("Choose a server: give server as one of the ids in ai_accounts action=list (servers).")
}

// providerAccountsList is the screen's list in plain fields, plus which accounts need a new sign-in and, on the Mac
// app, the connected servers an account can be added to.
func (api *StreamingAPI) providerAccountsList(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	views, err := api.listProviderAccountViews(r.Context(), claims.UserID, currentUserIsAdmin(r), providerAccountScope{Principal: claims.UserID})
	if err != nil {
		externalError(w, http.StatusInternalServerError, "unavailable", "Cannot read the AI accounts right now. Try again.")
		return
	}
	_, limitsBody := api.externalForward(r, http.MethodGet, "/api/provider-accounts/sign-in-needed", nil, nil, api.handleAccountSignInNeeded)
	var limits struct {
		Accounts []accountLimitsView `json:"accounts"`
	}
	_ = json.Unmarshal([]byte(limitsBody), &limits)
	health := map[string]accountLimitsView{}
	for _, view := range limits.Accounts {
		health[view.AccountID] = view
	}
	accounts := []map[string]any{}
	for _, v := range views {
		if v.Relation == "server" && !v.Usable && !v.CanManage {
			continue
		}
		row := map[string]any{"id": v.ID, "program": providerAccountProductName(v.Provider), "provider": v.Provider, "name": v.DisplayName,
			"whose": v.Relation, "signs_in_with": v.AuthMethod, "can_use": v.Usable, "can_manage": v.CanManage}
		if v.Configured != nil {
			row["set_up"] = *v.Configured
		}
		if v.Identity != "" {
			row["signed_in_as"] = v.Identity
		}
		if v.OwnerName != "" && v.Relation != "own" {
			row["owner"] = v.OwnerName
		}
		if v.ViaConnection != "" {
			row["runs_through_server"] = v.ViaConnection
		}
		if v.OwnOnServer {
			row["own_login_on_server"] = true
		}
		if h, ok := health[v.ID]; ok {
			row["health"] = h.Status
			if h.SignIn != nil && h.SignIn.Needed {
				row["needs_sign_in"] = true
				if h.SignIn.Reason != "" {
					row["sign_in_problem"] = h.SignIn.Reason
				}
			}
			if h.Billing != "" {
				row["billing"] = h.Billing
			}
		}
		accounts = append(accounts, row)
	}
	result := map[string]any{"accounts": accounts, "note": "whose: own = yours, server = the company account on this installation (on the Mac app, This Mac's own login), shared_* = shared with you."}
	if desktopBootstrapHost != "" {
		desktopConnectionsMu.Lock()
		connections, _ := readDesktopConnections(claims.UserID)
		desktopConnectionsMu.Unlock()
		servers := []map[string]string{}
		for _, c := range connections {
			servers = append(servers, map[string]string{"id": c.ID, "name": c.Name})
		}
		result["servers"] = servers
	}
	externalJSON(w, result)
}

// registerProviderAccountsChatTool gives the project's own chat the same ai_accounts tool, for the person in it.
func (api *StreamingAPI) registerProviderAccountsChatTool(reg definitionToolRegistrar, gate *productToolGate, claims *UserClaims, readOnly bool, baseURL string) error {
	if claims == nil || claims.UserID == "" || readOnly {
		return nil
	}
	var description string
	var props map[string]any
	providerAccountsToolDefinition(func(_ string, d string, _ bool, _ bool, p map[string]any, _ ...string) {
		description, props = d, p
	})
	gate.Declare(providerAccountsTool)
	return reg.RegisterCustomToolWithTimeout(providerAccountsTool, description, map[string]any{"type": "object", "properties": props, "required": []string{"action"}}, func(ctx context.Context, args map[string]any) (string, error) {
		body, _ := json.Marshal(args)
		r := httptest.NewRequest(http.MethodPost, baseURL+"/", bytes.NewReader(body)).WithContext(context.WithValue(ctx, UserContextKey, claims))
		w := httptest.NewRecorder()
		api.externalProviderAccountsCall(w, r, args)
		if w.Code >= 300 {
			return "", fmt.Errorf("%s", laptopActionErrorText(w.Body.String()))
		}
		return w.Body.String(), nil
	}, 2*time.Minute, "workspace_advanced")
}
