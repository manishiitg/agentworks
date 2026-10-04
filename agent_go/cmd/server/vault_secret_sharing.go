package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

type vaultShareGroup struct {
	ID          string `json:"ID"`
	Name        string `json:"Name"`
	Description string `json:"Description,omitempty"`
}

func canShareVaultSecrets(userID string) bool {
	return canManageGlobalSecrets(userID) && userAllowedProduct(&UserClaims{UserID: userID}, "mcp-gateway")
}

func vaultSecretShareGroups(ctx context.Context, actor string) ([]vaultShareGroup, error) {
	if !canShareVaultSecrets(actor) {
		return nil, errGlobalAdmin
	}
	data, err := vaultServiceRequest(ctx, actor, http.MethodGet, "/api/admin/groups", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Groups []vaultShareGroup `json:"groups"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, errors.New("Vault groups unavailable")
	}
	if out.Groups == nil {
		out.Groups = []vaultShareGroup{}
	}
	return out.Groups, nil
}

// Host-side transfer: the source value never passes through the browser or model.
// Copies are independent. Sharing never deletes, detaches or rotates the source.
func (api *StreamingAPI) shareWorkflowSecretToVault(ctx context.Context, actor, workspace, sourceName, vaultName string, groupIDs []string) error {
	if !canShareVaultSecrets(actor) {
		return errGlobalAdmin
	}
	sourceName, vaultName = strings.TrimSpace(sourceName), strings.TrimSpace(vaultName)
	if vaultName == "" {
		vaultName = sourceName
	}
	if !globalSecretNamePattern.MatchString(vaultName) || len(vaultName) > 128 || sourceName == "" {
		return errors.New("A valid source and Vault secret name are required")
	}
	if len(groupIDs) == 0 || len(groupIDs) > 100 {
		return errors.New("Select at least one Vault group (maximum 100)")
	}
	groups, err := vaultSecretShareGroups(ctx, actor)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, group := range groups {
		known[group.ID] = true
	}
	for _, id := range groupIDs {
		if !known[id] {
			return errors.New("Select existing Vault groups")
		}
	}
	_, roots, err := authorizeWorkflowContextPathsWithReadRoots(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: actor}), []string{workspace})
	if err != nil || len(roots) != 1 {
		return errors.New("Source project or workflow is unavailable")
	}
	rows, err := api.ensureSharedWorkflowSecrets(ctx, roots[0], actor)
	if err != nil {
		return errors.New("Could not read source secrets")
	}
	for _, row := range rows {
		if row.Name != sourceName {
			continue
		}
		value, err := decryptSharedWorkflowSecret(roots[0], row)
		if err != nil {
			return errors.New("Could not decrypt source secret")
		}
		if err := api.saveManagedGlobalSecret(ctx, actor, vaultName, value, true); err != nil {
			return err
		}
		if err := vaultSecretAdminRequest(ctx, actor, http.MethodPost, "/api/admin/secrets/"+url.PathEscape(vaultName)+"/grants", map[string]any{"group_ids": groupIDs}); err != nil {
			return errors.New("Secret was copied to Vault, but group access could not be confirmed. Review this secret's access in Vault before retrying. The project copy is unchanged")
		}
		return nil
	}
	return errGlobalNotFound
}

func (api *StreamingAPI) handleShareVaultSecret(w http.ResponseWriter, r *http.Request) {
	actor := GetUserIDFromContext(r.Context())
	if !canShareVaultSecrets(actor) {
		globalSecretError(w, errGlobalAdmin)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		groups, err := vaultSecretShareGroups(r.Context(), actor)
		if err != nil {
			writeUsersError(w, 503, err.Error())
			return
		}
		writeUsersJSON(w, 200, map[string]any{"groups": groups})
		return
	}
	var in struct {
		WorkspacePath string   `json:"workspace_path"`
		Name          string   `json:"name"`
		VaultName     string   `json:"vault_name"`
		GroupIDs      []string `json:"group_ids"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in) != nil {
		writeUsersError(w, 400, "Invalid sharing request")
		return
	}
	if err := api.shareWorkflowSecretToVault(r.Context(), actor, in.WorkspacePath, in.Name, in.VaultName, in.GroupIDs); err != nil {
		globalSecretError(w, err)
		return
	}
	writeUsersJSON(w, 200, map[string]bool{"success": true})
}
