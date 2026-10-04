package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	s.persistUnlock()
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
