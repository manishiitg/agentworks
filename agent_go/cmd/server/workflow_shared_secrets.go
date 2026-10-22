package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/sealbox"
)

// Project tools retain their existing scope and authorization. Vault is the
// sole metadata and encrypted-value store, partitioned by stable project root.
// Legacy ciphertext helpers are retained only for migration and owner reveal.

// sharedWorkflowSecretAAD is the AES-GCM additional data for a shared
// workflow secret: the store's canonical workflow path. Binding to the path
// means a ciphertext lifted from one workflow's document cannot be replayed
// into another's.
func sharedWorkflowSecretAAD(workflowPath string) ([]byte, error) {
	normalized, err := chathistory.NormalizeWorkflowSecretPath(workflowPath)
	if err != nil {
		return nil, err
	}
	return []byte("workflow:" + normalized), nil
}

// encryptSecretValueWithAAD seals plaintext with the server secrets key and
// the given additional data; the result is base64(nonce || ciphertext), the
// same wire shape /api/secrets/encrypt produces. (pkg/sealbox is the one
// AES-GCM helper; secrets_routes.go, provider_keys_store.go and
// services/workspace_config.go still carry their own copies to migrate.)
func encryptSecretValueWithAAD(plaintext string, aad []byte) (string, error) {
	return sealbox.Seal(deriveSecretsKey(), plaintext, aad)
}

// decryptSecretValueWithAAD is the inverse of encryptSecretValueWithAAD.
func decryptSecretValueWithAAD(encryptedBase64 string, aad []byte) (string, error) {
	return sealbox.Open(deriveSecretsKey(), encryptedBase64, aad)
}

// upsertSharedWorkflowSecret stores plaintext for everyone with access to the
// workflow.

func (api *StreamingAPI) projectVault(ctx context.Context, root, actor string) (string, string, error) {
	normalized, err := chathistory.NormalizeWorkflowSecretPath(root)
	if err != nil {
		return "", "", err
	}
	if actor == "" {
		actor = mcpCaller(ctx)
	}
	if actor == "" && !IsMultiUserMode() {
		actor = GetDefaultUserID()
	}
	owner := api.projectSecretsOwner(actor, normalized)
	if normalized == brainSecretsPath {
		if directory, e := readUserDirectoryFile(); e == nil {
			var admins []string
			for _, u := range directory.Users {
				if a := userAccessForClaims(&UserClaims{UserID: u.ID}); a.Admin && !a.Disabled {
					admins = append(admins, u.ID)
				}
			}
			sort.Strings(admins)
			if len(admins) > 0 {
				owner = admins[0]
			}
		}
	}

	if owner == "" || !activeMCPPerson(owner) {
		return "", "", fmt.Errorf("an active project owner is required")
	}
	data, err := vaultPersonRequest(ctx, owner, http.MethodPost, "/api/project-vaults", vaultPromoteJSON(map[string]string{"workspace_root": normalized}))
	if err != nil {
		return "", "", err
	}
	var result struct{ ID string }
	if json.Unmarshal(data, &result) != nil || result.ID == "" {
		return "", "", fmt.Errorf("project Vault identity unavailable")
	}
	return result.ID, owner, nil
}

// Project tools keep their existing permissions and names; Vault is their only
// resource store. Values use the same encrypted Vault value store as shared keys.
func (api *StreamingAPI) upsertSharedWorkflowSecret(ctx context.Context, workflowPath, name, plaintext string) error {
	id, owner, err := api.projectVault(ctx, workflowPath, mcpCaller(ctx))
	if err != nil {
		return err
	}
	full, err := vaultSecretName(id, name)
	if err != nil {
		return err
	}
	if err = api.storeManagedGlobalSecret(ctx, full, plaintext, false); err != nil {
		return err
	}
	if _, err = vaultPersonRequest(ctx, owner, http.MethodPost, "/api/vaults/"+url.PathEscape(id)+"/secrets", vaultPromoteJSON(map[string]string{"Name": full})); err != nil {
		return err
	}
	markWorkflowSecretsChanged(workflowPath)
	return nil
}

func (api *StreamingAPI) deleteSharedWorkflowSecret(ctx context.Context, workflowPath, name, callerUserID string) error {
	id, owner, err := api.projectVault(ctx, workflowPath, callerUserID)
	if err != nil {
		return err
	}
	if err = api.removeVaultSecret(ctx, owner, id, name); err != nil {
		return err
	}
	markWorkflowSecretsChanged(workflowPath)
	return nil
}

func decryptSharedWorkflowSecret(workflowPath string, s chathistory.UserSecret) (string, error) {
	if s.VaultName == "" {
		return "", fmt.Errorf("secret has not been migrated to Vault")
	}
	managedGlobalsMu.RLock()
	value, ok := managedGlobals[s.VaultName]
	managedGlobalsMu.RUnlock()
	if !ok {
		return "", fmt.Errorf("Vault secret value unavailable")
	}
	return value, nil
}

func (api *StreamingAPI) ensureSharedWorkflowSecrets(ctx context.Context, workflowPath, requestingUserID string) ([]chathistory.UserSecret, error) {
	if strings.TrimSpace(workflowPath) == "" {
		return nil, nil
	}
	id, owner, err := api.projectVault(ctx, workflowPath, requestingUserID)
	if err != nil {
		return nil, err
	}
	data, err := vaultPersonRequest(ctx, owner, http.MethodGet, "/api/vaults/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	var view struct {
		Names []string `json:"secret_names"`
	}
	if err = json.Unmarshal(data, &view); err != nil {
		return nil, err
	}
	rows := make([]chathistory.UserSecret, 0, len(view.Names))
	for _, full := range view.Names {
		rows = append(rows, chathistory.UserSecret{Name: vaultSecretShortName(full), VaultName: full, WorkflowPath: workflowPath})
	}
	return rows, nil
}
