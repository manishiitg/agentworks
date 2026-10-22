package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
)

// One-off import only. Runtime never reads these documents. Backups retain the
// original ciphertext; no secret value is included in a result or log.
func (api *StreamingAPI) migrateLegacySecretDocuments(ctx context.Context, apply bool) ([]placeMigrationRow, error) {
	users, err := listWorkspaceChildFolderNames(ctx, "_users")
	if err != nil {
		return nil, err
	}
	files := []string{}
	for _, user := range users {
		base := path.Join("_users", user)
		legacy, e := listWorkspaceFilesRecursive(ctx, path.Join(base, "workflow_secrets"))
		if e != nil {
			return nil, e
		}
		files = append(files, legacy...)
		if user != managedGlobalSecretsUserID && user != "_shared" {
			files = append(files, path.Join(base, "secrets.json"))
		}
	}
	sort.SliceStable(files, func(i, j int) bool {
		// The shared document superseded personal project copies before Vault.
		si, sj := strings.HasPrefix(files[i], "_users/_shared/"), strings.HasPrefix(files[j], "_users/_shared/")
		if si != sj {
			return si
		}
		return files[i] < files[j]
	})
	backup := "config/vault-migration-backups/" + time.Now().UTC().Format("20060102T150405.000000000")
	rows := []placeMigrationRow{}
	for _, secret := range legacyEnvironmentSecrets() {
		row := placeMigrationRow{Server: secret.Name, Kind: "environment secret", Result: "would move"}
		if apply {
			managedGlobalsMu.RLock()
			_, known := managedGlobals[secret.Name]
			managedGlobalsMu.RUnlock()
			if !known {
				if err := api.storeManagedGlobalSecret(ctx, secret.Name, secret.Value, true); err != nil {
					return rows, err
				}
			}
			row.Result = "moved"
		}
		rows = append(rows, row)
	}
	if apply {
		if err := syncVaultSecretMetadata(ctx, mcpCaller(ctx)); err != nil {
			return rows, err
		}
	}

	for _, file := range files {
		file = strings.TrimPrefix(file, "./")
		if !strings.HasSuffix(file, ".json") {
			continue
		}
		raw, exists, err := readFileFromWorkspace(ctx, file)
		if err != nil {
			return rows, err
		}
		if !exists {
			continue
		}
		var doc struct {
			WorkflowPath string `json:"workflow_path"`
			Secrets      map[string]struct {
				EncryptedValue string `json:"encrypted_value"`
			} `json:"secrets"`
		}
		if json.Unmarshal([]byte(raw), &doc) != nil {
			return rows, fmt.Errorf("legacy secret metadata is invalid")
		}
		personal := !strings.Contains(file, "/workflow_secrets/")
		if personal {
			if json.Unmarshal([]byte(raw), &doc.Secrets) != nil {
				return rows, fmt.Errorf("legacy personal secret metadata is invalid")
			}
		}
		if len(doc.Secrets) == 0 {
			if apply {
				if err := deleteWorkspaceFile(ctx, file); err != nil {
					return rows, err
				}
			}
			continue
		}
		parts := strings.Split(file, "/")
		sourceUser := ""
		for i, part := range parts {
			if part == "_users" && i+1 < len(parts) {
				sourceUser = parts[i+1]
				break
			}
		}
		actor := api.projectSecretsOwner(sourceUser, doc.WorkflowPath)
		if sourceUser == chathistory.SharedWorkflowSecretsUserID {
			actor = mcpCaller(ctx)
		}
		if !personal && doc.WorkflowPath == "" {
			return rows, fmt.Errorf("legacy project secret has no project identity")
		}
		if apply {
			if err := writeFileToWorkspace(ctx, path.Join(backup, file), raw); err != nil {
				return rows, err
			}
		}
		for name, record := range doc.Secrets {
			row := placeMigrationRow{Item: doc.WorkflowPath, Owner: actor, Server: name, Kind: "secret", Result: "would move"}
			if !apply {
				rows = append(rows, row)
				continue
			}
			var value string
			if sourceUser == chathistory.SharedWorkflowSecretsUserID {
				aad, e := sharedWorkflowSecretAAD(doc.WorkflowPath)
				if e != nil {
					return rows, e
				}
				value, err = decryptSecretValueWithAAD(record.EncryptedValue, aad)
			} else {
				value, err = decryptSecretValue(record.EncryptedValue, sourceUser)
			}
			if err != nil {
				return rows, fmt.Errorf("a legacy secret could not be decrypted; its backup is retained")
			}
			if personal {
				id, e := ensurePersonalVault(ctx, sourceUser)
				if e != nil {
					return rows, e
				}
				full, e := vaultSecretName(id, name)
				if e != nil {
					return rows, e
				}
				managedGlobalsMu.RLock()
				_, known := managedGlobals[full]
				managedGlobalsMu.RUnlock()
				if !known {
					if e = api.storeManagedGlobalSecret(ctx, full, value, true); e != nil {
						return rows, e
					}
				}
				if _, e = vaultPersonRequest(ctx, sourceUser, http.MethodPost, "/api/vaults/"+url.PathEscape(id)+"/secrets", vaultPromoteJSON(map[string]string{"Name": full})); e != nil {
					return rows, e
				}
				if e = api.chatStore.DeleteUserSecret(ctx, sourceUser, name); e != nil {
					return rows, e
				}
				row.Result = "moved"
				rows = append(rows, row)
				continue
			}
			acting := context.WithValue(ctx, UserContextKey, &UserClaims{UserID: actor})
			existing, e := api.ensureSharedWorkflowSecrets(acting, doc.WorkflowPath, actor)
			if e != nil {
				return rows, e
			}
			found := false
			for _, current := range existing {
				if current.Name == name {
					found = true
					break
				}
			}
			if !found {
				if err := api.upsertSharedWorkflowSecret(acting, doc.WorkflowPath, name, value); err != nil {
					return rows, err
				}
			}
			// A previously imported Vault value is canonical; retrying never overwrites a rotation.
			if err := api.chatStore.DeleteWorkflowSecret(ctx, sourceUser, doc.WorkflowPath, name); err != nil {
				return rows, err
			}
			row.Result = "moved"
			rows = append(rows, row)
		}
		if apply {
			if err := deleteWorkspaceFile(ctx, file); err != nil {
				return rows, err
			}
		}
	}
	return rows, nil
}

func (api *StreamingAPI) migrateLegacyPersonalMCPSecrets(ctx context.Context, person string, apply bool) ([]placeMigrationRow, error) {
	dir, err := placeMCPDir(person)
	if err != nil {
		return nil, err
	}
	secrets := map[string]string{}
	if err := readPlaceMCPJSON(path.Join(dir, "secrets.json"), &secrets); err != nil {
		return nil, err
	}
	rows := []placeMigrationRow{}
	for name, sealed := range secrets {
		row := placeMigrationRow{Owner: person, Server: name, Kind: "secret", Result: "would move"}
		if apply {
			value, e := decryptSecretValueWithAAD(sealed, personalSecretAAD(person, name))
			if e != nil {
				return rows, fmt.Errorf("a personal credential could not be decrypted")
			}
			id, e := ensurePersonalVault(ctx, person)
			if e != nil {
				return rows, e
			}
			full, e := vaultSecretName(id, name)
			if e != nil {
				return rows, e
			}
			managedGlobalsMu.RLock()
			_, exists := managedGlobals[full]
			managedGlobalsMu.RUnlock()
			if !exists {
				if e = api.storeManagedGlobalSecret(ctx, full, value, true); e != nil {
					return rows, e
				}
			}
			if _, e = vaultPersonRequest(ctx, person, http.MethodPost, "/api/vaults/"+url.PathEscape(id)+"/secrets", vaultPromoteJSON(map[string]string{"Name": full})); e != nil {
				return rows, e
			}
			delete(secrets, name)
			if e = writePlaceMCPJSON(path.Join(dir, "secrets.json"), secrets); e != nil {
				return rows, e
			}
			row.Result = "moved"
		}
		rows = append(rows, row)
	}
	return rows, nil
}
