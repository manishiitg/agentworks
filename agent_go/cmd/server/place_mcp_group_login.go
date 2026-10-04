package server

import (
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/manishiitg/mcpagent/oauth"
)

// One login per provider group (docs/design/personal_mcp_attach.md, "One
// sign-in per provider"). A person's servers that sign in through the same
// deployment sign-in app (google, github, ...) share one token with the union
// of their scopes, so Gmail, Drive and Calendar need one Google sign-in, and
// adding Docs later asks Google once for just its extra permissions.
//
//	tokens/app-<group>.json   the shared login (sealed like any token)
//	groups.json               { "<group>": ["scope", ...] } consented scopes
//
// A server whose person entered their own OAuth client keeps its own login
// (one token belongs to one client). A server that already has its own
// login keeps using it until the next sign-in, which moves it to the group.

const placeMCPGroupsFile = "groups.json"

func placeMCPGroupTokenFile(dir, group string) string {
	return filepath.Join(dir, "tokens", "app-"+group+".json")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// placeMCPGroupOf is the login group of one of the person's servers, or
// "" when it signs in on its own.
func placeMCPGroupOf(dir, userID string, server placeMCPServer) string {
	if server.OAuth == nil || server.Label != "" {
		return ""
	}
	group := placeMCPAppKey(server)
	if group == "" || fileExists(placeMCPClientFile(dir, userID, server.Name)) {
		return ""
	}
	return group
}

// placeMCPGroupScopes is the union of the scopes of the person's servers
// in group, sorted.
func placeMCPGroupScopes(dir, userID, group string, servers []placeMCPServer) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, server := range servers {
		if placeMCPGroupOf(dir, userID, server) != group {
			continue
		}
		for _, scope := range server.OAuth.Scopes {
			if !seen[scope] {
				seen[scope] = true
				out = append(out, scope)
			}
		}
	}
	sort.Strings(out)
	return out
}

func readPlaceMCPGroupConsent(dir string) map[string][]string {
	consent := map[string][]string{}
	_ = readPlaceMCPJSON(filepath.Join(dir, placeMCPGroupsFile), &consent)
	return consent
}

// recordPlaceMCPGroupConsent notes the scopes a group sign-in granted.
func recordPlaceMCPGroupConsent(dir, group string, scopes []string) error {
	placeMCPMu.Lock()
	defer placeMCPMu.Unlock()
	consent := readPlaceMCPGroupConsent(dir)
	consent[group] = append([]string(nil), scopes...)
	return writePlaceMCPJSON(filepath.Join(dir, placeMCPGroupsFile), consent)
}

// placeMCPServerConnected reports whether the server has a usable login:
// its group's login covering its scopes, or its own login.
func placeMCPServerConnected(dir, userID string, server placeMCPServer) bool {
	if server.OAuth == nil {
		return true
	}
	own := placeMCPTokenFile(dir, userID, server.Name)
	group := placeMCPGroupOf(dir, userID, server)
	if group == "" || (fileExists(own) && !fileExists(placeMCPGroupTokenFile(dir, group))) {
		_, err := oauth.NewTokenStore(own).Load()
		return err == nil
	}
	if _, err := oauth.NewTokenStore(placeMCPGroupTokenFile(dir, group)).Load(); err != nil {
		return false
	}
	granted := map[string]bool{}
	for _, scope := range readPlaceMCPGroupConsent(dir)[group] {
		granted[scope] = true
	}
	for _, scope := range server.OAuth.Scopes {
		if !granted[scope] {
			return false
		}
	}
	return true
}

// placeMCPGroupMembers lists the names of the person's servers in group.
func placeMCPGroupMembers(dir, userID, group string, servers []placeMCPServer) []string {
	names := []string{}
	for _, server := range servers {
		if placeMCPGroupOf(dir, userID, server) == group {
			names = append(names, server.Name)
		}
	}
	return names
}

// forgetUnusedPlaceMCPGroups deletes group logins no remaining server
// (other than except) uses.
func forgetUnusedPlaceMCPGroups(dir, userID, except string) error {
	servers, err := listPlaceMCPServers(userID)
	if err != nil {
		return err
	}
	placeMCPMu.Lock()
	defer placeMCPMu.Unlock()
	consent := readPlaceMCPGroupConsent(dir)
	changed := false
	for group := range consent {
		used := false
		for _, server := range servers {
			if server.Name != except && placeMCPGroupOf(dir, userID, server) == group {
				used = true
			}
		}
		if used {
			continue
		}
		if err := os.Remove(placeMCPGroupTokenFile(dir, group)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		delete(consent, group)
		changed = true
	}
	if !changed {
		return nil
	}
	return writePlaceMCPJSON(filepath.Join(dir, placeMCPGroupsFile), consent)
}
