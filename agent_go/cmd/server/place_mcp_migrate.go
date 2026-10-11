package server

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/mcpagent/oauth"
)

// One-time move of the old "personal" Code connections onto place
// connections (docs/design/personal_mcp_attach.md): a Code is a place like a
// Crew, so what a person had switched on for a Code becomes that Code's own
// connection, with its login. It is safe to run on every start: a connection
// already moved is skipped, and nothing is deleted from the person's old store
// (it is dormant and can be cleaned up once the move is verified).
//
// Not moved: a person's switch for someone else's Code (a place's connections
// belong to its owner), and header-server secrets are copied into the Code's
// project secrets so the connection keeps working.

// migrateCodePersonalMCP runs the move for every known person.
//
//nolint:unused // its caller was dropped in the Vault checkpoint; kept for its owner (PLAT-466 only unblocks the cmd/server lint gate)
func (api *StreamingAPI) migrateCodePersonalMCP() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	people := map[string]bool{GetDefaultUserID(): true}
	if dir, err := loadUserDirectory(); err == nil && dir != nil {
		for _, user := range dir.Users {
			if strings.TrimSpace(user.ID) != "" {
				people[user.ID] = true
			}
		}
	}
	ids := make([]string, 0, len(people))
	for id := range people {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	moved := 0
	for _, person := range ids {
		n, err := api.migrateCodePersonalMCPFor(ctx, person)
		if err != nil {
			log.Printf("[MCP_MIGRATE] %s: %v", person, err)
		}
		moved += n
	}
	if moved > 0 {
		log.Printf("[MCP_MIGRATE] moved %d Code connection(s) onto place connections", moved)
	}
}

func (api *StreamingAPI) migrateCodePersonalMCPFor(ctx context.Context, person string) (int, error) {
	dir, err := placeMCPDir(person)
	if err != nil {
		return 0, err
	}
	enabled := map[string][]string{}
	placeMCPMu.Lock()
	err = readPlaceMCPJSON(filepath.Join(dir, "enabled.json"), &enabled)
	placeMCPMu.Unlock()
	if err != nil || len(enabled) == 0 {
		return 0, err
	}
	servers, err := listPlaceMCPServers(person)
	if err != nil {
		return 0, err
	}
	byName := map[string]placeMCPServer{}
	for _, server := range servers {
		byName[server.Name] = server
	}
	roots := make([]string, 0, len(enabled))
	for root := range enabled {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	loginCopied := map[string]bool{} // a login moves to the first Code only
	moved := 0
	for _, raw := range roots {
		root := cleanAttachRoot(raw)
		if root == "" || !placeMCPCanAttach(ctx, person, root) {
			log.Printf("[MCP_MIGRATE] %s: not moving connections switched on in %s (not their own Code; connections belong to a Code's owner)", person, raw)
			continue
		}
		remaining := []string{}
		for _, name := range enabled[raw] {
			server, ok := byName[name]
			if !ok {
				continue
			}
			if err := api.moveCodeConnection(ctx, person, dir, server, root, !loginCopied[name]); err != nil {
				log.Printf("[MCP_MIGRATE] %s: could not move %s to %s: %v", person, name, root, err)
				remaining = append(remaining, name)
				continue
			}
			loginCopied[name] = true
			moved++
		}
		if err := setPersonalMCPEnabledNames(person, raw, remaining); err != nil {
			log.Printf("[MCP_MIGRATE] %s: could not record the move for %s: %v", person, raw, err)
		}
	}
	return moved, nil
}

// moveCodeConnection copies one server, its login (when withLogin) and its
// header secrets to the Code's own place store, then attaches it.
func (api *StreamingAPI) moveCodeConnection(ctx context.Context, person, fromDir string, server placeMCPServer, root string, withLogin bool) error {
	store := placeMCPStoreID(person, root)
	existing, _ := listPlaceMCPServers(store)
	for _, have := range existing {
		if have.Name == server.Name {
			return recordPlaceMCP(person, server.Name, root) // already moved
		}
	}
	toDir, err := placeMCPDir(store)
	if err != nil {
		return err
	}
	for _, ref := range server.Headers {
		if err := api.copyPersonalSecretToProject(ctx, person, root, ref.Secret); err != nil {
			return err
		}
	}
	if _, err := addPlaceMCPServer(store, server); err != nil {
		return err
	}
	if withLogin {
		files := [][2]string{
			{placeMCPTokenFile(fromDir, person, server.Name), placeMCPTokenFile(toDir, store, server.Name)},
			{placeMCPClientFile(fromDir, person, server.Name), placeMCPClientFile(toDir, store, server.Name)},
		}
		if group := placeMCPGroupOf(fromDir, person, server); group != "" {
			files = append(files, [2]string{placeMCPGroupTokenFile(fromDir, group), placeMCPGroupTokenFile(toDir, group)})
			if scopes := readPlaceMCPGroupConsent(fromDir)[group]; len(scopes) > 0 {
				if err := recordPlaceMCPGroupConsent(toDir, group, scopes); err != nil {
					return err
				}
			}
		}
		for _, pair := range files {
			if err := resealFile(pair[0], pair[1]); err != nil {
				return err
			}
		}
	}
	return recordPlaceMCP(person, server.Name, root)
}

// resealFile copies a sealed login file to its new path (each is bound to its
// own path), doing nothing when the source does not exist.
func resealFile(from, to string) error {
	data, err := oauth.ReadTokenFile(from)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return oauth.WriteTokenFile(to, data)
}

// copyPersonalSecretToProject makes a header server's secret a secret of the
// Code, unless the Code already has one of that name.
func (api *StreamingAPI) copyPersonalSecretToProject(ctx context.Context, person, root, name string) error {
	if api.chatStore == nil {
		return errors.New("project secrets are unavailable")
	}
	if _, err := api.projectSecretValue(root, name); err == nil {
		return nil
	}
	value, err := personalSecretValue(person, name)
	if err != nil {
		return err
	}
	return api.upsertSharedWorkflowSecret(ctx, root, name, value)
}
