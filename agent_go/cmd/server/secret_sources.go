package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
)

// This snapshot explains saved choices. It deliberately never decrypts a
// value, initializes a manifest, or migrates the legacy per-person store.
type secretSourceView struct {
	Status   string            `json:"status"`
	Note     string            `json:"note"`
	Warnings []string          `json:"warnings"`
	Secrets  []secretSourceRow `json:"secrets"`
}

type secretSourceRow struct {
	Name             string `json:"name"`
	DisplayName      string `json:"display_name"`
	ProductStored    bool   `json:"product_stored"`
	ProductSelected  bool   `json:"product_selected"`
	VaultSelected    bool   `json:"vault_selected"`
	VaultAvailable   *bool  `json:"vault_available"`
	LegacyStored     bool   `json:"legacy_stored"`
	EnvironmentOwned bool   `json:"environment_owned"`
	ConfiguredSource string `json:"configured_source"`
	Explanation      string `json:"explanation"`
}

func unavailableSecretSources() secretSourceView {
	return secretSourceView{Status: "unavailable", Note: "Secret sources are unavailable. Refresh this product's Settings and try again.", Warnings: []string{}, Secrets: []secretSourceRow{}}
}

func (api *StreamingAPI) secretSources(ctx context.Context, userID, workspace string) secretSourceView {
	view := secretSourceView{Status: "available", Note: "Saved setup, not a running session. Values, account identities and whether a token works are not checked. Vault availability uses your current access.", Warnings: []string{}, Secrets: []secretSourceRow{}}
	if api.chatStore == nil {
		return unavailableSecretSources()
	}
	// The runtime file is authoritative; older project files are only read,
	// never converted here. Work and Code use the same runtime filename.
	raw, found, err := readProjectRuntimeManifest(ctx, "work", workspace)
	if err != nil || !found {
		return unavailableSecretSources()
	}
	var saved struct {
		CreatedBy string `json:"created_by"`
		Access    struct {
			Owners []string `json:"owners"`
		} `json:"access"`
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if json.Unmarshal([]byte(raw), &saved) != nil {
		return unavailableSecretSources()
	}
	var local, shared []string
	localRaw, localInitialized := saved.Capabilities["selected_secrets"]
	if localInitialized && json.Unmarshal(localRaw, &local) != nil {
		return unavailableSecretSources()
	}
	if globalRaw, ok := saved.Capabilities["selected_global_secret_names"]; ok && json.Unmarshal(globalRaw, &shared) != nil {
		return unavailableSecretSources()
	}
	stored, err := api.chatStore.ListWorkflowSecrets(ctx, chathistory.SharedWorkflowSecretsUserID, workspace)
	if err != nil {
		return unavailableSecretSources()
	}
	rows := map[string]*secretSourceRow{}
	rowFor := func(name string) *secretSourceRow {
		if rows[name] == nil {
			rows[name] = &secretSourceRow{Name: name, DisplayName: vaultSecretShortName(name)}
		}
		return rows[name]
	}
	for _, secret := range stored {
		rowFor(secret.Name).ProductStored = true
	}
	// Project runtimes initialize a missing selection from their stored names.
	// Workflows require an explicit selection. Report that existing distinction.
	project := isProductRuntimeManifestWorkspace(workspace)
	if project && localInitialized && strings.TrimSpace(string(localRaw)) == "null" {
		return unavailableSecretSources()
	}
	if project && !localInitialized {
		for _, secret := range stored {
			local = append(local, secret.Name)
		}
		view.Warnings = append(view.Warnings, "This project's own secret selection has not been saved yet; its existing setup loads stored copies. This view does not save that selection.")
	}
	for _, name := range local {
		rowFor(name).ProductSelected = true
	}
	for _, name := range shared {
		rowFor(name).VaultSelected = true
	}
	// An empty shared document may still have pre-existing legacy copies.
	// Match the runtime's candidate identities, but do not move or decrypt them.
	legacyUnavailable := false
	if len(stored) == 0 {
		candidates := append([]string{userID, saved.CreatedBy}, saved.Access.Owners...)
		for _, uid := range appendUniqueStrings(nil, candidates...) {
			if uid == "" || uid == chathistory.SharedWorkflowSecretsUserID {
				continue
			}
			legacy, readErr := api.chatStore.ListWorkflowSecrets(ctx, uid, workspace)
			if readErr != nil {
				view.Status = "partial"
				legacyUnavailable = true
				view.Warnings = append(view.Warnings, "Older product copies could not be checked. Their availability is unknown.")
				continue
			}
			for _, secret := range legacy {
				r := rowFor(secret.Name)
				r.LegacyStored = true
				if project && !localInitialized {
					r.ProductSelected = true
				}
			}
		}
	}
	globals, globalErr := permittedGlobalSecrets(ctx, userID)
	if globalErr != nil {
		view.Status = "partial"
		view.Warnings = append(view.Warnings, "Vault access could not be checked. The company copy and any same-name overlap are unknown; try again after Vault is available.")
	}
	byName := map[string]globalSecretEntry{}
	for _, secret := range globals {
		byName[secret.Name] = secret
	}
	for _, r := range rows {
		secret, available := byName[r.Name]
		if globalErr == nil {
			r.VaultAvailable = &available
		}
		r.EnvironmentOwned = available && !secret.Managed
		r.ConfiguredSource, r.Explanation = "unselected", "Stored here, but not selected for this product."
		switch {
		case r.ProductSelected && r.ProductStored:
			r.ConfiguredSource = "product"
			r.Explanation = "This product's own copy is selected and takes priority when it can be loaded. Its value is not checked."
			if available {
				r.Explanation += " A copy also exists in the company Vault."
				if secret.Managed || r.VaultSelected {
					r.Explanation += " The company copy is used only if the selected own copy cannot be loaded."
				} else {
					r.Explanation += " The company copy is not selected."
				}
			}
		case r.ProductSelected && r.LegacyStored:
			r.ConfiguredSource, r.Explanation = "legacy", "An older own copy is selected. People with access to this product can see its name only; no person’s identity or value is shown. Whether it can be loaded is not checked; this view leaves it where it is."
		case r.ProductSelected || r.VaultSelected:
			if globalErr != nil || r.ProductSelected && legacyUnavailable {
				r.ConfiguredSource, r.Explanation = "unavailable", "Selected, but the company copy's availability could not be checked."
			} else if available && (r.VaultSelected || secret.Managed) {
				r.ConfiguredSource, r.Explanation = "vault", "The company Vault copy is selected. Its value and account are not checked."
				if r.ProductSelected && !r.VaultSelected {
					r.Explanation = "No own copy is stored here. The existing setup uses the company Vault copy with this name; its value and account are not checked."
				}
				if r.ProductStored {
					r.Explanation += " An own copy is stored here but is not selected, so it does not take priority."
				}
			} else {
				r.ConfiguredSource, r.Explanation = "unavailable", "Selected, but no usable source is visible with your current access. Check this product's selection and Vault access in Settings."
			}
		}
		if isVaultSecretName(r.Name) {
			r.Explanation = strings.ReplaceAll(r.Explanation, "company Vault", "personal Vault")
			r.Explanation = strings.ReplaceAll(r.Explanation, "company copy", "personal Vault copy")
		}
		if r.EnvironmentOwned {
			r.Explanation += " The company copy is managed in the server's configuration."
		}
		view.Secrets = append(view.Secrets, *r)
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
