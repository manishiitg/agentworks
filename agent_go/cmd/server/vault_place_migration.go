package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/mcpagent/oauth"
)

// One-off importer. A plain ciphertext backup precedes changes. All remote
// MCP types (catalog/custom, OAuth/key/no-auth, HTTP/SSE) move to Vault with
// stable account identity; project selections are rewritten before source
// records are removed. Failures leave their original records intact.

type placeMigrationRow struct {
	Item   string `json:"item"`
	Owner  string `json:"owner"`
	Server string `json:"server"`
	Kind   string `json:"kind"`
	Result string `json:"result"`
	Detail string `json:"detail,omitempty"`
}

func (api *StreamingAPI) migratePlaceConnections(ctx context.Context, apply bool) ([]placeMigrationRow, error) {
	if apply {
		if err := backupLegacyMCPStore(); err != nil {
			return nil, err
		}
		// Moving a connection takes about a minute (tool discovery). A caller that gives up waiting must not stop the move
		// half way, so the work outlives the request that started it.
		ctx = context.WithoutCancel(ctx)
	}
	placeMCPMu.Lock()
	all, err := readPlaceMCPAttachmentsLocked()
	placeMCPMu.Unlock()
	if err != nil {
		return nil, err
	}
	users := map[string]bool{GetDefaultUserID(): true}
	if directory, e := readUserDirectoryFile(); e == nil {
		for _, u := range directory.Users {
			users[u.ID] = true
		}
	}
	for _, attachments := range all {
		for _, a := range attachments {
			users[a.Owner] = true
		}
	}
	attached := map[string]bool{}
	for root, attachments := range all {
		for _, a := range attachments {
			attached[attachmentStore(a, root)+"\x00"+a.Server] = true
		}
	}
	for person := range users {
		if person == "" {
			continue
		}
		servers, e := listPlaceMCPServers(person)
		if e != nil {
			return nil, e
		}
		for _, server := range servers {
			if !attached[person+"\x00"+server.Name] {
				all[""] = append(all[""], placeMCPAttachment{Owner: person, Server: server.Name})
			}
		}
	}
	sharedRows, err := api.migrateLegacySharedMCP(ctx, apply)
	if err != nil {
		return sharedRows, err
	}
	secretRows, err := api.migrateLegacySecretDocuments(ctx, apply)
	if err != nil {
		return secretRows, err
	}
	roots := make([]string, 0, len(all))
	for root := range all {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	moved := map[string]string{}  // source store + server -> its Vault connection
	failed := map[string]string{} // the same key -> why it could not move
	rows := append(sharedRows, secretRows...)
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
			}
			provider := server.Catalog
			row.Kind = "oauth"
			switch {
			case len(server.Headers) > 0:
				row.Kind = "key"
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
			if err := api.replaceMigratedMCPSelection(ctx, a.Owner, root, a.Server, placeMCPInternalName(store, a.Server), id); err != nil {
				row.Result, row.Detail = "failed", "the Vault connection "+id+" exists but the project could not select it: "+err.Error()
				rows = append(rows, row)
				continue
			}
			if err := func() error {
				if root == "" {
					return removePlaceMCPServer(store, a.Server)
				}
				return forgetPlaceMCP(a.Owner, a.Server, root)
			}(); err != nil {
				row.Result, row.Detail = "failed", err.Error()
				rows = append(rows, row)
				continue
			}
			row.Result, row.Detail = "moved", "Vault connection "+id
			// A copied login is not always accepted by the Vault; say so instead of reporting a working connection.
			if row.Kind != "noauth" {
				if c, err := vaultSavedConnection(ctx, id); err == nil && c.Status != "active" {
					row.Result, row.Detail = "moved, discovery pending", "Vault connection "+id+" is saved; finish its setup through chat and verify discovery"
					if c.OAuthServer != "" {
						row.Result = "moved, sign in again"
					}
				}
			}
			log.Printf("[PLACE_MIGRATION] moved owner=%s connection=%s kind=%s item=%s vault_connection=%s", a.Owner, a.Server, row.Kind, root, id)
			rows = append(rows, row)
		}
	}
	if apply {
		for key := range moved {
			parts := strings.SplitN(key, "\x00", 2)
			store, name := parts[0], parts[1]
			remaining := false
			placeMCPMu.Lock()
			left, _ := readPlaceMCPAttachmentsLocked()
			placeMCPMu.Unlock()
			for root, items := range left {
				for _, a := range items {
					if attachmentStore(a, root) == store && a.Server == name {
						remaining = true
					}
				}
			}
			if !remaining {
				if err := removePlaceMCPServer(store, name); err != nil {
					return rows, err
				}
			}
		}
	}
	for person := range users {
		if person == "" {
			continue
		}
		more, e := api.migrateLegacyPersonalMCPSecrets(ctx, person, apply)
		rows = append(rows, more...)
		if e != nil {
			return rows, e
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
func (api *StreamingAPI) placeMigrationConnection(ctx context.Context, owner, legacyStore string, server placeMCPServer, provider, kind string) (string, error) {
	vaultID, err := ensurePersonalVault(ctx, owner)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(legacyStore + "\x00" + server.Name))
	slug := "migrated-" + hex.EncodeToString(sum[:12])
	data, err := vaultServiceRequest(ctx, owner, http.MethodGet, "/api/admin/connectors", nil)
	if err != nil {
		return "", err
	}
	var existing struct {
		Connectors []struct{ ID, VaultID, InstanceSlug string }
	}
	if json.Unmarshal(data, &existing) != nil {
		return "", fmt.Errorf("Vault inventory unavailable")
	}
	id := ""
	for _, c := range existing.Connectors {
		if c.VaultID == vaultID && c.InstanceSlug == slug {
			id = c.ID
			break
		}
	}
	if id == "" {
		payload := map[string]any{"Import": true, "OAuth": server.OAuth != nil, "Transport": server.Transport, "Name": server.Name, "URL": server.URL, "Label": server.Label, "Slug": slug}
		if len(server.Headers) > 0 {
			headers := map[string]string{}
			for header, ref := range server.Headers {
				value, err := headerSecretValue(legacyStore, ref.Secret)
				if err != nil {
					return "", fmt.Errorf("source credential unavailable")
				}
				format := ref.Format
				if format == "" {
					format = "{}"
				}
				headers[header] = strings.ReplaceAll(format, "{}", value)
			}
			encoded, _ := json.Marshal(headers)
			payload["Credential"] = string(encoded)
			payload["AuthScheme"] = "Headers"
		}
		data, err = vaultPersonRequest(ctx, owner, http.MethodPost, "/api/vaults/"+url.PathEscape(vaultID)+"/connectors", vaultPromoteJSON(payload))
		if err != nil {
			return "", err
		}
		var made struct{ ID string }
		if json.Unmarshal(data, &made) != nil || !vaultConnectionID.MatchString(made.ID) {
			return "", fmt.Errorf("Vault returned no connection identity")
		}
		id = made.ID
	}
	if server.OAuth != nil {
		_, cfg, err := placeMCPServerConfig(legacyStore, server.Name)
		if err != nil {
			return "", err
		}
		oc := *cfg.OAuth
		token, tokenErr := oauth.ReadTokenFile(oc.TokenFile)
		oc.TokenFile = getUserTokenFilePath(platformMCPTokenUserID, vaultCredentialName(id))
		if dir, err := placeMCPDir(legacyStore); err == nil {
			if client, _ := readPlaceMCPClient(dir, legacyStore, server.Name); client != nil {
				oc.ClientID = client.ClientID
				oc.ClientSecret = client.ClientSecret
				oc.ClientSecretFile = ""
				raw, _ := json.Marshal(client)
				if err = oauth.WriteTokenFile(getUserClientFilePath(platformMCPTokenUserID, vaultCredentialName(id)), raw); err != nil {
					return "", err
				}
			}
		}
		raw, _ := json.Marshal(oc)
		if err = oauth.WriteTokenFile(vaultCredentialConfigPath(id), raw); err != nil {
			return "", err
		}
		if tokenErr == nil && len(token) > 0 {
			if err = oauth.WriteTokenFile(oc.TokenFile, token); err != nil {
				return "", err
			}
		}
	}
	// Discovery verifies the imported login; an unavailable upstream remains an
	// honest pending Vault connection, never a second source of credentials.
	_, syncErr := vaultPersonRequest(ctx, owner, http.MethodPost, "/api/vaults/"+url.PathEscape(vaultID)+"/connectors/"+id+"/sync", nil)
	if syncErr != nil {
		log.Printf("[VAULT_MIGRATION] connection=%s discovery=pending", id)
	}
	return id, nil
}

func (api *StreamingAPI) replaceMigratedMCPSelection(ctx context.Context, owner, root, oldName, internal, id string) error {
	if root == "" {
		return nil
	}
	if err := api.selectVaultServer(ctx, owner, root, id, true); err != nil {
		return err
	}
	if err := api.migrateProjectMCPToolSelection(ctx, root, oldName, internal, vaultServerName(id)); err != nil {
		return err
	}
	return api.setProjectSelectedServers(ctx, root, func(current []string) []string {
		out := []string{}
		for _, name := range current {
			if !strings.EqualFold(name, oldName) && name != internal {
				out = append(out, name)
			}
		}
		return out
	})
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

func backupLegacyMCPStore() error {
	root, err := mcpConnectionsRoot()
	if err != nil {
		return err
	}
	if _, err = os.Stat(root); os.IsNotExist(err) {
		return nil
	}
	backup := root + ".vault-backup-" + time.Now().UTC().Format("20060102T150405.000000000")
	return filepath.WalkDir(root, func(source string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, source)
		if err != nil {
			return err
		}
		target := filepath.Join(backup, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("migration store contains a symlink")
		}
		raw, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0600)
	})
}

// Legacy inventory readers below are used by the one-off importer and retired
// store cleanup only. Live setup and execution never resolve these accounts.
func lookupPersonalMCP(person, name string) (placeMCPServer, bool, error) {
	rows, err := listPlaceMCPServers(person)
	if err != nil {
		return placeMCPServer{}, false, err
	}
	for _, row := range rows {
		if strings.EqualFold(row.Name, name) {
			return row, true, nil
		}
	}
	var match placeMCPServer
	count := 0
	for _, row := range rows {
		if name != "" && strings.EqualFold(row.Catalog, name) {
			match = row
			count++
		}
	}
	if count > 1 {
		return match, false, fmt.Errorf("several legacy accounts match; use the exact name")
	}
	return match, count == 1, nil
}

// Keep a project's existing per-tool picks when its account identity moves.
func (api *StreamingAPI) migrateProjectMCPToolSelection(ctx context.Context, root, old, internal, next string) error {
	rewrite := func(items []string) []string {
		out := append([]string{}, items...)
		for i, item := range out {
			server, tool, ok := strings.Cut(item, ":")
			if ok && (strings.EqualFold(server, old) || server == internal) {
				out[i] = next + ":" + tool
			}
		}
		return out
	}
	if isProjectWorkspacePath(root) {
		raw, file, err := ensureProjectRuntimeManifest(ctx, "work", root)
		if err != nil {
			return err
		}
		var manifest map[string]interface{}
		if err = json.Unmarshal([]byte(raw), &manifest); err != nil {
			return err
		}
		caps, _ := manifest["capabilities"].(map[string]interface{})
		if caps == nil {
			return nil
		}
		var items []string
		if source, ok := caps["selected_tools"].([]interface{}); ok {
			for _, item := range source {
				if value, ok := item.(string); ok {
					items = append(items, value)
				}
			}
		}
		if len(items) == 0 {
			return nil
		}
		caps["selected_tools"] = rewrite(items)
		encoded, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return err
		}
		return writeFileToWorkspace(ctx, file, string(encoded)+"\n")
	}
	manifest, found, err := ReadWorkflowManifest(ctx, root)
	if err != nil {
		return err
	}
	if !found || manifest == nil {
		return fmt.Errorf("project manifest unavailable")
	}
	if len(manifest.Capabilities.SelectedTools) == 0 {
		return nil
	}
	caps := manifest.Capabilities
	caps.SelectedTools = rewrite(caps.SelectedTools)
	raw, _ := json.Marshal(UpdateWorkflowManifestRequest{WorkspacePath: root, Capabilities: &caps})
	request := httptest.NewRequest(http.MethodPut, "/api/workflows/manifest", bytes.NewReader(raw)).WithContext(ctx)
	response := httptest.NewRecorder()
	requireWorkflowWriteAccess(api.handleUpdateWorkflowManifest)(response, request)
	if response.Code != 200 {
		return fmt.Errorf("project tool selection could not be migrated")
	}
	return nil
}

func (api *StreamingAPI) migrateLegacySharedMCP(ctx context.Context, apply bool) ([]placeMigrationRow, error) {
	actor := mcpCaller(ctx)
	if actor == "" && !IsMultiUserMode() {
		actor = GetDefaultUserID()
	}
	data, err := vaultServiceRequest(ctx, actor, http.MethodGet, "/api/admin/connectors", nil)
	if err != nil {
		return nil, err
	}
	var inventory struct{ Connectors []vaultOAuthConnection }
	if json.Unmarshal(data, &inventory) != nil {
		return nil, fmt.Errorf("Vault inventory unavailable")
	}
	rows := []placeMigrationRow{}
	for _, c := range inventory.Connectors {
		if c.OAuthServer == "" || c.OAuthCredentialID != "" {
			continue
		}
		row := placeMigrationRow{Owner: actor, Server: c.ID, Kind: "shared OAuth", Result: "would move"}
		if apply {
			if _, err = vaultServiceRequest(ctx, actor, http.MethodPost, "/api/admin/connectors/"+c.ID+"/migrate-oauth", nil); err != nil {
				return rows, err
			}
			status, e := importVaultOAuthCredential(ctx, api, c.ID, true)
			if e != nil {
				row.Result = "failed"
				row.Detail = e.Error()
			} else {
				row.Result = "moved"
				row.Detail = status
				if status != "sign-in required" {
					_, e = vaultServiceRequest(ctx, actor, http.MethodPost, "/api/admin/connectors/"+c.ID+"/sync", nil)
					if e != nil {
						row.Detail = "credentials saved; discovery pending"
					}
				}
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
