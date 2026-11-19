package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// workflowRunSecretSelection is which Vault secrets a workflow run uses: the workflow's saved selection when it has
// one, otherwise the one the request sent. The run and the workflow page's Keys row both read it here, so a change to
// where a Crew workflow takes its keys from changes both at once.
func workflowRunSecretSelection(requested, saved *[]string) *[]string {
	if saved != nil {
		return saved
	}
	return requested
}

type crewWorkflowUsesItem struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
	// Ready is whether a run started now would get it. Key values are never returned.
	Ready bool `json:"ready"`
	// server is an app's runtime name (vault_<id> or, on a Mac, srv_<conn>_<id>), for the Team check only.
	server string
}

// crewWorkflowUses is what a saved Crew workflow uses, for its page: where it runs, its inputs and presets, the keys
// its script declares, the apps and skills a run gets.
type crewWorkflowUses struct {
	Place   string                 `json:"place"`
	Inputs  []crewWorkflowUsesItem `json:"inputs"`
	Presets []string               `json:"presets"`
	Keys    []crewWorkflowUsesItem `json:"keys"`
	Apps    []crewWorkflowUsesItem `json:"apps"`
	Skills  []string               `json:"skills"`
	// Team is what the workflow would need on its Crew's Team to run online; absent when the Crew has no Team.
	Team *crewWorkflowUsesTeam `json:"team,omitempty"`
}

func (api *StreamingAPI) handleCrewWorkflowUses(w http.ResponseWriter, r *http.Request) {
	binding := api.crewScriptAuthoringBinding(w, r)
	if binding == nil {
		return
	}
	claims := GetUserFromContext(r.Context())
	if r.Method == http.MethodPost {
		api.copyCrewWorkflowNeedToTeam(w, r, claims, binding)
		return
	}
	uses, err := api.crewWorkflowUsesFor(r.Context(), claims.UserID, binding)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(uses)
}

func (api *StreamingAPI) crewWorkflowUsesFor(ctx context.Context, person string, binding *crewExecutableBinding) (crewWorkflowUses, error) {
	out := crewWorkflowUses{Place: "Online", Inputs: []crewWorkflowUsesItem{}, Presets: []string{}, Keys: []crewWorkflowUsesItem{}, Apps: []crewWorkflowUsesItem{}, Skills: []string{}}
	if IsLocalMode() {
		out.Place = "Local"
	}
	definition, err := readCrewScript(ctx, binding.Root)
	if err != nil {
		return out, err
	}
	for _, input := range definition.Inputs {
		out.Inputs = append(out.Inputs, crewWorkflowUsesItem{Name: input.Name, Value: input.Value, Ready: true})
	}
	for _, preset := range definition.InputPresets {
		out.Presets = append(out.Presets, preset.Name)
	}
	// The same manifest, selection and owner the run reads (ReadWorkflowManifest, workflowRunSecretSelection,
	// projectRunSecrets), checked by the run's own MissingScriptSecrets.
	manifest, found, err := ReadWorkflowManifest(ctx, binding.Root)
	if err != nil || !found {
		return out, err
	}
	caps := manifest.Capabilities
	out.Skills = append(out.Skills, caps.SelectedSkills...)
	env := map[string]string{}
	if secrets, err := api.projectRunSecrets(ctx, person, binding.Root, workflowRunSecretSelection(nil, caps.SelectedGlobalSecretNames)); err == nil {
		for _, secret := range secrets {
			env["SECRET_"+secret.Name] = secret.Value
		}
	}
	missing := workflow.MissingScriptSecrets(definition, env)
	for _, name := range definition.Secrets {
		out.Keys = append(out.Keys, crewWorkflowUsesItem{Name: name, Ready: !containsExactString(missing, name)})
	}
	out.Apps = api.crewWorkflowApps(ctx, person, binding.Root, caps)
	out.Team = api.crewWorkflowTeamNeeds(ctx, person, binding, definition.Secrets, out.Apps)
	return out, nil
}

// crewWorkflowApps names the apps a run gets, as the runtime picks them (RuntimeServers): the chosen ones, or every
// Vault app the owner may use when "Use all my apps automatically" is on. Ready is whether the app is available now.
func (api *StreamingAPI) crewWorkflowApps(ctx context.Context, person, root string, caps WorkflowCapabilities) []crewWorkflowUsesItem {
	apps := []crewWorkflowUsesItem{}
	labels := map[string]string{}
	access, accessErr := vaultAccessFor(ctx, api.runSecretsOwner(ctx, person, root))
	if accessErr == nil {
		for _, server := range access.Servers {
			labels[vaultServerName(server.ID)] = firstNonEmptyTrimmed(server.Label, server.Name, server.Provider)
		}
	}
	seen := map[string]bool{}
	add := func(server string) {
		if seen[server] {
			return
		}
		seen[server] = true
		if strings.HasPrefix(server, "vault_") {
			label, ok := labels[server]
			apps = append(apps, crewWorkflowUsesItem{Name: firstNonEmptyTrimmed(label, "An app no longer in Vault"), Ready: ok, server: server})
			return
		}
		item := crewWorkflowUsesItem{Name: server, Ready: true}
		if strings.HasPrefix(server, serverVaultPrefix) {
			item.server = server // an app from the person's Vault on the server, as a Mac names it
		}
		apps = append(apps, item)
	}
	for _, server := range caps.RuntimeServers() {
		add(server)
	}
	if !caps.ChosenVaultAppsOnly && accessErr == nil {
		for _, server := range access.Servers {
			add(vaultServerName(server.ID))
		}
	}
	sort.SliceStable(apps, func(i, j int) bool { return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name) })
	return apps
}
