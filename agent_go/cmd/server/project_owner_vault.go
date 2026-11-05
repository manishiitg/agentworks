package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
)

// All in Vault (owner, 5 Nov): a secret added to a Crew, Goal, Flow or Code project is saved in its owner's own Vault
// ("My accounts") and selected on the project by its Vault name, in one step. Everyone who uses the project gets it
// lent by the owner, like any other Vault secret the owner selects. Removing it from a project only unselects it; the
// value stays in the owner's Vault until they delete it there.

var errProjectSecretOwner = errors.New("Only the owner can add a secret here, because it is saved in their Vault. Ask the owner to add it, or give them the value")

// saveProjectSecret stores value as NAME in the project owner's My accounts and returns the Vault name the project
// selects. Saving a name that is already there replaces its value (a rotation, for every project that uses it).
func (api *StreamingAPI) saveProjectSecret(ctx context.Context, actor, root, name, value string) (string, error) {
	actor, name = strings.TrimSpace(actor), strings.TrimSpace(name)
	owner := api.projectSecretsOwner(actor, root)
	if actor == "" || owner != actor {
		return "", errProjectSecretOwner
	}
	if !vaultSecretUserName.MatchString(name) {
		return "", errors.New("a secret name is letters, digits and underscores, starting with a letter or underscore, up to 128 characters")
	}
	vaultID, err := ensurePersonalVault(ctx, owner)
	if err != nil {
		return "", fmt.Errorf("your Vault is unavailable: %w", err)
	}
	if err := api.setVaultSecret(ctx, owner, vaultID, name, value, true); err != nil {
		return "", err
	}
	full, err := vaultSecretName(vaultID, name)
	if err != nil {
		return "", err
	}
	markWorkflowSecretsChanged(root)
	log.Printf("[PROJECT_SECRETS] owner=%s project=%s secret=%s saved=my_accounts", owner, root, name)
	return full, nil
}

// prepareCrewSyncSecret makes a Mac Crew's secret available in its owner's My accounts on this server. state is "kept"
// when the owner's Vault here already has it (the server's value stays), "ready" when the Mac's value was saved, and
// "missing" when neither has a value.
func (api *StreamingAPI) prepareCrewSyncSecret(ctx context.Context, owner, root, name, value string) (string, string, error) {
	vaultID, err := ensurePersonalVault(ctx, owner)
	if err != nil {
		return "", "", err
	}
	full, err := vaultSecretName(vaultID, name)
	if err != nil {
		return "", "missing", nil
	}
	managedGlobalsMu.RLock()
	_, have := managedGlobals[full]
	managedGlobalsMu.RUnlock()
	switch {
	case have:
		return full, "kept", nil
	case strings.TrimSpace(value) == "":
		return full, "missing", nil
	}
	if _, err := api.saveProjectSecret(ctx, owner, root, name, value); err != nil {
		return "", "", err
	}
	return full, "ready", nil
}

// ownVaultSecretValues are the secrets a project selects from its owner's own Vault (My accounts), by the name the
// owner gave them, with their values. A Mac sends them when it moves a Crew to the server.
func ownVaultSecretValues(ctx context.Context, owner string, selection []string) (map[string]string, error) {
	values := map[string]string{}
	if len(selection) == 0 {
		return values, nil
	}
	vaultID, err := ensurePersonalVault(ctx, owner)
	if err != nil {
		return nil, err
	}
	managedGlobalsMu.RLock()
	defer managedGlobalsMu.RUnlock()
	for _, full := range selection {
		short := vaultSecretShortName(full)
		if mine, e := vaultSecretName(vaultID, short); e != nil || mine != full {
			continue
		}
		if value, ok := managedGlobals[full]; ok && value != "" {
			values[short] = value
		}
	}
	return values, nil
}

// withProjectSecret selects a Vault secret and drops any other selected secret with the same name, which it replaces:
// a project has one $SECRET_<NAME> per name.
func withProjectSecret(selection []string, full string) []string {
	out := withoutProjectSecret(selection, vaultSecretShortName(full))
	return append(out, full)
}

// withoutProjectSecret unselects every secret called name (its short or its full Vault name).
func withoutProjectSecret(selection []string, name string) []string {
	short := vaultSecretShortName(strings.TrimSpace(name))
	out := make([]string, 0, len(selection))
	for _, selected := range selection {
		if selected == name || vaultSecretShortName(selected) == short {
			continue
		}
		out = append(out, selected)
	}
	return out
}

// selectProjectSecret saves full as selected in the project's runtime manifest (profileID chooses it as for any product
// selection; Crews, Code, Goals and Flows keep it in workflow.json).
func selectProjectSecret(ctx context.Context, profileID, root, full string) error {
	if err := updateProductSelectedGlobalSecrets(ctx, profileID, root, func(current []string) []string { return withProjectSecret(current, full) }); err != nil {
		return err
	}
	// Until the project secrets are moved, a same-named project copy would win over the saved value.
	return updateProductSelectedSecrets(ctx, profileID, root, func(current []string) []string { return removeString(current, vaultSecretShortName(full)) })
}

// unselectProjectSecret removes name from the project's selection; the value stays in its Vault.
func unselectProjectSecret(ctx context.Context, profileID, root, name string) error {
	if err := updateProductSelectedGlobalSecrets(ctx, profileID, root, func(current []string) []string { return withoutProjectSecret(current, name) }); err != nil {
		return err
	}
	return updateProductSelectedSecrets(ctx, profileID, root, func(current []string) []string { return removeString(current, vaultSecretShortName(name)) })
}
