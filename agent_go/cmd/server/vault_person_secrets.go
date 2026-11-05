package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Vault owns secret metadata and encrypted values. People can supply values
// through chat; tool replies contain names only and never echo a value.

const vaultSecretPrefix = "VLT_"

var vaultSecretUserName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func isVaultSecretName(name string) bool {
	return strings.HasPrefix(name, vaultSecretPrefix) && strings.Contains(name[len(vaultSecretPrefix):], "__")
}

// vaultSecretName is the reserved host name of one vault's secret.
func vaultSecretName(vaultID, name string) (string, error) {
	id := strings.ToUpper(strings.ReplaceAll(strings.TrimPrefix(strings.TrimSpace(vaultID), "v-"), "-", "_"))
	if id == "" || !regexp.MustCompile(`^[A-Z0-9_]{4,32}$`).MatchString(id) {
		return "", errors.New("invalid vault")
	}
	name = strings.TrimSpace(name)
	if !vaultSecretUserName.MatchString(name) {
		return "", errors.New("a secret name is letters, digits and underscores, starting with a letter or underscore, up to 128 characters")
	}
	return vaultSecretPrefix + id + "__" + name, nil
}

// vaultSecretShortName turns a reserved host name back into the name the owner chose.
func vaultSecretShortName(full string) string {
	if i := strings.Index(full, "__"); isVaultSecretName(full) && i >= 0 {
		return full[i+2:]
	}
	return full
}

// setVaultSecret stores a value in a vault the person owns and grants it to the vault's members. replace allows
// changing an existing value. Ownership is checked by the Vault service as well, and the value is rolled back if the
// Vault service refuses.
func (api *StreamingAPI) setVaultSecret(ctx context.Context, person, vaultID, name, value string, replace bool) error {
	if !personVaultActive(person) {
		return errors.New("your account cannot manage vaults")
	}
	if strings.TrimSpace(value) == "" {
		return errors.New("a non-empty value is required")
	}
	if err := api.checkVaultSecretProjectScope(ctx, person, vaultID); err != nil {
		return err
	}
	full, err := vaultSecretName(vaultID, name)
	if err != nil {
		return err
	}
	if !vaultOwnerOf(ctx, person, vaultID) {
		return errors.New("only an owner of this vault can add secrets")
	}
	managedGlobalsMu.RLock()
	_, existed := managedGlobals[full]
	managedGlobalsMu.RUnlock()
	if existed && !replace {
		return errors.New("this vault already has a secret with that name; replace it explicitly")
	}
	if err := api.storeManagedGlobalSecret(ctx, full, value, false); err != nil {
		return err
	}
	if _, err := vaultPersonRequest(ctx, person, http.MethodPost, "/api/vaults/"+url.PathEscape(vaultID)+"/secrets", vaultPromoteJSON(map[string]string{"Name": full})); err != nil {
		if !existed {
			_ = api.removeManagedGlobalSecretValue(ctx, full)
		}
		return err
	}
	return nil
}

// removeVaultSecret revokes a vault secret everywhere, then deletes its value.
func (api *StreamingAPI) removeVaultSecret(ctx context.Context, person, vaultID, name string) error {
	if err := api.checkVaultSecretProjectScope(ctx, person, vaultID); err != nil {
		return err
	}
	full, err := vaultSecretName(vaultID, name)
	if err != nil {
		return err
	}
	// A name Vault no longer knows (a selected secret that never got a value) is already gone; removing it still
	// succeeds so the caller can detach the name.
	if _, err := vaultPersonRequest(ctx, person, http.MethodDelete, "/api/vaults/"+url.PathEscape(vaultID)+"/secrets/"+url.PathEscape(full), nil); err != nil && !strings.Contains(err.Error(), "unknown secret") {
		return err
	}
	if err := api.removeManagedGlobalSecretValue(ctx, full); err != nil && !errors.Is(err, errGlobalNotFound) {
		return err
	}
	return nil
}

func (api *StreamingAPI) checkVaultSecretProjectScope(ctx context.Context, person, vaultID string) error {
	data, err := vaultPersonRequest(ctx, person, http.MethodGet, "/api/vaults/"+url.PathEscape(vaultID), nil)
	if err != nil {
		return err
	}
	var view struct{ Group struct{ ProjectRoot string } }
	if json.Unmarshal(data, &view) != nil {
		return errors.New("Vault scope unavailable")
	}
	root := view.Group.ProjectRoot
	if root == "" {
		return nil
	}
	if root == brainSecretsPath {
		r := (&http.Request{}).WithContext(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: person}))
		if !brainSecretsManager(r) {
			return errors.New("only whole-Brain owners manage these secrets")
		}
		return nil
	}
	// The retired per-project vaults are emptied into their owners' Vaults at startup; nothing is added to them.
	return errors.New("this project's own vault is retired; save the secret in your Vault")
}
