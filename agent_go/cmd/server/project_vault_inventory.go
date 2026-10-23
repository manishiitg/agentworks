package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
)

// The panel and chat read the same names-only snapshot. Selection still uses
// the existing settings, personal-account and laptop operations.
type projectVaultItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Provider  string `json:"provider,omitempty"`
	Owner     string `json:"owner,omitempty"`
	Status    string `json:"status"`
	Selected  bool   `json:"selected"`
	CanSelect bool   `json:"can_select"`
}

// One checkbox edits one existing setting through the same native services as
// Builder and MCP. In particular, no browser-side selection policy is needed.
func (api *StreamingAPI) selectProjectVaultItem(ctx context.Context, person, workspace, kind, id string, selected bool) (projectVaultInventory, error) {
	before, err := api.projectVaultInventory(ctx, person, workspace)
	if err != nil {
		return before, err
	}
	var item *projectVaultItem
	for _, group := range before.Vaults {
		for _, row := range group.Items {
			if row.Kind == kind && row.ID == id {
				copy := row
				item = &copy
			}
		}
	}
	if item == nil || !item.CanSelect {
		return before, errors.New("Choose an available item from this project's Vault panel")
	}
	if item.Selected == selected {
		return before, nil
	}
	r := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	scope, err := personalConnectionItemScope(r, workspace, true, "Vault", "Vault")
	if err != nil {
		return before, err
	}
	root := scope.Workspace
	settings := map[string]any{}
	switch kind {
	case "google", "slack":
		action := "detach"
		if selected {
			action = "attach"
		}
		args := map[string]any{"action": action, "connection_id": id, "connection_owner": item.Owner}
		if kind == "google" {
			_, err = api.personalGoogleOperation(r, root, args)
		} else {
			_, err = api.personalSlackOperation(r, root, args)
		}
	case "computer":
		caller := GetUserFromContext(ctx)
		if !personalLaptopSettingsClaims(caller) || !laptopProjectAllowed(ctx, caller, root) {
			return before, errors.New("Use your own Builder connection to select this computer")
		}
		args := map[string]any{"action": "remove"}
		if selected {
			args = map[string]any{"action": "set", "device_id": id, "folders": []any{}, "commands": false}
		}
		w := httptest.NewRecorder()
		api.laptopAccessAction(w, r, caller, root, args)
		if w.Code >= 400 {
			err = errors.New(laptopActionErrorText(w.Body.String()))
		}
	case "project_secret", "shared_secret":
		operation := "unselect"
		if selected {
			operation = "select"
		}
		if kind == "shared_secret" {
			operation = "stop_shared"
			if selected {
				operation = "use_shared"
			}
		}
		settings["secrets"] = map[string]any{operation: []string{id}}
	case "mcp":
		err = api.selectVaultServer(ctx, person, root, id, selected)
	default:
		return before, errors.New("Choose an app, secret or computer from Vault")
	}
	if err != nil {
		return before, err
	}
	if len(settings) > 0 {
		w := httptest.NewRecorder()
		if isCrewProjectPath(root) {
			manifest, e := readCrewProjectManifests(ctx, crewProfileID, root)
			if e != nil {
				return before, e
			}
			settings["crew_id"] = manifest.ID
			api.externalCrewSettingsCall(w, r, "update_settings", settings)
		} else {
			level, manifest := workflowAccessForWorkspacePath(ctx, GetUserFromContext(ctx), root)
			if manifest == nil {
				return before, errors.New("Open a Goal you can build")
			}
			api.externalSettingsCall(w, r, "update_settings", settings, DiscoveredWorkflow{WorkspacePath: root, Manifest: manifest}, level)
		}
		if w.Code >= 400 {
			return before, errors.New(laptopActionErrorText(w.Body.String()))
		}
	}
	log.Printf("[PROJECT_VAULT] actor=%q workspace=%q kind=%q item=%q selected=%t", person, root, kind, id, selected)
	return api.projectVaultInventory(ctx, person, root)
}

type projectVaultGroup struct {
	ID      string             `json:"id"`
	Name    string             `json:"name"`
	Sharing string             `json:"sharing"`
	Project bool               `json:"project"`
	Items   []projectVaultItem `json:"items"`
}

type projectVaultInventory struct {
	Vaults   []projectVaultGroup `json:"vaults"`
	Notices  []string            `json:"notices"`
	Servers  []string            `json:"selected_servers"`
	Secrets  []string            `json:"selected_secrets"`
	Shared   []string            `json:"selected_global_secret_names"`
	Computer crewLaptopSettings  `json:"-"`
}

func vaultSharingLabel(count int) string {
	if count <= 1 {
		return "Only you"
	}
	if count == 2 {
		return "You + 1 person"
	}
	return fmt.Sprintf("You + %d people", count-1)
}

func (api *StreamingAPI) projectVaultInventory(ctx context.Context, person, workspace string) (projectVaultInventory, error) {
	out := projectVaultInventory{Vaults: []projectVaultGroup{}, Notices: []string{}}
	r := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	if mcpCaller(ctx) != person {
		return out, errors.New("Use your own project chat to open Vault")
	}
	scope, err := personalConnectionItemScope(r, workspace, true, "Vault", "Vault")
	if err != nil {
		return out, err
	}
	root := scope.Workspace
	raw, found, err := readProjectRuntimeManifest(ctx, "work", root)
	if err != nil || !found {
		return out, errors.New("Project selections could not be read. Reopen the project and try again")
	}
	var saved struct {
		Capabilities WorkflowCapabilities `json:"capabilities"`
	}
	if json.Unmarshal([]byte(raw), &saved) != nil {
		return out, errors.New("Project selections could not be read")
	}
	caps := saved.Capabilities
	out.Servers = append([]string{}, caps.SelectedServers...)
	out.Secrets = append([]string{}, caps.SelectedSecrets...)
	out.Shared = []string{}
	if caps.SelectedGlobalSecretNames != nil {
		out.Shared = append(out.Shared, (*caps.SelectedGlobalSecretNames)...)
	}
	// Empty Vault picks have the runtime's existing all-connections meaning;
	// NO_SERVERS is the explicit empty selection. Report actual use, not ticks
	// inferred from only the explicit names.
	picks, none := false, slices.Contains(out.Servers, "NO_SERVERS")
	for _, name := range out.Servers {
		picks = picks || strings.HasPrefix(name, "vault_")
	}
	lender := api.projectSecretsOwner(person, root)
	lenderAccess, lenderErr := vaultAccessFor(ctx, lender)
	available := map[string]bool{}
	for _, row := range lenderAccess.Servers {
		available[row.ID] = true
	}
	serverUsed := func(id string) bool {
		return slices.Contains(out.Servers, vaultServerName(id)) || available[id] && !picks && !none
	}
	if lenderErr != nil {
		out.Notices = append(out.Notices, "Project app access could not be checked.")
	}
	var list struct {
		Vaults []struct {
			Group struct {
				ID, Name, ProjectRoot string
				Owners                []string
			}
			Members     []string `json:"members"`
			Connections []struct {
				ID, Label, Provider, Status string
				SignInError                 string `json:"sign_in_error"`
			} `json:"connections"`
			SecretNames []string `json:"secret_names"`
		} `json:"vaults"`
	}
	groups := map[string]*projectVaultGroup{}
	if data, e := vaultPersonRequest(ctx, person, http.MethodGet, "/api/vaults", nil); e == nil && json.Unmarshal(withVaultSignInErrors(data), &list) == nil {
		for _, row := range list.Vaults {
			group := &projectVaultGroup{ID: row.Group.ID, Name: row.Group.Name, Project: row.Group.ProjectRoot != "", Sharing: vaultSharingLabel(len(settingsWith(row.Members, row.Group.Owners...))), Items: []projectVaultItem{}}
			for _, c := range row.Connections {
				status := "needs_attention"
				if c.Status == "active" && c.SignInError == "" {
					status = "connected"
				}
				group.Items = append(group.Items, projectVaultItem{ID: c.ID, Name: c.Label, Provider: c.Provider, Kind: "mcp", Status: status, Selected: serverUsed(c.ID), CanSelect: available[c.ID] || lender == person || slices.Contains(out.Servers, vaultServerName(c.ID))})
			}
			for _, name := range row.SecretNames {
				item := projectVaultItem{ID: name, Name: vaultSecretShortName(name), Kind: "shared_secret", Status: "stored", Selected: slices.Contains(out.Shared, name), CanSelect: true}
				if row.Group.ProjectRoot == root {
					item.ID, item.Kind = item.Name, "project_secret"
					item.Selected = slices.Contains(out.Secrets, item.Name)
				}
				group.Items = append(group.Items, item)
			}
			groups[group.ID] = group
		}
	} else {
		out.Notices = append(out.Notices, "Vaults could not be loaded. Ask in chat to check the connection.")
	}
	// Company and other shared grants are included even when they are not
	// person-owned vaults. Runtime metadata contains no credential values.
	if access, e := vaultAccessFor(ctx, person); e == nil {
		for _, row := range access.Groups {
			group := groups[row.ID]
			if group == nil {
				group = &projectVaultGroup{ID: row.ID, Name: row.Name, Sharing: vaultSharingLabel(row.MemberCount), Project: row.ProjectRoot != "", Items: []projectVaultItem{}}
				groups[row.ID] = group
			}
			for _, c := range row.Servers {
				if slices.ContainsFunc(group.Items, func(i projectVaultItem) bool { return i.Kind == "mcp" && i.ID == c.ID }) {
					continue
				}
				group.Items = append(group.Items, projectVaultItem{ID: c.ID, Name: c.Label, Provider: c.Provider, Kind: "mcp", Status: "connected", Selected: serverUsed(c.ID), CanSelect: available[c.ID] || lender == person || slices.Contains(out.Servers, vaultServerName(c.ID))})
			}
			for _, secret := range row.Secrets {
				if slices.ContainsFunc(group.Items, func(i projectVaultItem) bool {
					return i.ID == secret.Name || i.Kind == "project_secret" && i.Name == vaultSecretShortName(secret.Name)
				}) {
					continue
				}
				group.Items = append(group.Items, projectVaultItem{ID: secret.Name, Name: vaultSecretShortName(secret.Name), Kind: "shared_secret", Status: "stored", Selected: slices.Contains(out.Shared, secret.Name), CanSelect: true})
			}
		}
	} else {
		out.Notices = append(out.Notices, "Shared Vault access could not be checked.")
	}
	if lender != person {
		project := &projectVaultGroup{ID: "project-apps", Name: "Apps used by this project", Project: true, Sharing: "Shared with this project", Items: []projectVaultItem{}}
		for _, row := range lenderAccess.Servers {
			seen := false
			for _, group := range groups {
				seen = seen || slices.ContainsFunc(group.Items, func(i projectVaultItem) bool { return i.Kind == "mcp" && i.ID == row.ID })
			}
			if serverUsed(row.ID) && !seen {
				project.Items = append(project.Items, projectVaultItem{ID: row.ID, Name: row.Label, Provider: row.Provider, Kind: "mcp", Status: "connected", Selected: true, CanSelect: true})
			}
		}
		groups[project.ID] = project
	}
	// A co-owner can inspect and change the project's selected secret names
	// without gaining access to its owner's other personal secrets.
	projectSecrets := &projectVaultGroup{ID: "project-secrets", Name: "Project secrets", Project: true, Sharing: "Shared with this project", Items: []projectVaultItem{}}
	for _, row := range api.secretSources(ctx, person, root).Secrets {
		if !row.ProductStored && !row.ProductSelected && !row.VaultSelected {
			continue
		}
		kind, id, selected := "shared_secret", row.Name, row.VaultSelected
		if row.ProductStored || row.ProductSelected {
			kind, selected = "project_secret", row.ProductSelected
		}
		seen := false
		for _, group := range groups {
			seen = seen || slices.ContainsFunc(group.Items, func(i projectVaultItem) bool { return i.Kind == kind && i.ID == id })
		}
		if !seen {
			projectSecrets.Items = append(projectSecrets.Items, projectVaultItem{ID: id, Name: row.DisplayName, Kind: kind, Status: "stored", Selected: selected, CanSelect: true})
		}
	}
	groups[projectSecrets.ID] = projectSecrets
	personal := projectVaultGroup{ID: "personal", Name: "Your accounts and computers", Sharing: "Only you", Items: []projectVaultItem{}}
	if data, e := api.personalGoogleOperation(r, root, map[string]any{"action": "list_saved"}); e == nil {
		encoded, _ := json.Marshal(data)
		var result struct {
			Accounts []personalGoogleAccountRow `json:"accounts"`
		}
		if json.Unmarshal(encoded, &result) == nil {
			for _, a := range result.Accounts {
				status := "needs_attention"
				if a.Connected {
					status = "connected"
				}
				personal.Items = append(personal.Items, projectVaultItem{ID: a.ID, Name: a.Label, Provider: "Google", Owner: a.Owner, Kind: "google", Status: status, Selected: a.Selected, CanSelect: a.CanAdd || a.CanDetach})
			}
		}
	} else {
		out.Notices = append(out.Notices, "Google account status could not be checked.")
	}
	if data, e := api.personalSlackOperation(r, root, map[string]any{"action": "list_saved"}); e == nil {
		encoded, _ := json.Marshal(data)
		var result slackAccountsResponse
		if json.Unmarshal(encoded, &result) == nil {
			for _, a := range result.Accounts {
				seen := false
				for _, group := range groups {
					seen = seen || slices.ContainsFunc(group.Items, func(i projectVaultItem) bool { return i.ID == a.ID && i.Kind == "mcp" })
				}
				if seen {
					continue
				}
				status := "needs_attention"
				if a.Connected && a.Problem == "" {
					status = "connected"
				}
				personal.Items = append(personal.Items, projectVaultItem{ID: a.ID, Name: a.Label, Provider: "Slack", Owner: a.Owner, Kind: "slack", Status: status, Selected: a.Selected, CanSelect: a.CanAdd || a.CanDetach})
			}
		}
	} else {
		out.Notices = append(out.Notices, "Slack account status could not be checked.")
	}
	if caller := GetUserFromContext(ctx); caller != nil && laptopProjectAllowed(ctx, caller, root) {
		if out.Computer, err = readLaptopSettings(person, root); err != nil {
			out.Notices = append(out.Notices, "Computer selection could not be checked.")
		}
		for _, device := range api.localDeviceListForProject(ctx, caller, root) {
			if out.Computer.DeviceID != device.DeviceID || out.Computer.DeviceOwnerID == "" || out.Computer.DeviceOwnerID == person {
				rememberVaultComputer(person, device.DeviceID)
			}
			personal.Items = append(personal.Items, projectVaultItem{ID: device.DeviceID, Name: device.DeviceID, Kind: "computer", Status: "online", Selected: out.Computer.DeviceID == device.DeviceID, CanSelect: true})
		}
		for _, id := range knownVaultComputers(person) {
			if !slices.ContainsFunc(personal.Items, func(i projectVaultItem) bool { return i.Kind == "computer" && i.ID == id }) {
				personal.Items = append(personal.Items, projectVaultItem{ID: id, Name: id, Kind: "computer", Status: "offline", Selected: out.Computer.DeviceID == id, CanSelect: true})
			}
		}
		if out.Computer.DeviceID != "" && !slices.ContainsFunc(personal.Items, func(i projectVaultItem) bool { return i.Kind == "computer" && i.ID == out.Computer.DeviceID }) {
			personal.Items = append(personal.Items, projectVaultItem{ID: out.Computer.DeviceID, Name: out.Computer.DeviceID, Kind: "computer", Status: "offline", Selected: true, CanSelect: true})
		}
	}
	if len(personal.Items) > 0 {
		out.Vaults = append(out.Vaults, personal)
	}
	for _, group := range groups {
		if len(group.Items) > 0 {
			out.Vaults = append(out.Vaults, *group)
		}
	}
	sort.SliceStable(out.Vaults, func(i, j int) bool {
		if out.Vaults[i].Project != out.Vaults[j].Project {
			return !out.Vaults[i].Project
		}
		return strings.ToLower(out.Vaults[i].Name) < strings.ToLower(out.Vaults[j].Name)
	})
	return out, nil
}
