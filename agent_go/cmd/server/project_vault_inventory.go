package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

// The panel and chat read the same names-only snapshot. Selection still uses
// the existing settings, personal-account and laptop operations.
type projectVaultItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Provider     string `json:"provider,omitempty"`
	AccountEmail string `json:"account_email,omitempty"`
	App          string `json:"app,omitempty"`
	Owner        string `json:"owner,omitempty"`
	// Source says whose it is, in plain words: "you", a Company group's name, "this project" or "the project's owner".
	Source    string `json:"source,omitempty"`
	Status    string `json:"status"`
	Selected  bool   `json:"selected"`
	CanSelect bool   `json:"can_select"`
	// UsedIn names the person's own projects that use this item (only for Yours), so they know what removing it affects.
	UsedIn []string `json:"used_in,omitempty"`
}

// One checkbox edits one existing setting through the same native services as
// Builder and MCP. In particular, no browser-side selection policy is needed.
func (api *StreamingAPI) selectProjectVaultItem(ctx context.Context, person, workspace, kind, id string, selected bool) (projectVaultInventory, error) {
	before, err := api.projectVaultInventory(ctx, person, workspace)
	if err != nil {
		return before, err
	}
	// "Use all my apps automatically" (PLAT-856-394): one project switch, the same Builder check as one app.
	if kind == "all_vault_apps" {
		if before.UseAllVaultApps == selected {
			return before, nil
		}
		r := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
		scope, err := personalConnectionItemScope(r, workspace, true, "Vault", "Vault")
		if err != nil {
			return before, err
		}
		if err := api.setProjectSelectedServers(ctx, scope.Workspace, func(current []string) []string { return current }, !selected); err != nil {
			return before, err
		}
		return api.projectVaultInventory(ctx, person, workspace)
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
	Vaults  []projectVaultGroup `json:"vaults"`
	Notices []string            `json:"notices"`
	Servers []string            `json:"selected_servers"`
	// UseAllVaultApps is "Use all my apps automatically" (PLAT-856-394).
	UseAllVaultApps bool               `json:"use_all_vault_apps"`
	Secrets         []string           `json:"selected_secrets"`
	Shared          []string           `json:"selected_global_secret_names"`
	Computer        crewLaptopSettings `json:"-"`
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
	out.Servers = savedAppSelection(caps.SelectedServers)
	out.UseAllVaultApps = !caps.ChosenVaultAppsOnly
	out.Secrets = append([]string{}, caps.SelectedSecrets...)
	out.Shared = []string{}
	if caps.SelectedGlobalSecretNames != nil {
		out.Shared = append(out.Shared, (*caps.SelectedGlobalSecretNames)...)
	}
	// With every Vault app on, each app the owner may use is in use; otherwise only the chosen ones. Report actual use.
	chosenOnly := caps.ChosenVaultAppsOnly
	lender := api.projectSecretsOwner(person, root)
	lenderAccess, lenderErr := vaultAccessFor(ctx, lender)
	available := map[string]bool{}
	for _, row := range lenderAccess.Servers {
		available[row.ID] = true
	}
	serverUsed := func(id string) bool {
		return slices.Contains(out.Servers, vaultServerName(id)) || available[id] && !chosenOnly
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
	// Every item lands under one of three headings (PLAT-856-400): Yours, Company, This project only.
	category := map[string]string{}
	if data, e := vaultPersonRequest(ctx, person, http.MethodGet, "/api/vaults", nil); e == nil && json.Unmarshal(withVaultSignInErrors(data), &list) == nil {
		for _, row := range list.Vaults {
			group := &projectVaultGroup{ID: row.Group.ID, Name: row.Group.Name, Project: row.Group.ProjectRoot != "", Items: []projectVaultItem{}}
			switch {
			case row.Group.ProjectRoot != "":
				category[group.ID] = "project"
			case slices.Contains(row.Group.Owners, person) && len(settingsWith(row.Members, row.Group.Owners...)) <= 1:
				category[group.ID] = "yours"
			default:
				category[group.ID] = "company"
			}
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
				group = &projectVaultGroup{ID: row.ID, Name: row.Name, Project: row.ProjectRoot != "", Items: []projectVaultItem{}}
				groups[row.ID] = group
				category[row.ID] = "company"
				if row.ProjectRoot != "" {
					category[row.ID] = "project"
				}
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
		project := &projectVaultGroup{ID: "project-apps", Name: "the project's owner", Project: true, Items: []projectVaultItem{}}
		category[project.ID] = "project"
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
	projectSecrets := &projectVaultGroup{ID: "project-secrets", Name: "this project", Project: true, Items: []projectVaultItem{}}
	category[projectSecrets.ID] = "project"
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
	// Shared Google connections use the existing company registry, rather than
	// personal list_saved. Show the same project-scoped accounts as the Google
	// tools without making them attachable through personal-account controls.
	if google := services.GetGmailService(); google != nil {
		company := &projectVaultGroup{ID: "company-google", Name: "Company Google", Items: []projectVaultItem{}}
		category[company.ID] = "company"
		scope := gmailToolScope(root)
		current, _ := google.ConnectionForScope("", scope)
		for _, account := range google.ConnectionsUsableFrom(scope) {
			if account.IsPrivate() {
				continue
			}
			status := "needs_attention"
			if account.Enabled && account.Status == services.GmailConnectionConnected {
				status = "connected"
			}
			company.Items = append(company.Items, projectVaultItem{ID: account.ID, Name: account.DisplayName, AccountEmail: account.Email, Provider: "Google", Kind: "google", Status: status, Selected: account.ID == current.ID})
		}
		groups[company.ID] = company
	}
	personal := projectVaultGroup{ID: "personal", Name: "you", Items: []projectVaultItem{}}
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
				email := ""
				if a.Mine {
					email = a.Google.Email
				}
				personal.Items = append(personal.Items, projectVaultItem{ID: a.ID, Name: a.Label, AccountEmail: email, Provider: "Google", Owner: a.Owner, Kind: "google", Status: status, Selected: a.Selected, CanSelect: a.CanAdd || a.CanDetach})
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
					for i := range group.Items {
						if group.Items[i].ID == a.ID && group.Items[i].Kind == "mcp" {
							// Keep the canonical MCP selection; identify the already-authorized personal Slack app.
							group.Items[i].App = "slack"
							seen = true
						}
					}
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
	groups[personal.ID] = &personal
	category[personal.ID] = "yours"
	headings := []projectVaultGroup{
		{ID: "yours", Name: "Yours", Sharing: "Only you, and the projects you build that use them", Items: []projectVaultItem{}},
		{ID: "company", Name: "Company", Sharing: "Given to you by an administrator", Items: []projectVaultItem{}},
		{ID: "project", Name: "This project only", Sharing: "Kept with this project", Project: true, Items: []projectVaultItem{}},
	}
	ids := make([]string, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	// Yours first, so an item reached both as yours and through a group shows as yours; then by source name.
	order := map[string]int{"yours": 0, "company": 1, "project": 2}
	sort.SliceStable(ids, func(i, j int) bool {
		if order[category[ids[i]]] != order[category[ids[j]]] {
			return order[category[ids[i]]] < order[category[ids[j]]]
		}
		return strings.ToLower(groups[ids[i]].Name) < strings.ToLower(groups[ids[j]].Name)
	})
	seen := map[string]bool{}
	for _, id := range ids {
		group, heading := groups[id], &headings[order[category[id]]]
		source := group.Name
		if source == "Platform" {
			source = "Everyone"
		} else if category[id] == "yours" {
			source = "you"
		}
		for _, item := range group.Items {
			if key := item.Kind + ":" + item.ID; !seen[key] {
				seen[key] = true
				item.Source = source
				heading.Items = append(heading.Items, item)
			}
		}
	}
	if yours := &headings[0]; len(yours.Items) > 0 {
		used := api.personalUsedIn(ctx, person)
		for i, item := range yours.Items {
			if item.Kind == "mcp" || item.Kind == "shared_secret" || item.Kind == "computer" {
				yours.Items[i].UsedIn = used.forItem(item.Kind, item.ID)
			}
		}
	}
	for _, heading := range headings {
		if len(heading.Items) > 0 {
			out.Vaults = append(out.Vaults, heading)
		}
	}
	return out, nil
}
