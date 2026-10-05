package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/manishiitg/mcpagent/executor"
)

// Secrets in a person-owned vault (PLAT-507). The value is stored encrypted in the host's managed global secret store
// under a name reserved for the vault, VLT_<vault>__<NAME>; the Vault service records only that name and grants it to the
// vault's members, and to nobody else. The platform's own name sync skips these names. Values never pass through a tool
// or a chat: an owner types one into the vault screen, or promotes a secret that already exists in a Crew, Code or
// workflow, which the server copies without showing it.

const vaultSecretPrefix = "VLT_"

var vaultSecretUserName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,99}$`)

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
		return "", errors.New("a secret name is letters, digits and underscores, starting with a letter or underscore, up to 100 characters")
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
	full, err := vaultSecretName(vaultID, name)
	if err != nil {
		return err
	}
	if _, err := vaultPersonRequest(ctx, person, http.MethodDelete, "/api/vaults/"+url.PathEscape(vaultID)+"/secrets/"+url.PathEscape(full), nil); err != nil {
		return err
	}
	if err := api.removeManagedGlobalSecretValue(ctx, full); err != nil && !errors.Is(err, errGlobalNotFound) {
		return err
	}
	return nil
}

// promoteSecretToVault copies a secret of this chat's Crew, Code or workflow into a vault the person owns. The agent
// names the secret and confirms; the value moves server-side and is never shown. The original stays where it is.
func (api *StreamingAPI) promoteSecretToVault(ctx context.Context, person, vaultID, name string, confirm bool) (string, error) {
	place := api.placeRootForSession(sessionIDFromContextForVaults(ctx))
	if place == "" {
		return "", errors.New("promote a secret from a Crew, Code or workflow chat")
	}
	if !placeMCPCanAttach(ctx, person, place) {
		return "", errors.New("only someone who can edit this place can copy its secrets into a vault")
	}
	name = strings.TrimSpace(name)
	if !vaultOwnerOf(ctx, person, vaultID) {
		return "", errors.New("you can promote only into a vault you own")
	}
	if !confirm {
		return fmt.Sprintf("Promoting the secret %q copies its value into your vault %s. Everyone you add to that vault can then use it (not read it as text), and only you and the vault's other owners can change or remove it. The original secret stays where it is. Call promote_secret again with confirm=true to go ahead.", name, vaultID), nil
	}
	value, err := api.projectSecretValue(place, name)
	if err != nil {
		return "", fmt.Errorf("read the secret %q here: %w", name, err)
	}
	if err := api.setVaultSecret(ctx, person, vaultID, name, value, false); err != nil {
		return "", err
	}
	return fmt.Sprintf("Copied %q into your vault %s. Members can use it as the secret %s. The original is unchanged.", name, vaultID, mustVaultSecretName(vaultID, name)), nil
}

func mustVaultSecretName(vaultID, name string) string {
	full, _ := vaultSecretName(vaultID, name)
	return full
}

func sessionIDFromContextForVaults(ctx context.Context) string {
	return executor.SessionIDFromContext(ctx)
}
