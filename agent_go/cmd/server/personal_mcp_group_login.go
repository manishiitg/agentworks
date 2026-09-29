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

const personalMCPGroupsFile = "groups.json"

func personalMCPGroupTokenFile(dir, group string) string {
	return filepath.Join(dir, "tokens", "app-"+group+".json")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// personalMCPGroupOf is the login group of one of the person's servers, or
// "" when it signs in on its own.
func personalMCPGroupOf(dir, userID string, server personalMCPServer) string {
	if server.OAuth == nil {
		return ""
	}
	group := personalMCPAppKey(server)
	if group == "" || fileExists(personalMCPClientFile(dir, userID, server.Name)) {
		return ""
	}
	return group
}

// personalMCPGroupScopes is the union of the scopes of the person's servers
// in group, sorted.
func personalMCPGroupScopes(dir, userID, group string, servers []personalMCPServer) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, server := range servers {
		if personalMCPGroupOf(dir, userID, server) != group {
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

func readPersonalMCPGroupConsent(dir string) map[string][]string {
	consent := map[string][]string{}
	_ = readPersonalMCPJSON(filepath.Join(dir, personalMCPGroupsFile), &consent)
	return consent
}

// recordPersonalMCPGroupConsent notes the scopes a group sign-in granted.
func recordPersonalMCPGroupConsent(dir, group string, scopes []string) error {
	personalMCPMu.Lock()
	defer personalMCPMu.Unlock()
	consent := readPersonalMCPGroupConsent(dir)
	consent[group] = append([]string(nil), scopes...)
	return writePersonalMCPJSON(filepath.Join(dir, personalMCPGroupsFile), consent)
}

// personalMCPServerConnected reports whether the server has a usable login:
// its group's login covering its scopes, or its own login.
func personalMCPServerConnected(dir, userID string, server personalMCPServer) bool {
	if server.OAuth == nil {
		return true
	}
	own := personalMCPTokenFile(dir, userID, server.Name)
	group := personalMCPGroupOf(dir, userID, server)
	if group == "" || (fileExists(own) && !fileExists(personalMCPGroupTokenFile(dir, group))) {
		_, err := oauth.NewTokenStore(own).Load()
		return err == nil
	}
	if _, err := oauth.NewTokenStore(personalMCPGroupTokenFile(dir, group)).Load(); err != nil {
		return false
	}
	granted := map[string]bool{}
	for _, scope := range readPersonalMCPGroupConsent(dir)[group] {
		granted[scope] = true
	}
	for _, scope := range server.OAuth.Scopes {
		if !granted[scope] {
			return false
		}
	}
	return true
}

// personalMCPGroupMembers lists the names of the person's servers in group.
func personalMCPGroupMembers(dir, userID, group string, servers []personalMCPServer) []string {
	names := []string{}
	for _, server := range servers {
		if personalMCPGroupOf(dir, userID, server) == group {
			names = append(names, server.Name)
		}
	}
	return names
}

// forgetUnusedPersonalMCPGroups deletes group logins no remaining server
// (other than except) uses.
func forgetUnusedPersonalMCPGroups(dir, userID, except string) error {
	servers, err := listPersonalMCPServers(userID)
	if err != nil {
		return err
	}
	personalMCPMu.Lock()
	defer personalMCPMu.Unlock()
	consent := readPersonalMCPGroupConsent(dir)
	changed := false
	for group := range consent {
		used := false
		for _, server := range servers {
			if server.Name != except && personalMCPGroupOf(dir, userID, server) == group {
				used = true
			}
		}
		if used {
			continue
		}
		if err := os.Remove(personalMCPGroupTokenFile(dir, group)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		delete(consent, group)
		changed = true
	}
	if !changed {
		return nil
	}
	return writePersonalMCPJSON(filepath.Join(dir, personalMCPGroupsFile), consent)
}
