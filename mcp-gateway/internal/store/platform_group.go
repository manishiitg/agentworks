package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"time"
)

// PlatformGroupID is stable and workspace-specific, including on reinstall.
// Keep the original prefix so changing the display name preserves grants.
func PlatformGroupID(workspace string) string {
	hash := sha256.Sum256([]byte(workspace))
	return "everyone-" + hex.EncodeToString(hash[:12])
}

// EnsurePlatformGroup seeds an empty built-in group and backfills membership.
// Re-running installation or startup preserves every existing grant.
func (s *MemoryStore) EnsurePlatformGroup(workspace string) error {
	s.mu.Lock()
	if _, ok := s.workspaces[workspace]; !ok {
		s.mu.Unlock()
		return errors.New("unknown workspace")
	}
	id := PlatformGroupID(workspace)
	group, exists := s.groups[id]
	if exists && (!group.BuiltIn || group.WorkspaceID != workspace) {
		s.mu.Unlock()
		return errors.New("built-in group identity is already in use")
	}
	if !exists {
		s.groups[id] = Group{ID: id, WorkspaceID: workspace, Name: "Platform", Description: "All platform users. Share MCP tools and secrets across Code, Crew, and workflows.", BuiltIn: true}
	} else if group.Name == "Everyone" {
		group.Name = "Platform"
		s.groups[id] = group
	}
	if s.members[id] == nil {
		s.members[id] = map[string]bool{}
	}
	for uid, user := range s.users {
		if user.WorkspaceID == workspace {
			s.members[id][uid] = true
		}
	}
	if revoked := s.revokeVaultOwnedFromPlatformLocked(workspace); revoked > 0 {
		log.Printf("Vault: removed %d company-wide grants from person-owned vault connections and secrets", revoked)
	}
	secrets, servers := s.grantPlatformDefaultsLocked(workspace)
	s.persistUnlock()
	if secrets+servers > 0 {
		log.Printf("Vault: Platform group granted %d shared secrets and %d MCP servers that existed without a grant", secrets, servers)
	}
	return s.PersistenceError()
}

// EnsurePlatformUser binds an identity that the host has already authenticated.
// It cannot move another workspace's user or erase existing metadata.
func (s *MemoryStore) EnsurePlatformUser(workspace, id string) error {
	s.mu.Lock()
	if _, ok := s.workspaces[workspace]; !ok {
		s.mu.Unlock()
		return errors.New("unknown workspace")
	}
	user, exists := s.users[id]
	if exists && user.WorkspaceID != workspace {
		s.mu.Unlock()
		return errors.New("user belongs to another workspace")
	}
	group := PlatformGroupID(workspace)
	if !s.groups[group].BuiltIn {
		s.mu.Unlock()
		return errors.New("platform group unavailable")
	}
	if exists && s.members[group][id] {
		s.mu.Unlock()
		return s.PersistenceError()
	}
	if !exists {
		s.users[id] = User{ID: id, WorkspaceID: workspace}
	}
	if s.members[group] == nil {
		s.members[group] = map[string]bool{}
	}
	s.members[group][id] = true
	s.persistUnlock()
	return s.PersistenceError()
}

// The Platform group automatically has every shared secret and MCP server of
// its workspace, so installing or upgrading Vault changes nothing for people who
// could use them before. Admin removals are remembered in platformRevoked and
// are never undone here. Only items that exist are granted; no value is created.
// A server grant covers all of the server's tools, including ones discovered
// later, so no per-tool grant is written (it would survive detaching the server).
const platformAutoGrantActor = "system:platform-auto-grant"

// isPlatformGroupLocked reports whether id is some workspace's built-in Platform group.
func (s *MemoryStore) isPlatformGroupLocked(id string) bool {
	g, ok := s.groups[id]
	return ok && g.BuiltIn && g.WorkspaceID != "" && PlatformGroupID(g.WorkspaceID) == id
}

func (s *MemoryStore) autoGrantSecretLocked(workspace, name string) bool {
	// A secret that belongs to a person-owned vault is shared only through that vault (PLAT-507).
	if s.secretResources[name].VaultID != "" {
		return false
	}
	id := PlatformGroupID(workspace)
	if !s.isPlatformGroupLocked(id) || s.platformRevoked["secret:"+name] || s.secretGrants[id][name] {
		return false
	}
	if s.secretGrants[id] == nil {
		s.secretGrants[id] = map[string]bool{}
	}
	s.secretGrants[id][name] = true
	s.policyEvents[workspace] = append(s.policyEvents[workspace], PolicyEvent{At: time.Now().UTC(), Actor: platformAutoGrantActor, Action: "grant_secret", PackageID: name, GroupID: id})
	return true
}

func (s *MemoryStore) autoGrantServerLocked(c Connector) bool {
	// A connection that belongs to a person-owned vault is shared only through that vault (PLAT-507).
	if c.VaultID != "" {
		return false
	}
	id := PlatformGroupID(c.WorkspaceID)
	if !s.isPlatformGroupLocked(id) || s.platformRevoked["server:"+c.ID] || s.groupServers[id][c.ID] {
		return false
	}
	if s.groupServers[id] == nil {
		s.groupServers[id] = map[string]bool{}
	}
	s.groupServers[id][c.ID] = true
	s.policyEvents[c.WorkspaceID] = append(s.policyEvents[c.WorkspaceID], PolicyEvent{At: time.Now().UTC(), Actor: platformAutoGrantActor, Action: "attach_server_to_group", GroupID: id, ConnectorID: c.ID})
	return true
}

func workspace(c Connector) string { return c.WorkspaceID }

func (s *MemoryStore) grantPlatformDefaultsLocked(ws string) (secrets, servers int) {
	for name, row := range s.secretResources {
		if row.WorkspaceID == ws && s.autoGrantSecretLocked(ws, name) {
			secrets++
		}
	}
	for _, c := range s.connectors {
		if c.WorkspaceID == ws && s.autoGrantServerLocked(c) {
			servers++
		}
	}
	return secrets, servers
}

// revokeVaultOwnedFromPlatformLocked removes any grant of a person-owned vault's connection or secret to the Platform
// group (a connection used to be granted to everyone when it was created, before it was bound to its vault) and
// remembers the removal so nothing grants it again (PLAT-507). It runs at every start and changes nothing when clean.
func (s *MemoryStore) revokeVaultOwnedFromPlatformLocked(workspace string) int {
	id := PlatformGroupID(workspace)
	revoked := 0
	for cid, c := range s.connectors {
		if c.WorkspaceID != workspace || c.VaultID == "" {
			continue
		}
		if s.groupServers[id][cid] {
			delete(s.groupServers[id], cid)
			delete(s.serverReadOnly[id], cid)
			revoked++
		}
		s.platformRevoked["server:"+cid] = true
	}
	for name, secret := range s.secretResources {
		if secret.WorkspaceID != workspace || secret.VaultID == "" {
			continue
		}
		if s.secretGrants[id][name] {
			delete(s.secretGrants[id], name)
			revoked++
		}
		s.platformRevoked["secret:"+name] = true
	}
	return revoked
}
