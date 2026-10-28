package store

import (
	"errors"
	"fmt"
	"sort"
)

// One-off conversion of the retired person-created vaults (PLAT-856-400): a vault only its owner uses is merged into
// that person's personal vault; a vault shared with other people becomes an ordinary administrator-managed group with
// the same members, connections and secrets. Remove this file once every server has converted.

// PersonVault is a non-project vault as the administrator's conversion sees it.
type PersonVault struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Owners       []string `json:"owners"`
	Members      []string `json:"members"`
	ConnectorIDs []string `json:"connector_ids"`
	SecretNames  []string `json:"secret_names"`
}

// VaultConversion is one step of the plan. Mode "merge" moves everything into TargetID (the owner's personal vault,
// created when it does not exist yet); mode "company" turns the vault into a plain group. SecretRenames maps each of
// the vault's secret names to its new name; the host has already stored the values under the new names.
type VaultConversion struct {
	VaultID       string            `json:"vault_id"`
	Mode          string            `json:"mode"`
	TargetID      string            `json:"target_id,omitempty"`
	SecretRenames map[string]string `json:"secret_renames,omitempty"`
}

// PersonVaults lists every non-project vault in workspace.
func (s *MemoryStore) PersonVaults(workspace string) []PersonVault {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []PersonVault{}
	for _, g := range s.groups {
		if g.WorkspaceID != workspace || !g.IsVault() || g.ProjectRoot != "" {
			continue
		}
		v := PersonVault{ID: g.ID, Name: g.Name, Owners: append([]string{}, g.Owners...), Members: []string{}, ConnectorIDs: []string{}, SecretNames: []string{}}
		for member := range s.members[g.ID] {
			v.Members = append(v.Members, member)
		}
		for id, c := range s.connectors {
			if c.VaultID == g.ID {
				v.ConnectorIDs = append(v.ConnectorIDs, id)
			}
		}
		for name, secret := range s.secretResources {
			if secret.VaultID == g.ID {
				v.SecretNames = append(v.SecretNames, name)
			}
		}
		sort.Strings(v.Owners)
		sort.Strings(v.Members)
		sort.Strings(v.ConnectorIDs)
		sort.Strings(v.SecretNames)
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ConvertPersonVaults applies the whole plan or nothing.
func (s *MemoryStore) ConvertPersonVaults(workspace string, plan []VaultConversion) error {
	s.mu.Lock()
	defer s.persistUnlock()
	for _, step := range plan {
		if err := s.checkVaultConversionLocked(workspace, step); err != nil {
			return fmt.Errorf("%s: %w", step.VaultID, err)
		}
	}
	for _, step := range plan {
		s.applyVaultConversionLocked(workspace, step)
	}
	return nil
}

func (s *MemoryStore) checkVaultConversionLocked(workspace string, step VaultConversion) error {
	g, ok := s.groups[step.VaultID]
	if !ok || g.WorkspaceID != workspace || !g.IsVault() || g.ProjectRoot != "" {
		return errors.New("not a personal vault")
	}
	seen := map[string]bool{}
	for old, renamed := range step.SecretRenames {
		if row, ok := s.secretResources[old]; !ok || row.VaultID != g.ID {
			return fmt.Errorf("secret %s is not in this vault", old)
		}
		if _, taken := s.secretResources[renamed]; (taken && renamed != old) || seen[renamed] || renamed == "" {
			return fmt.Errorf("secret name %s is already used", renamed)
		}
		seen[renamed] = true
	}
	for name, row := range s.secretResources {
		if row.VaultID == g.ID {
			if _, ok := step.SecretRenames[name]; !ok {
				return fmt.Errorf("secret %s has no new name", name)
			}
		}
	}
	switch step.Mode {
	case "company":
		return nil
	case "merge":
		if len(g.Owners) != 1 || step.TargetID == "" || step.TargetID == g.ID {
			return errors.New("a merge needs one owner and another target vault")
		}
		if t, exists := s.groups[step.TargetID]; exists && (!t.IsVault() || t.ProjectRoot != "" || t.Name != PersonalVaultName || len(t.Owners) != 1 || t.Owners[0] != g.Owners[0]) {
			return errors.New("the target is not this owner's personal vault")
		}
		return nil
	}
	return errors.New("mode must be merge or company")
}

func (s *MemoryStore) applyVaultConversionLocked(workspace string, step VaultConversion) {
	g := s.groups[step.VaultID]
	dest, vaultID := step.VaultID, ""
	if step.Mode == "merge" {
		dest, vaultID = step.TargetID, step.TargetID
		if _, exists := s.groups[dest]; !exists {
			s.groups[dest] = Group{ID: dest, WorkspaceID: workspace, Name: PersonalVaultName, Description: "Your own accounts, used only by you and the projects you build.", Kind: GroupKindVault, Owners: []string{g.Owners[0]}}
			s.members[dest] = map[string]bool{g.Owners[0]: true}
		}
	}
	for id, c := range s.connectors {
		if c.VaultID != g.ID {
			continue
		}
		c.VaultID = vaultID
		s.connectors[id] = c
		if s.groupServers[dest] == nil {
			s.groupServers[dest] = map[string]bool{}
		}
		s.groupServers[dest][id] = true
		// A company connection reaches only this group, never everyone through the Platform group.
		s.platformRevoked["server:"+id] = true
	}
	for old, renamed := range step.SecretRenames {
		row := s.secretResources[old]
		delete(s.secretResources, old)
		row.Name, row.VaultID = renamed, vaultID
		s.secretResources[renamed] = row
		for _, grants := range s.secretGrants {
			delete(grants, old)
		}
		if s.secretGrants[dest] == nil {
			s.secretGrants[dest] = map[string]bool{}
		}
		s.secretGrants[dest][renamed] = true
		s.platformRevoked["secret:"+renamed] = true
	}
	if step.Mode == "company" {
		g.Kind, g.Owners = "", nil
		s.groups[g.ID] = g
		return
	}
	for name := range s.groupGrants[g.ID] {
		if s.groupGrants[dest] == nil {
			s.groupGrants[dest] = map[string]bool{}
		}
		s.groupGrants[dest][name] = true
	}
	delete(s.groups, g.ID)
	delete(s.members, g.ID)
	delete(s.groupGrants, g.ID)
	delete(s.groupServers, g.ID)
	delete(s.serverReadOnly, g.ID)
	delete(s.secretGrants, g.ID)
}
