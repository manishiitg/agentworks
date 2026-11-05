package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/sealbox"
)

// A product's secrets live in its owner's own Vault (project_owner_vault.go). The one vault that still belongs to a
// place rather than a person is Brain's keystore: a system setting the administrator holds (its backup token).

// sharedWorkflowSecretAAD is the AES-GCM additional data the retired per-workflow store bound a value to: the store's
// canonical workflow path. Only key rotation and the one-off import of old documents still read such values.
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

// brainVault is Brain's keystore vault and the administrator who holds it (the first active one, by ID).
func brainVault(ctx context.Context) (string, string, error) {
	root, err := chathistory.NormalizeWorkflowSecretPath(brainSecretsPath)
	if err != nil {
		return "", "", err
	}
	owner := ""
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
	if owner == "" && !IsMultiUserMode() {
		owner = GetDefaultUserID()
	}
	if owner == "" || !activeMCPPerson(owner) {
		return "", "", fmt.Errorf("an active Brain administrator is required")
	}
	data, err := vaultPersonRequest(ctx, owner, http.MethodPost, "/api/project-vaults", vaultPromoteJSON(map[string]string{"workspace_root": root}))
	if err != nil {
		return "", "", err
	}
	var result struct{ ID string }
	if json.Unmarshal(data, &result) != nil || result.ID == "" {
		return "", "", fmt.Errorf("Brain's Vault identity unavailable")
	}
	return result.ID, owner, nil
}

// brainVaultNames lists the full Vault names of Brain's secrets.
func brainVaultNames(ctx context.Context) ([]string, string, string, error) {
	id, owner, err := brainVault(ctx)
	if err != nil {
		return nil, "", "", err
	}
	data, err := vaultPersonRequest(ctx, owner, http.MethodGet, "/api/vaults/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, "", "", err
	}
	var view struct {
		Names []string `json:"secret_names"`
	}
	if err = json.Unmarshal(data, &view); err != nil {
		return nil, "", "", err
	}
	return view.Names, id, owner, nil
}

// storeBrainSecret saves one Brain secret in its keystore vault.
func (api *StreamingAPI) storeBrainSecret(ctx context.Context, name, value string) error {
	id, owner, err := brainVault(ctx)
	if err != nil {
		return err
	}
	full, err := vaultSecretName(id, name)
	if err != nil {
		return err
	}
	if err = api.storeManagedGlobalSecret(ctx, full, value, false); err != nil {
		return err
	}
	_, err = vaultPersonRequest(ctx, owner, http.MethodPost, "/api/vaults/"+url.PathEscape(id)+"/secrets", vaultPromoteJSON(map[string]string{"Name": full}))
	return err
}

// deleteBrainSecret removes one Brain secret from its keystore vault and the value store.
func (api *StreamingAPI) deleteBrainSecret(ctx context.Context, name string) error {
	id, owner, err := brainVault(ctx)
	if err != nil {
		return err
	}
	return api.removeVaultSecret(ctx, owner, id, name)
}
