package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
)

// One-off (delete after the last server has moved: no backward-compatibility code, owner 2026-10-19): moves the old
// project-connection ("place") store into Vault, so Vault is the only store (owner, 2026-10-21). For every project
// attachment in the index it takes the OWNER's connection and puts it in that owner's own vault ("My accounts") with the
// login re-sealed, never copied (promoteStoredConnection, so no second sign-in), selects it in the project, and removes
// the attachment. A person's Slack becomes their one Slack connection there. Connections it cannot move are reported with
// the reason and left in place: a key or header connection (add the key to Vault as a secret), a Google server (it stays
// in its own integration), a custom server (add it in Vault by name and URL, then select it).
//
// Dry run unless apply is true. The operator takes a plain backup of the personal MCP store and the attachment index
// first. Admin only: it acts as each owner, which a Vault administrator already may.

type placeMigrationRow struct {
	Item   string `json:"item"`
	Owner  string `json:"owner"`
	Server string `json:"server"`
	Kind   string `json:"kind"`
	Result string `json:"result"`
	Detail string `json:"detail,omitempty"`
}

func (api *StreamingAPI) migratePlaceConnections(ctx context.Context, apply bool) ([]placeMigrationRow, error) {
	placeMCPMu.Lock()
	all, err := readPlaceMCPAttachmentsLocked()
	placeMCPMu.Unlock()
	if err != nil {
		return nil, err
	}
	roots := make([]string, 0, len(all))
	for root := range all {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	moved := map[string]string{}  // source store + server -> its Vault connection
	failed := map[string]string{} // the same key -> why it could not move
	rows := []placeMigrationRow{}
	for _, root := range roots {
		for _, a := range all[root] {
			row := placeMigrationRow{Item: root, Owner: a.Owner, Server: a.Server}
			store := attachmentStore(a, root)
			server, found := placeMigrationServer(store, a.Server)
			switch {
			case !found:
				row.Kind, row.Result, row.Detail = "missing", "removed", "the connection no longer exists; only its attachment is dropped"
				if apply {
					if err := forgetPlaceMCP(a.Owner, a.Server, root); err != nil {
						row.Result, row.Detail = "failed", err.Error()
					}
				}
				rows = append(rows, row)
				continue
			case strings.HasPrefix(server.Catalog, "Google") || strings.Contains(server.URL, "googleapis.com"):
				row.Kind, row.Result, row.Detail = "google", "skipped", "Google stays in its own integration"
				rows = append(rows, row)
				continue
			case len(server.Headers) > 0:
				row.Kind, row.Result, row.Detail = "key", "skipped", "it uses a key or header: add the key to Vault as a secret and select it"
				rows = append(rows, row)
				continue
			}
			provider, catalogErr := vaultCatalogProvider(ctx, server.URL)
			if catalogErr != nil {
				row.Kind, row.Result, row.Detail = "custom", "skipped", "a custom server: add it in Vault by name and URL, then select it"
				rows = append(rows, row)
				continue
			}
			row.Kind = "oauth"
			switch {
			case server.OAuth == nil:
				row.Kind = "noauth"
			case isPersonalSlack(server):
				row.Kind = "slack"
			}
			if !apply {
				row.Result = "would move"
				rows = append(rows, row)
				continue
			}
			key := store + "\x00" + a.Server
			id, done := moved[key]
			if reason, bad := failed[key]; bad {
				row.Result, row.Detail = "failed", reason
				rows = append(rows, row)
				continue
			}
			if !done {
				id, err = api.placeMigrationConnection(ctx, a.Owner, store, server, provider, row.Kind)
				if err != nil {
					failed[key] = err.Error()
					row.Result, row.Detail = "failed", err.Error()
					rows = append(rows, row)
					continue
				}
				moved[key] = id
			}
			if err := api.selectVaultServer(ctx, a.Owner, root, id, true); err != nil {
				row.Result, row.Detail = "failed", "the Vault connection "+id+" exists but the project could not select it: "+err.Error()
				rows = append(rows, row)
				continue
			}
			if err := forgetPlaceMCP(a.Owner, a.Server, root); err != nil {
				row.Result, row.Detail = "failed", err.Error()
				rows = append(rows, row)
				continue
			}
			row.Result, row.Detail = "moved", "Vault connection "+id
			// A copied login is not always accepted by the Vault; say so instead of reporting a working connection.
			if row.Kind != "noauth" {
				if c, err := vaultConnection(ctx, id); err == nil && c.Status != "active" {
					row.Result, row.Detail = "moved, sign in again", "Vault connection "+id+" needs the owner to sign in once (Vault, or Slack as me for Slack)"
				}
			}
			log.Printf("[PLACE_MIGRATION] moved owner=%s connection=%s kind=%s item=%s vault_connection=%s", a.Owner, a.Server, row.Kind, root, id)
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func placeMigrationServer(store, name string) (placeMCPServer, bool) {
	servers, err := listPlaceMCPServers(store)
	if err != nil {
		return placeMCPServer{}, false
	}
	for _, server := range servers {
		if server.Name == name {
			return server, true
		}
	}
	return placeMCPServer{}, false
}

// placeMigrationConnection returns the Vault connection that stands for one stored connection: the owner's existing Slack
// for a Slack account, else a new connection in the owner's own vault with the stored login re-sealed into it.
func (api *StreamingAPI) placeMigrationConnection(ctx context.Context, owner, store string, server placeMCPServer, provider, kind string) (string, error) {
	serverName := server.Name
	if !personVaultActive(owner) {
		return "", errors.New("this person's account cannot own a vault")
	}
	if kind == "slack" {
		if own, found, err := ownVaultSlack(ctx, owner); err != nil {
			return "", err
		} else if found {
			return own.ID, nil
		}
	}
	vaultID, err := ensurePersonalVault(ctx, owner)
	if err != nil {
		return "", err
	}
	if kind == "noauth" {
		// Nothing to carry over: Vault connects the catalog server itself. One connection per server in a vault, so a
		// second place entry for the same server reuses it.
		if id, found, err := ownVaultConnectionOf(ctx, owner, provider); err != nil {
			return "", err
		} else if found {
			return id, nil
		}
		id, err := createOwnVaultConnection(ctx, owner, vaultID, server)
		if err != nil {
			return "", fmt.Errorf("move %s: %w", serverName, err)
		}
		return id, nil
	}
	id, _, err := api.promoteStoredConnection(ctx, owner, store, serverName, serverName, vaultID, true)
	if err != nil {
		return "", fmt.Errorf("move %s: %w", serverName, err)
	}
	return id, nil
}

// ownVaultConnectionOf finds the person's connection to one catalog server in a vault they own.
func ownVaultConnectionOf(ctx context.Context, person, provider string) (string, bool, error) {
	list, err := personalVaults(ctx, person)
	if err != nil {
		return "", false, err
	}
	norm := func(name string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(name) {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	for _, v := range list.Vaults {
		if v.Role != "owner" {
			continue
		}
		for _, c := range v.Connections {
			if norm(c.Provider) == norm(provider) {
				return c.ID, true, nil
			}
		}
	}
	return "", false, nil
}
