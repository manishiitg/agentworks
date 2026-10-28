package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
)

// One-off conversion of person-created vaults (PLAT-856-400). Each person keeps one personal vault: every other vault
// that only its owner uses is merged into the owner's "My accounts", and a vault shared with other people becomes a
// Company group with the same members, connections and secrets. Secret values move server-side under their new names
// and are never shown. Remove this file once every server has converted.

type personVaultRow struct {
	VaultID     string `json:"vault_id"`
	Name        string `json:"name"`
	Mode        string `json:"mode"`
	Target      string `json:"target,omitempty"`
	Members     int    `json:"members"`
	Connections int    `json:"connections"`
	Secrets     int    `json:"secrets"`
	Result      string `json:"result"`
	Reason      string `json:"reason,omitempty"`
}

type personVaultView struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Owners       []string `json:"owners"`
	Members      []string `json:"members"`
	ConnectorIDs []string `json:"connector_ids"`
	SecretNames  []string `json:"secret_names"`
}

type personVaultStep struct {
	VaultID       string            `json:"vault_id"`
	Mode          string            `json:"mode"`
	TargetID      string            `json:"target_id,omitempty"`
	SecretRenames map[string]string `json:"secret_renames,omitempty"`
}

// convertPersonalVaults lists (apply=false) or applies the conversion. people is how many owners get a merge.
func (api *StreamingAPI) convertPersonalVaults(ctx context.Context, apply bool) (rows []personVaultRow, people int, err error) {
	data, err := vaultServiceRequest(ctx, "", http.MethodGet, "/api/admin/person-vaults", nil)
	if err != nil {
		return nil, 0, err
	}
	var list struct {
		Vaults []personVaultView `json:"vaults"`
	}
	if json.Unmarshal(data, &list) != nil {
		return nil, 0, errors.New("the Vault service returned an unreadable vault list")
	}
	managedGlobalsMu.RLock()
	taken := map[string]bool{}
	for name := range managedGlobals {
		taken[name] = true
	}
	managedGlobalsMu.RUnlock()

	owned := map[string][]personVaultView{}
	var steps []personVaultStep
	var stepRows []int
	for _, v := range list.Vaults {
		if len(v.Owners) == 1 && len(v.Members) <= 1 {
			owned[v.Owners[0]] = append(owned[v.Owners[0]], v)
			continue
		}
		// Shared with other people: a Company group; its secrets take their plain names.
		row := personVaultRow{VaultID: v.ID, Name: v.Name, Mode: "company", Members: len(v.Members), Connections: len(v.ConnectorIDs), Secrets: len(v.SecretNames)}
		renames, reason := personVaultRenames(v.SecretNames, "", taken)
		if reason != "" {
			row.Result, row.Reason = "skipped", reason
		} else {
			steps, stepRows = append(steps, personVaultStep{VaultID: v.ID, Mode: "company", SecretRenames: renames}), append(stepRows, len(rows))
		}
		rows = append(rows, row)
	}
	owners := make([]string, 0, len(owned))
	for owner := range owned {
		owners = append(owners, owner)
	}
	sort.Strings(owners)
	for _, owner := range owners {
		vaults := owned[owner]
		target := ""
		for _, v := range vaults {
			if v.Name == personalVaultName {
				target = v.ID // list is sorted by ID: the first "My accounts" stays
				break
			}
		}
		if target == "" {
			var b [6]byte
			if _, err := rand.Read(b[:]); err != nil {
				return nil, 0, err
			}
			target = "v-" + hex.EncodeToString(b[:])
		}
		merged := false
		for _, v := range vaults {
			if v.ID == target {
				continue
			}
			row := personVaultRow{VaultID: v.ID, Name: v.Name, Mode: "merge", Target: target, Members: len(v.Members), Connections: len(v.ConnectorIDs), Secrets: len(v.SecretNames)}
			renames, reason := personVaultRenames(v.SecretNames, target, taken)
			if reason != "" {
				row.Result, row.Reason = "skipped", reason
			} else {
				steps, stepRows = append(steps, personVaultStep{VaultID: v.ID, Mode: "merge", TargetID: target, SecretRenames: renames}), append(stepRows, len(rows))
				merged = true
			}
			rows = append(rows, row)
		}
		if merged {
			people++
		}
	}
	for _, i := range stepRows {
		rows[i].Result = "planned"
	}
	if !apply || len(steps) == 0 {
		return rows, people, nil
	}

	// Values first under their new names, then the Vault service's metadata in one step, then the old values.
	var copied []string
	undo := func() {
		for _, name := range copied {
			_ = api.removeManagedGlobalSecretValue(ctx, name)
		}
	}
	for _, step := range steps {
		for old, renamed := range step.SecretRenames {
			managedGlobalsMu.RLock()
			value, ok := managedGlobals[old]
			managedGlobalsMu.RUnlock()
			if !ok {
				undo()
				return rows, people, errors.New("a vault secret value is missing: " + old)
			}
			if err := api.storeManagedGlobalSecret(ctx, renamed, value, true); err != nil {
				undo()
				return rows, people, err
			}
			copied = append(copied, renamed)
		}
	}
	payload, _ := json.Marshal(map[string]any{"plan": steps})
	if _, err := vaultServiceRequest(ctx, "", http.MethodPost, "/api/admin/person-vaults", payload); err != nil {
		undo()
		return rows, people, err
	}
	for _, step := range steps {
		for old := range step.SecretRenames {
			if err := api.removeManagedGlobalSecretValue(ctx, old); err != nil && !errors.Is(err, errGlobalNotFound) {
				return rows, people, err
			}
		}
	}
	for _, i := range stepRows {
		rows[i].Result = "converted"
	}
	return rows, people, nil
}

// personVaultRenames gives each vault secret its new name: the target vault's reserved name for a merge, the plain
// name for a Company group. A name that is already taken stops this vault's conversion.
func personVaultRenames(names []string, target string, taken map[string]bool) (map[string]string, string) {
	renames := map[string]string{}
	for _, full := range names {
		short := vaultSecretShortName(full)
		renamed := short
		if target != "" {
			var err error
			if renamed, err = vaultSecretName(target, short); err != nil {
				return nil, "secret " + short + " has an unusable name"
			}
		}
		if taken[renamed] && renamed != full {
			return nil, "a secret named " + short + " already exists there"
		}
		renames[full] = renamed
	}
	for _, renamed := range renames {
		taken[renamed] = true
	}
	return renames, ""
}
