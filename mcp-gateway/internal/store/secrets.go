package store

import (
	"errors"
	"sort"
	"time"
)

// SecretResource is metadata only. Values remain in the host's encrypted secret store.
// Names are immutable reference IDs; rotation never changes a project's binding.
type SecretResource struct {
	Name        string `json:"name"`
	WorkspaceID string `json:"workspace_id"`
	Managed     bool   `json:"managed"`
}

func (s *MemoryStore) RegisterSecrets(workspace string, rows []SecretResource) error {
	s.mu.Lock()
	if _, ok := s.workspaces[workspace]; !ok {
		s.mu.Unlock()
		return errors.New("unknown workspace")
	}
	for _, row := range rows {
		if old, ok := s.secretResources[row.Name]; ok && old.WorkspaceID != workspace {
			s.mu.Unlock()
			return errors.New("secret belongs to another workspace")
		}
	}
	for _, row := range rows {
		row.WorkspaceID = workspace
		s.secretResources[row.Name] = row
	}
	s.persistUnlock()
	return s.PersistenceError()
}
func (s *MemoryStore) DeleteSecret(workspace, name, actor string) error {
	s.mu.Lock()
	if row, ok := s.secretResources[name]; ok && row.WorkspaceID != workspace {
		s.mu.Unlock()
		return errors.New("secret belongs to another workspace")
	}
	delete(s.secretResources, name)
	for _, grants := range s.secretGrants {
		delete(grants, name)
	}
	s.policyEvents[workspace] = append(s.policyEvents[workspace], PolicyEvent{At: time.Now().UTC(), Actor: actor, Action: "delete_secret", PackageID: name})
	s.persistUnlock()
	return s.PersistenceError()
}
func (s *MemoryStore) ListSecrets(workspace, user, group string, all bool) []SecretResource {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows := []SecretResource{}
	for _, row := range s.secretResources {
		if row.WorkspaceID != workspace {
			continue
		}
		allowed := all
		if group != "" {
			g, ok := s.groups[group]
			allowed = ok && g.WorkspaceID == workspace && s.secretGrants[group][row.Name]
		}
		if !all && group == "" {
			u, ok := s.users[user]
			if ok && u.WorkspaceID == workspace {
				for gid, members := range s.members {
					g, exists := s.groups[gid]
					if exists && g.WorkspaceID == workspace && members[user] && s.secretGrants[gid][row.Name] {
						allowed = true
					}
				}
			}
		}
		if allowed {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

// SetSecretGrants validates the entire selection before changing any grants.
// It adds access only to the supplied groups; existing grants remain unchanged.
func (s *MemoryStore) SetSecretGrants(workspace, name, actor string, groups []string, allow bool) error {
	s.mu.Lock()
	row, exists := s.secretResources[name]
	if !exists || row.WorkspaceID != workspace || len(groups) == 0 || len(groups) > 100 {
		s.mu.Unlock()
		return errors.New("unknown secret or invalid group selection")
	}
	for _, group := range groups {
		g, ok := s.groups[group]
		if !ok || g.WorkspaceID != workspace {
			s.mu.Unlock()
			return errors.New("unknown group or secret")
		}
	}
	seen := map[string]bool{}
	for _, group := range groups {
		if seen[group] {
			continue
		}
		seen[group] = true
		if s.secretGrants[group] == nil {
			s.secretGrants[group] = map[string]bool{}
		}
		if allow {
			s.secretGrants[group][name] = true
		} else {
			delete(s.secretGrants[group], name)
		}
		action := "revoke_secret"
		if allow {
			action = "grant_secret"
		}
		s.policyEvents[workspace] = append(s.policyEvents[workspace], PolicyEvent{At: time.Now().UTC(), Actor: actor, Action: action, PackageID: name, GroupID: group})
	}
	s.persistUnlock()
	return s.PersistenceError()
}
func (s *MemoryStore) SetSecretGrant(workspace, group, name, actor string, allow bool) error {
	return s.SetSecretGrants(workspace, name, actor, []string{group}, allow)
}
