package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

// This snapshot explains a product's saved secret choices: the Vault secrets it selects, lent by its owner. It never
// reads a value.
type secretSourceView struct {
	Status   string            `json:"status"`
	Note     string            `json:"note"`
	Warnings []string          `json:"warnings"`
	Secrets  []secretSourceRow `json:"secrets"`
}

type secretSourceRow struct {
	Name             string `json:"name"`
	DisplayName      string `json:"display_name"`
	VaultAvailable   *bool  `json:"vault_available"`
	ConfiguredSource string `json:"configured_source"`
	Explanation      string `json:"explanation"`
}

func unavailableSecretSources() secretSourceView {
	return secretSourceView{Status: "unavailable", Note: "Secret sources are unavailable. Refresh this product's Settings and try again.", Warnings: []string{}, Secrets: []secretSourceRow{}}
}

func (api *StreamingAPI) secretSources(ctx context.Context, userID, workspace string) secretSourceView {
	view := secretSourceView{Status: "available", Note: "Saved setup, not a running session. Values, account identities and whether a token works are not checked. Secrets come from Vault and are lent by the project's owner.", Warnings: []string{}, Secrets: []secretSourceRow{}}
	raw, found, err := readProjectRuntimeManifest(ctx, "work", workspace)
	if err != nil || !found {
		return unavailableSecretSources()
	}
	var saved struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if json.Unmarshal([]byte(raw), &saved) != nil {
		return unavailableSecretSources()
	}
	var selected []string
	if globalRaw, ok := saved.Capabilities["selected_global_secret_names"]; ok && json.Unmarshal(globalRaw, &selected) != nil {
		return unavailableSecretSources()
	}
	owner := api.projectSecretsOwner(userID, workspace)
	globals, globalErr := permittedGlobalSecrets(ctx, owner)
	if globalErr != nil {
		view.Status = "partial"
		view.Warnings = append(view.Warnings, "Vault access could not be checked; try again after Vault is available.")
	}
	usable := map[string]bool{}
	for _, secret := range globals {
		usable[secret.Name] = true
	}
	for _, name := range selected {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		row := secretSourceRow{Name: name, DisplayName: vaultSecretShortName(name), ConfiguredSource: "vault"}
		switch {
		case globalErr != nil:
			row.ConfiguredSource, row.Explanation = "unavailable", "Selected, but Vault could not be checked."
		case usable[name]:
			available := true
			row.VaultAvailable = &available
			row.Explanation = "Selected from Vault and lent by the project's owner. Scripts read it as $SECRET_" + row.DisplayName + ". Its value is not checked."
		default:
			available := false
			row.VaultAvailable = &available
			row.ConfiguredSource, row.Explanation = "unavailable", "Selected, but the project's owner can no longer use it. Add it again or unselect it."
		}
		view.Secrets = append(view.Secrets, row)
	}
	sort.Slice(view.Secrets, func(i, j int) bool { return view.Secrets[i].Name < view.Secrets[j].Name })
	return view
}

func (api *StreamingAPI) handleSecretSources(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	workspace, err := secretWorkspacePathForRequest(r, r.URL.Query().Get("workspace_path"))
	if err != nil || strings.TrimSpace(r.URL.Query().Get("workspace_path")) == "" {
		writeUsersError(w, http.StatusBadRequest, "Open this product's Settings to inspect its secret sources.")
		return
	}
	manifest, _, _ := readWorkflowManifest(r.Context(), workspace, false)
	if workflowAccessForManifest(GetUserFromContext(r.Context()), manifest) == WorkflowAccessNone {
		writeWorkflowPermissionDenied(w, "read")
		return
	}
	writeUsersJSON(w, http.StatusOK, api.secretSources(r.Context(), GetUserIDFromContext(r.Context()), workspace))
}
