package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// All in Vault (owner, 5 Nov): a secret added to a Crew, Goal, Flow or Code project is saved in its owner's own Vault
// ("My accounts") and selected on the project by its Vault name, in one step; a server Crew's goes into its project
// vault instead (#4144). Everyone who uses the project gets it
// lent by the owner, like any other Vault secret the owner selects. Removing it from a project only unselects it; the
// value stays in the owner's Vault until they delete it there.

var errProjectSecretOwner = errors.New("Only the owner can add a secret here, because it is saved in their Vault. Ask the owner to add it, or give them the value")

// Where a key is saved (#4144). A key reaches a project's work only from the vault that project reads: a server Crew
// reads only its project vault (crew_vault.go), everything else its owner's own Vault plus the Team and company keys
// it selects. So the default follows the project, and an agent may still name a place.
const (
	secretPlacePersonal = "personal" // the person's own Vault ("My accounts")
	secretPlaceProject  = "project"  // a server Crew's project vault
	secretPlaceTeam     = "team"     // a Mac Team's vault, through the Team tool
	secretPlaceCompany  = "company"  // company keys, kept by a Vault administrator
)

// defaultSecretPlace is where a key given in this project goes when nobody names a place: a server Crew's project vault,
// or else the person's own Vault.
func defaultSecretPlace(root string) string {
	if serverCrewForPath(root) != "" {
		return secretPlaceProject
	}
	return secretPlacePersonal
}

// secretPlaceRefusal is the plain answer for a place this save does not write to, saying what to do instead.
func secretPlaceRefusal(where string) error {
	switch where {
	case secretPlaceTeam:
		return errors.New("Team keys are added through the Team: save the key in your own Vault first, then share it with the Team tool (way_add, item secret). The value goes server to server and is never shown")
	case secretPlaceCompany:
		return errors.New("Only Vault admins add or change company keys. Ask an admin, keep the key for yourself (personal), or save it in this online project (project)")
	}
	return fmt.Errorf("choose where to save it: %s (only you), %s (everyone in this online project), %s or %s", secretPlacePersonal, secretPlaceProject, secretPlaceTeam, secretPlaceCompany)
}

// saveProjectSecret stores value as NAME and returns the Vault name the project selects and where it went. where is
// one of the places above, or "" for the project's default. Saving a name that is already there replaces its value.
func (api *StreamingAPI) saveProjectSecret(ctx context.Context, actor, root, name, value, where string) (string, string, error) {
	actor, name = strings.TrimSpace(actor), strings.TrimSpace(name)
	where = strings.ToLower(strings.TrimSpace(where))
	if where == "" {
		where = defaultSecretPlace(root)
	}
	if !vaultSecretUserName.MatchString(name) {
		return "", "", errors.New("a secret name is letters, digits and underscores, starting with a letter or underscore, up to 128 characters")
	}
	var vaultID string
	switch where {
	case secretPlaceProject:
		project := serverCrewForPath(root)
		if project == "" {
			return "", "", errors.New("This project has no project vault (only online projects do); save it in your own Vault (personal) instead")
		}
		var err error
		if vaultID, err = api.crewVaultFor(ctx, actor, project); err != nil {
			return "", "", err
		}
		defer forgetCrewVaultContents(project) // the project's next turn and run see it at once
	case secretPlacePersonal:
		// In a Crew everyone saves into their own Vault (projectSecretsOwner); for a Goal only its owner.
		if actor == "" || api.projectSecretsOwner(actor, root) != actor {
			return "", "", errProjectSecretOwner
		}
		var err error
		if vaultID, err = ensurePersonalVault(ctx, actor); err != nil {
			return "", "", fmt.Errorf("your Vault is unavailable: %w", err)
		}
	case secretPlaceCompany:
		// A company key reaches everyone on the server (#4148); only Vault admins add one.
		if !canManageGlobalSecrets(actor) {
			return "", "", secretPlaceRefusal(where)
		}
		if err := api.saveManagedGlobalSecret(ctx, actor, name, value, false); err != nil {
			return "", "", err
		}
		markWorkflowSecretsChanged(root)
		log.Printf("[PROJECT_SECRETS] added_by=%s project=%s secret=%s saved=company", actor, root, name)
		return name, where, nil
	default:
		return "", "", secretPlaceRefusal(where)
	}
	if err := api.setVaultSecret(ctx, actor, vaultID, name, value, true); err != nil {
		return "", "", err
	}
	full, err := vaultSecretName(vaultID, name)
	if err != nil {
		return "", "", err
	}
	markWorkflowSecretsChanged(root)
	log.Printf("[PROJECT_SECRETS] added_by=%s project=%s secret=%s saved=%s", actor, root, name, where)
	return full, where, nil
}

// runSecretsOwner is whose Vault grants a run's selected secrets come from: the project's owner, for the owner, an
// editor, a schedule and a published Flow version alike (owner-lent). A Flow's release folder answers for its draft.
func (api *StreamingAPI) runSecretsOwner(ctx context.Context, person, workspace string) string {

	return api.projectSecretsOwner(person, workspace)
}

// projectRunSecrets resolves a Goal or Flow run's SECRET_* values: the project's selected secrets, lent by its owner.
// A selected secret the owner may no longer use stops the run.
// A server Crew's workflows use only its Crew vault's keys (crew_vault.go).
func (api *StreamingAPI) projectRunSecrets(ctx context.Context, person, workspace string, selection *[]string) ([]resolvedSecret, error) {
	if project := serverCrewForPath(workspace); project != "" {
		names := api.crewVaultSecretNames(ctx, project)
		selection = &names
	}
	owner := api.runSecretsOwner(ctx, person, workspace)
	if err := validateVaultSecretSelection(ctx, owner, selection); err != nil {
		return nil, err
	}
	return api.mergeGlobalSecretsFor(ctx, owner, selection), nil
}

// projectSecretSelection is a project's saved selection of Vault secrets.
func projectSecretSelection(ctx context.Context, root string) ([]string, error) {
	manifest, err := readProjectSelectionManifest(ctx, root)
	if err != nil || !manifest.found {
		return nil, err
	}
	capabilities, _ := manifest.doc["capabilities"].(map[string]any)
	return manifestNames(capabilities["selected_global_secret_names"]), nil
}

// projectSecretValueFor is one secret the project uses, by its Vault name or the $SECRET_ name it is used under, lent
// by the project's owner as in a run.
func (api *StreamingAPI) projectSecretValueFor(ctx context.Context, person, root, name string) (string, bool) {
	selection, err := projectSecretSelection(ctx, root)
	if err != nil {
		return "", false
	}
	rows, err := api.projectRunSecrets(ctx, person, root, &selection)
	if err != nil {
		return "", false
	}
	for _, row := range rows {
		if row.Name == vaultSecretShortName(strings.TrimSpace(name)) && strings.TrimSpace(row.Value) != "" {
			return row.Value, true
		}
	}
	return "", false
}

// projectSecretValue is one secret a project uses, for an MCP connection's header (projectSecretReader): its selected
// Vault secret by name, lent by the project's owner.
func (api *StreamingAPI) projectSecretValue(root, name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if value, ok := api.projectSecretValueFor(ctx, "", root, name); ok {
		return value, nil
	}
	return "", fmt.Errorf("this connection needs the secret %q; add it to this project's Secrets", name)
}

// projectSelectedVaultNames maps the names a check asks for (a selected Vault name, or the $SECRET_ name the project
// uses it under) to the project's selected Vault names. A name the project does not select maps to itself.
func projectSelectedVaultNames(wanted, selection []string) []string {
	out := make([]string, 0, len(wanted))
	for _, name := range wanted {
		match := name
		for _, selected := range selection {
			if selected == name {
				match = selected
				break
			}
			if vaultSecretShortName(selected) == name {
				match = selected
			}
		}
		out = append(out, match)
	}
	return out
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
	if _, _, err := api.saveProjectSecret(ctx, owner, root, name, value, secretPlacePersonal); err != nil {
		return "", "", err
	}
	return full, "ready", nil
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
	return updateProductSelectedGlobalSecrets(ctx, profileID, root, func(current []string) []string { return withProjectSecret(current, full) })
}

// unselectProjectSecret removes name from the project's selection; the value stays in its Vault.
func unselectProjectSecret(ctx context.Context, profileID, root, name string) error {
	return updateProductSelectedGlobalSecrets(ctx, profileID, root, func(current []string) []string { return withoutProjectSecret(current, name) })
}
