package store

import (
	"errors"
	"sort"
	"strings"
	"unicode/utf8"
)

// Person-owned vaults (PLAT-507). A vault is a group with owners: a bundle of connections that its owners manage and its
// members use. Only an owner adds, removes or updates what is in it and decides who may use it; a member cannot re-share.
// The platform Vault stays an ordinary administrator-managed set of groups.
const (
	GroupKindVault = "vault"
	// MaxVaultsPerOwner is how many vaults one person may own (owner decision, 2026-10-05).
	MaxVaultsPerOwner = 5
)

func (g Group) IsVault() bool { return g.Kind == GroupKindVault }

func (g Group) HasOwner(userID string) bool {
	for _, owner := range g.Owners {
		if owner == userID {
			return true
		}
	}
	return false
}

// VaultSummary is a vault as one person sees it.
type VaultSummary struct {
	Group        Group    `json:"group"`
	Role         string   `json:"role"` // "owner" or "member"
	Members      []string `json:"members"`
	ConnectorIDs []string `json:"connector_ids"`
}

// CreateVault makes a vault owned by ownerID, who is also its first member. A person owns at most MaxVaultsPerOwner.
func (s *MemoryStore) CreateVault(workspace, ownerID, id, name, description string) error {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return errors.New("a vault needs a name of 1 to 100 characters")
	}
	if utf8.RuneCountInString(description) > MaxGroupDescriptionLength {
		return errors.New("vault description must be at most 1000 characters")
	}
	s.mu.Lock()
	defer s.persistUnlock()
	if u, ok := s.users[ownerID]; !ok || u.WorkspaceID != workspace {
		return errors.New("unknown user")
	}
	if _, exists := s.groups[id]; exists {
		return errors.New("vault exists")
	}
	owned := 0
	for _, g := range s.groups {
		if g.WorkspaceID == workspace && g.IsVault() && g.HasOwner(ownerID) {
			owned++
		}
	}
	if owned >= MaxVaultsPerOwner {
		return errors.New("you already own the maximum number of vaults (5)")
	}
	s.groups[id] = Group{ID: id, WorkspaceID: workspace, Name: name, Description: description, Kind: GroupKindVault, Owners: []string{ownerID}}
	if s.members[id] == nil {
		s.members[id] = map[string]bool{}
	}
	s.members[id][ownerID] = true
	return nil
}

// VaultsFor lists the vaults userID owns or belongs to.
func (s *MemoryStore) VaultsFor(workspace, userID string) []VaultSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []VaultSummary{}
	for _, g := range s.groups {
		if g.WorkspaceID != workspace || !g.IsVault() {
			continue
		}
		owner := g.HasOwner(userID)
		if !owner && !s.members[g.ID][userID] {
			continue
		}
		role := "member"
		if owner {
			role = "owner"
		}
		view := VaultSummary{Group: g, Role: role, Members: []string{}, ConnectorIDs: []string{}}
		for member := range s.members[g.ID] {
			view.Members = append(view.Members, member)
		}
		for _, c := range s.connectors {
			if c.VaultID == g.ID {
				view.ConnectorIDs = append(view.ConnectorIDs, c.ID)
			}
		}
		sort.Strings(view.Members)
		sort.Strings(view.ConnectorIDs)
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Group.Name < out[j].Group.Name })
	return out
}

// ownedVaultLocked returns the vault when actor owns it.
func (s *MemoryStore) ownedVaultLocked(actor, vaultID string) (Group, error) {
	g, ok := s.groups[vaultID]
	if !ok || !g.IsVault() {
		return Group{}, errors.New("unknown vault")
	}
	if !g.HasOwner(actor) {
		return Group{}, errors.New("only an owner of this vault can do that")
	}
	return g, nil
}

// VaultOwnedBy reports whether actor owns vaultID.
func (s *MemoryStore) VaultOwnedBy(actor, vaultID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, err := s.ownedVaultLocked(actor, vaultID)
	return err == nil
}

// VaultSetMember adds or removes a member. Only an owner may; an owner stays a member while they own the vault.
func (s *MemoryStore) VaultSetMember(actor, vaultID, userID string, add bool) error {
	s.mu.Lock()
	defer s.persistUnlock()
	g, err := s.ownedVaultLocked(actor, vaultID)
	if err != nil {
		return err
	}
	if u, ok := s.users[userID]; !ok || u.WorkspaceID != g.WorkspaceID {
		return errors.New("unknown user")
	}
	if add {
		if s.members[vaultID] == nil {
			s.members[vaultID] = map[string]bool{}
		}
		s.members[vaultID][userID] = true
		return nil
	}
	if g.HasOwner(userID) {
		return errors.New("remove them as an owner first")
	}
	delete(s.members[vaultID], userID)
	return nil
}

// VaultSetOwner adds or removes an owner. Only an owner may; a vault always keeps at least one owner. A new owner is
// also made a member. A person may not be made an owner of more than MaxVaultsPerOwner vaults.
func (s *MemoryStore) VaultSetOwner(actor, vaultID, userID string, add bool) error {
	s.mu.Lock()
	defer s.persistUnlock()
	g, err := s.ownedVaultLocked(actor, vaultID)
	if err != nil {
		return err
	}
	if u, ok := s.users[userID]; !ok || u.WorkspaceID != g.WorkspaceID {
		return errors.New("unknown user")
	}
	if add {
		if g.HasOwner(userID) {
			return nil
		}
		owned := 0
		for _, other := range s.groups {
			if other.WorkspaceID == g.WorkspaceID && other.IsVault() && other.HasOwner(userID) {
				owned++
			}
		}
		if owned >= MaxVaultsPerOwner {
			return errors.New("that person already owns the maximum number of vaults (5)")
		}
		g.Owners = append(append([]string{}, g.Owners...), userID)
		s.groups[vaultID] = g
		if s.members[vaultID] == nil {
			s.members[vaultID] = map[string]bool{}
		}
		s.members[vaultID][userID] = true
		return nil
	}
	if !g.HasOwner(userID) {
		return nil
	}
	if len(g.Owners) <= 1 {
		return errors.New("a vault must keep at least one owner")
	}
	remaining := make([]string, 0, len(g.Owners)-1)
	for _, owner := range g.Owners {
		if owner != userID {
			remaining = append(remaining, owner)
		}
	}
	g.Owners = remaining
	s.groups[vaultID] = g
	return nil
}

// VaultAttachConnector puts a just-created connection into a vault the actor owns and lets the vault's members use all
// its tools. It refuses a connection that already belongs to a vault or to the platform's own, so an owner cannot take
// over an existing connection: the vault routes call it only right after creating the connection themselves.
func (s *MemoryStore) VaultAttachConnector(actor, vaultID, connectorID string) error {
	s.mu.Lock()
	defer s.persistUnlock()
	g, err := s.ownedVaultLocked(actor, vaultID)
	if err != nil {
		return err
	}
	c, ok := s.connectors[connectorID]
	if !ok || c.WorkspaceID != g.WorkspaceID {
		return errors.New("unknown connector")
	}
	if c.VaultID != "" {
		return errors.New("connection already belongs to a vault")
	}
	c.VaultID = vaultID
	s.connectors[connectorID] = c
	if s.groupServers[vaultID] == nil {
		s.groupServers[vaultID] = map[string]bool{}
	}
	s.groupServers[vaultID][connectorID] = true
	return nil
}

// VaultConnectorOwnedBy reports whether connectorID belongs to a vault actor owns.
func (s *MemoryStore) VaultConnectorOwnedBy(actor, vaultID, connectorID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := s.ownedVaultLocked(actor, vaultID); err != nil {
		return false
	}
	c, ok := s.connectors[connectorID]
	return ok && c.VaultID == vaultID
}

// DeleteVault removes an empty vault the actor owns: its connections must be removed first, so nothing keeps working
// without an owner.
func (s *MemoryStore) DeleteVault(actor, vaultID string) error {
	s.mu.Lock()
	defer s.persistUnlock()
	if _, err := s.ownedVaultLocked(actor, vaultID); err != nil {
		return err
	}
	for _, c := range s.connectors {
		if c.VaultID == vaultID {
			return errors.New("remove this vault's connections first")
		}
	}
	delete(s.groups, vaultID)
	delete(s.members, vaultID)
	delete(s.groupGrants, vaultID)
	delete(s.groupServers, vaultID)
	return nil
}
