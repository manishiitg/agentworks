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

// ensurePersonalVault returns the person's one personal vault; the Vault service creates it the first time.
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
	data, err := vaultPersonRequest(ctx, person, http.MethodPost, "/api/vaults", []byte("{}"))
	if err != nil {
		return "", err
	}
	var made struct{ ID string }
	if json.Unmarshal(data, &made) != nil || made.ID == "" {
		return "", errors.New("Your Vault returned no ID.")
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

// projectAppSelection is the project's saved app list and its "chosen Vault apps only" setting, from its own manifest
// (a Crew's runtime manifest, a Goal's workflow.json).
func projectAppSelection(ctx context.Context, workspace string) ([]string, bool, error) {
	if isProjectWorkspacePath(workspace) {
		return productAppSelection(ctx, "work", workspace)
	}
	manifest, exists, err := ReadWorkflowManifest(ctx, workspace)
	if err != nil || !exists || manifest == nil {
		return nil, false, errors.New("This item could not be read.")
	}
	return savedAppSelection(manifest.Capabilities.SelectedServers), manifest.Capabilities.ChosenVaultAppsOnly, nil
}

// projectSelectedServers is the project's app list as the agent runtime takes it (runtimeAppSelection).
func projectSelectedServers(ctx context.Context, workspace string) ([]string, error) {
	servers, chosenOnly, err := projectAppSelection(ctx, workspace)
	if err != nil {
		return nil, err
	}
	return runtimeAppSelection(servers, chosenOnly), nil
}

// projectVaultAppInUse reports whether a project uses one Vault app: all of them when "Use all my apps
// automatically" is on, else only the chosen ones.
func projectVaultAppInUse(servers []string, chosenOnly bool, connectionID string) bool {
	return !chosenOnly || containsExactString(servers, vaultServerName(connectionID))
}

// setProjectSelectedServers rewrites the project's saved app list through the same writers the app's panels use;
// chosenOnly, when given, sets "Use all my apps automatically" off (true) or on (false).
func (api *StreamingAPI) setProjectSelectedServers(ctx context.Context, workspace string, mutate func([]string) []string, chosenOnly ...bool) error {
	if isProjectWorkspacePath(workspace) {
		return updateProductSelectedServers(ctx, "work", workspace, mutate, chosenOnly...)
	}
	manifest, exists, err := ReadWorkflowManifest(ctx, workspace)
	if err != nil || !exists || manifest == nil {
		return errors.New("This item could not be read.")
	}
	caps := manifest.Capabilities
	caps.SelectedServers = savedAppSelection(mutate(savedAppSelection(caps.SelectedServers)))
	if len(chosenOnly) > 0 {
		caps.ChosenVaultAppsOnly = chosenOnly[0]
	}
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

// selectVaultServer adds or removes one Vault connection from the project. A project using all its owner's Vault apps
// switches to chosen apps only with every app in use kept, so one change never silently drops the others.
func (api *StreamingAPI) selectVaultServer(ctx context.Context, person, workspace, connectionID string, on bool) error {
	if !canManageProjectApps(ctx, person, workspace) {
		return errors.New("only a current Builder can change project connections")
	}
	name := vaultServerName(connectionID)
	current, chosenOnly, err := projectAppSelection(ctx, workspace)
	if err != nil {
		return err
	}
	owner := api.projectSecretsOwner(person, workspace)
	rows, err := vaultServersFor(ctx, owner)
	if err != nil {
		return err
	}
	available := []string{}
	allowed := false
	for _, row := range rows {
		n := vaultServerName(row.ID)
		available = append(available, n)
		if n == name {
			allowed = true
		}
	}
	if on && !allowed { // Pending accounts can be selected by their owner before sign-in.
		c, e := vaultSavedConnection(ctx, connectionID)
		if e != nil || !vaultOwnerOf(ctx, person, c.VaultID) {
			return errors.New("this Vault connection is unavailable")
		}
	}
	picked := []string{}
	others := []string{}
	for _, n := range current {
		if strings.HasPrefix(n, "vault_") {
			picked = append(picked, n)
		} else {
			others = append(others, n)
		}
	}
	// Changing one Vault app by hand means chosen apps only, starting from every app in use now.
	if !chosenOnly {
		picked = available
	}
	picked = settingsWithout(picked, []string{name})
	if on {
		picked = settingsWith(picked, name)
	}
	return api.setProjectSelectedServers(ctx, workspace, func([]string) []string { return append(others, picked...) }, true)

}

func (api *StreamingAPI) personalSlackAccounts(r *http.Request, workspace string) (slackAccountsResponse, error) {
	out := slackAccountsResponse{Accounts: []slackAccountRow{}, Notice: slackAccountNotice}
	ctx := r.Context()
	actor := personalConnectionActor(ctx)
	if actor == "" || !canManagePersonalConnection(r, actor) {
		return out, fmt.Errorf("Sign in to manage your Slack accounts in your project's Vault panel or in chat.")
	}
	if workspace == "" {
		if err := personalAccountManagementScope(r, workspace, false); err != nil {
			return out, err
		}
	}
	_, buildErr := personalConnectionItemScope(r, workspace, true, "Slack", "your project's Vault panel")
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
		out := slackAccountRow{personalConnectionPermissions: personalConnectionAccess(r, owner, workspace, selected, "Slack", "your project's Vault panel"),
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
		return map[string]any{"connection_id": connectionID, "status": discovery.Status, "message": "Ask an administrator to set up the Slack sign-in app in the Company Vault. Then choose Sign in again. " + discovery.Message, "notice": slackAccountNotice}, nil
	}
	return map[string]any{"connection_id": connectionID, "auth_url": start.AuthURL, "next": "Open this link and sign in with Slack, then return here. This account is used only in the items you explicitly add it to.", "notice": slackAccountNotice}, nil
}

// One operation path is used by the app and the native Builder/MCP tool.
func (api *StreamingAPI) personalSlackOperation(r *http.Request, workspace string, args map[string]interface{}) (any, error) {
	ctx := r.Context()
	actor := personalConnectionActor(ctx)
	if actor == "" || !canManagePersonalConnection(r, actor) {
		return nil, fmt.Errorf("Sign in to manage your Slack accounts in your project's Vault panel or in chat.")
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
			data, err := vaultPersonRequest(ctx, actor, http.MethodPost, "/api/vaults/"+url.PathEscape(vaultID)+"/connectors", vaultPromoteJSON(map[string]string{"Provider": "Slack", "Label": label}))
			var created struct{ ID string }
			if err == nil && (json.Unmarshal(data, &created) != nil || !vaultConnectionID.MatchString(created.ID)) {
				err = errors.New("Vault returned no connection ID")
			}
			connectionID := created.ID
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
	permissions := personalConnectionAccess(r, owner, workspace, selected, "Slack", "your project's Vault panel")
	switch action {
	case "attach":
		if !permissions.CanAdd {
			return nil, fmt.Errorf("Add your own saved Slack account to an item you build, from your project's Vault panel.")
		}
		if err := api.selectVaultServer(ctx, actor, workspace, id, true); err != nil {
			return nil, err
		}
	case "detach":
		if !permissions.CanDetach {
			return nil, fmt.Errorf("Ask this item's Builder or the account owner to remove it in your project's Vault panel or in chat.")
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
			return nil, fmt.Errorf("Only the account owner can sign in again. Ask them to reconnect this saved Slack account in your project's Vault panel or in chat.")
		}
		return api.personalSlackSignIn(r, actor, id)
	default:
		return nil, fmt.Errorf("Choose list_saved, connect, attach, detach, reconnect or disconnect.")
	}
	log.Printf("[SLACK_ACCOUNTS] actor=%s account=%s owner=%s item=%s reason=%s", actor, id, owner, root, action)
	return map[string]any{"connection_id": id, "selected": action == "attach", "notice": slackAccountNotice}, nil
}
