package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
)

// Personal Slack lives in the person's own vault (owner, 2026-10-21: one store, Vault only). One Slack account per
// person (owner, 2026-10-16) in a vault they own and nobody else belongs to. A project "uses" it by selecting that
// connection (vault_<id>) like any other Vault MCP; a Crew or Goal then lends its owner's selection to everyone who uses
// it, and the Vault proxy keeps the lent Slack to its send side for them (lentVaultSlackRefusal).

const personalVaultName = "My accounts"

type personalVaultList struct {
	Vaults []struct {
		Group struct {
			ID   string
			Name string
		} `json:"group"`
		Role        string `json:"role"`
		Connections []struct {
			ID       string `json:"id"`
			Label    string `json:"label"`
			Provider string `json:"provider"`
			Status   string `json:"status"`
		} `json:"connections"`
	} `json:"vaults"`
}

func personalVaults(ctx context.Context, person string) (personalVaultList, error) {
	var list personalVaultList
	data, err := vaultPersonRequest(ctx, person, http.MethodGet, "/api/vaults", nil)
	if err != nil {
		return list, err
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return list, errors.New("Your vaults could not be read.")
	}
	return list, nil
}

// ensurePersonalVault returns the vault that holds the person's own accounts, creating it the first time.
func ensurePersonalVault(ctx context.Context, person string) (string, error) {
	list, err := personalVaults(ctx, person)
	if err != nil {
		return "", err
	}
	for _, v := range list.Vaults {
		if v.Role == "owner" && v.Group.Name == personalVaultName {
			return v.Group.ID, nil
		}
	}
	payload, _ := json.Marshal(map[string]string{"Name": personalVaultName, "Description": "Your own accounts, used only by you and the projects you build."})
	data, err := vaultPersonRequest(ctx, person, http.MethodPost, "/api/vaults", payload)
	if err != nil {
		return "", err
	}
	var made struct{ ID string }
	if json.Unmarshal(data, &made) != nil || made.ID == "" {
		return "", errors.New("Your vault was created but returned no ID.")
	}
	return made.ID, nil
}

type personalSlackConnection struct {
	VaultID string
	ID      string
	Status  string
}

// ownVaultSlack finds the person's Slack connection in a vault they own, preferring a signed-in one.
func ownVaultSlack(ctx context.Context, person string) (personalSlackConnection, bool, error) {
	list, err := personalVaults(ctx, person)
	if err != nil {
		return personalSlackConnection{}, false, err
	}
	var found personalSlackConnection
	ok := false
	for _, v := range list.Vaults {
		if v.Role != "owner" {
			continue
		}
		for _, c := range v.Connections {
			if !strings.EqualFold(c.Provider, "slack") {
				continue
			}
			if !ok || c.Status == "active" && found.Status != "active" {
				found, ok = personalSlackConnection{VaultID: v.Group.ID, ID: c.ID, Status: c.Status}, true
			}
		}
	}
	return found, ok, nil
}

// projectVaultServers is the project's selected servers, from its own manifest (a Crew's runtime manifest, a Goal's
// workflow.json).
func projectSelectedServers(ctx context.Context, workspace string) ([]string, error) {
	if isProjectWorkspacePath(workspace) {
		servers, _, err := productSelectedServers(ctx, "work", workspace)
		return servers, err
	}
	manifest, exists, err := ReadWorkflowManifest(ctx, workspace)
	if err != nil || !exists || manifest == nil {
		return nil, errors.New("This item could not be read.")
	}
	return append([]string{}, manifest.Capabilities.SelectedServers...), nil
}

// setProjectSelectedServers rewrites the project's selected servers through the same writers the app's panels use.
func (api *StreamingAPI) setProjectSelectedServers(ctx context.Context, workspace string, mutate func([]string) []string) error {
	if isProjectWorkspacePath(workspace) {
		return updateProductSelectedServers(ctx, "work", workspace, mutate)
	}
	manifest, exists, err := ReadWorkflowManifest(ctx, workspace)
	if err != nil || !exists || manifest == nil {
		return errors.New("This item could not be read.")
	}
	caps := manifest.Capabilities
	caps.SelectedServers = mutate(append([]string{}, caps.SelectedServers...))
	body, _ := json.Marshal(UpdateWorkflowManifestRequest{WorkspacePath: workspace, Capabilities: &caps})
	save := httptest.NewRequest(http.MethodPut, "/api/workflows/manifest", bytes.NewReader(body)).WithContext(ctx)
	save.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	requireWorkflowWriteAccess(api.handleUpdateWorkflowManifest)(recorder, save)
	if recorder.Code != http.StatusOK {
		return fmt.Errorf("The item's settings were not saved: %s", strings.TrimSpace(recorder.Body.String()))
	}
	return nil
}

// selectVaultServer adds or removes one Vault connection from the project. A project that picks no Vault connection uses
// every one its owner may use; the first pick would silently drop all the others, so it keeps them picked alongside.
func (api *StreamingAPI) selectVaultServer(ctx context.Context, person, workspace, connectionID string, on bool) error {
	name := vaultServerName(connectionID)
	var keep []string
	if on {
		if current, err := projectSelectedServers(ctx, workspace); err == nil && !slices.ContainsFunc(current, func(s string) bool { return strings.HasPrefix(s, "vault_") }) {
			if rows, err := vaultServersFor(ctx, person); err == nil {
				for _, row := range rows {
					keep = append(keep, vaultServerName(row.ID))
				}
			}
		}
	}
	return api.setProjectSelectedServers(ctx, workspace, func(current []string) []string {
		if !on {
			return settingsWithout(current, []string{name})
		}
		return settingsWith(settingsWith(current, keep...), name)
	})
}

func (api *StreamingAPI) personalSlackAccounts(r *http.Request, workspace string) (slackAccountsResponse, error) {
	out := slackAccountsResponse{Accounts: []slackAccountRow{}, Notice: slackAccountNotice}
	ctx := r.Context()
	actor := personalConnectionActor(ctx)
	if actor == "" || !canManagePersonalConnection(r, actor) {
		return out, fmt.Errorf("Sign in to manage your Slack accounts in My Vault.")
	}
	if workspace == "" {
		if err := personalAccountManagementScope(r, workspace, false); err != nil {
			return out, err
		}
	}
	_, buildErr := personalConnectionItemScope(r, workspace, true, "Slack", "My Vault")
	out.CanConnect = personalAccountManagementScope(r, workspace, true) == nil && personVaultActive(actor)
	out.SignInReady = api.slackSignInReady()
	selectedNames := map[string]bool{}
	if workspace != "" {
		if servers, err := projectSelectedServers(ctx, workspace); err == nil {
			for _, s := range servers {
				selectedNames[s] = true
			}
		}
	}
	row := func(owner, label string, c personalSlackConnection) slackAccountRow {
		selected := selectedNames[vaultServerName(c.ID)]
		out := slackAccountRow{personalConnectionPermissions: personalConnectionAccess(r, owner, workspace, selected, "Slack", "My Vault"),
			ID: c.ID, Owner: owner, Label: label, Selected: selected, Connected: c.Status == "active", Mine: owner == actor}
		if c.Status != "active" {
			if outcome, ok := vaultSignInOutcomes.Load(c.ID); ok {
				if o := outcome.(vaultSignInOutcome); !o.ok {
					out.Problem = o.reason
				}
			}
		}
		return out
	}
	if own, ok, err := ownVaultSlack(ctx, actor); err != nil {
		return out, err
	} else if ok {
		out.Accounts = append(out.Accounts, row(actor, "Your Slack", own))
	}
	// A project's Builder also sees the Slack its owner lent to it.
	if workspace != "" && buildErr == nil {
		if lender := api.projectSecretsOwner(actor, workspace); lender != "" && lender != actor {
			if lent, ok, err := ownVaultSlack(ctx, lender); err == nil && ok && selectedNames[vaultServerName(lent.ID)] {
				out.Accounts = append(out.Accounts, row(lender, shareOwnerLabel(lender)+"'s Slack", lent))
			}
		}
	}
	return out, nil
}

// signInPrompt starts the Slack sign-in of one of the person's own vault connections and says what to do next.
func (api *StreamingAPI) personalSlackSignIn(r *http.Request, actor, connectionID string) (map[string]any, error) {
	ctx := r.Context()
	c, err := vaultConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	start, discovery, err := api.beginVaultConnectionOAuth(ctx, actor, chatSessionIDFromContext(ctx), c.ID, c.OAuthServer, deriveOAuthRedirectURI(r), "", "")
	if err != nil {
		return nil, err
	}
	if discovery != nil {
		return map[string]any{"connection_id": connectionID, "status": discovery.Status, "message": "Ask an administrator to set up the Slack sign-in app in My Vault. Then choose Sign in again. " + discovery.Message, "notice": slackAccountNotice}, nil
	}
	return map[string]any{"connection_id": connectionID, "auth_url": start.AuthURL, "next": "Open this link and sign in with Slack, then return here. This account is used only in the items you explicitly add it to.", "notice": slackAccountNotice}, nil
}

// One operation path is used by the app and the native Builder/MCP tool.
func (api *StreamingAPI) personalSlackOperation(r *http.Request, workspace string, args map[string]interface{}) (any, error) {
	ctx := r.Context()
	actor := personalConnectionActor(ctx)
	if actor == "" || !canManagePersonalConnection(r, actor) {
		return nil, fmt.Errorf("Sign in to manage your Slack accounts in My Vault.")
	}
	action, _ := args["action"].(string)
	if action == "list_saved" {
		return api.personalSlackAccounts(r, workspace)
	}
	if action != "detach" && (workspace == "" || action == "connect") {
		if err := personalAccountManagementScope(r, workspace, action == "connect"); err != nil {
			return nil, err
		}
	}
	if !personVaultActive(actor) {
		return nil, fmt.Errorf("Your account cannot own a vault, so it cannot connect Slack. Ask an administrator.")
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
		if err := personalAccountManagementScope(r, workspace, true); err != nil {
			return nil, err
		}
		// One Slack account per person (owner, 2026-10-16): Connect reuses the saved one, so repeated clicks never pile
		// up empty accounts.
		own, found, err := ownVaultSlack(ctx, actor)
		if err != nil {
			return nil, err
		}
		if !found {
			vaultID, err := ensurePersonalVault(ctx, actor)
			if err != nil {
				return nil, err
			}
			label, _ := args["label"].(string)
			if strings.TrimSpace(label) == "" {
				label = "My Slack account"
			}
			connectionID, err := createOwnVaultConnection(ctx, actor, vaultID, placeMCPServer{URL: "https://mcp.slack.com/mcp", Label: label})
			if err != nil {
				return nil, err
			}
			own = personalSlackConnection{VaultID: vaultID, ID: connectionID, Status: "authentication_required"}
		}
		id = own.ID
		if workspace != "" {
			if err := api.selectVaultServer(ctx, actor, workspace, id, true); err != nil {
				return nil, err
			}
		}
		log.Printf("[SLACK_ACCOUNTS] actor=%s account=%s owner=%s item=%s reason=connect vault=%s signed_in=%t", actor, id, actor, root, own.VaultID, own.Status == "active")
		if own.Status == "active" {
			return map[string]any{"connection_id": id, "selected": workspace != "", "message": "Your saved Slack account is connected.", "notice": slackAccountNotice}, nil
		}
		return api.personalSlackSignIn(r, actor, id)
	}
	own, found, err := ownVaultSlack(ctx, owner)
	if err != nil {
		return nil, err
	}
	if !found || own.ID != id {
		return nil, fmt.Errorf("Choose a saved Slack account from this item's Slack settings.")
	}
	selected := false
	if workspace != "" {
		if servers, err := projectSelectedServers(ctx, workspace); err == nil {
			selected = slices.Contains(servers, vaultServerName(id))
		}
	}
	permissions := personalConnectionAccess(r, owner, workspace, selected, "Slack", "My Vault")
	switch action {
	case "attach":
		if !permissions.CanAdd {
			return nil, fmt.Errorf("Add your own saved Slack account to an item you build, from My Vault.")
		}
		if err := api.selectVaultServer(ctx, actor, workspace, id, true); err != nil {
			return nil, err
		}
	case "detach":
		if !permissions.CanDetach {
			return nil, fmt.Errorf("Ask this item's Builder or the account owner to remove it in My Vault.")
		}
		if err := api.selectVaultServer(ctx, actor, workspace, id, false); err != nil {
			return nil, err
		}
	case "disconnect":
		if !permissions.CanManage {
			return nil, fmt.Errorf("Ask the account owner or an administrator to disconnect this saved Slack account.")
		}
		// Removing the connection makes every item that selected it stop resolving it.
		if _, err := vaultPersonRequest(ctx, owner, http.MethodDelete, "/api/vaults/"+url.PathEscape(own.VaultID)+"/connectors/"+url.PathEscape(id), nil); err != nil {
			return nil, err
		}
		mutex := platformMCPOAuthMutex("vault:" + id)
		mutex.Lock()
		advanceVaultOAuthGeneration(id)
		removeVaultCredentialFiles(id)
		mutex.Unlock()
	case "reconnect":
		if !permissions.CanReconnect {
			return nil, fmt.Errorf("Only the account owner can sign in again. Ask them to reconnect this saved Slack account in My Vault.")
		}
		return api.personalSlackSignIn(r, actor, id)
	default:
		return nil, fmt.Errorf("Choose list_saved, connect, attach, detach, reconnect or disconnect.")
	}
	log.Printf("[SLACK_ACCOUNTS] actor=%s account=%s owner=%s item=%s reason=%s", actor, id, owner, root, action)
	return map[string]any{"connection_id": id, "selected": action == "attach", "notice": slackAccountNotice}, nil
}
