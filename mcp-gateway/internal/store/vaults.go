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

// VaultConnection is what a member needs to recognise a connection in a vault: its name, its provider and whether it
// is signed in. No upstream URL and no credential.
type VaultConnection struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Provider string `json:"provider"`
	Status   string `json:"status"`
}

// VaultSummary is a vault as one person sees it.
type VaultSummary struct {
	Group        Group    `json:"group"`
	Role         string   `json:"role"` // "owner" or "member"
	Members      []string `json:"members"`
	ConnectorIDs []string `json:"connector_ids"`
	// Connections is ConnectorIDs with names and sign-in status.
	Connections []VaultConnection `json:"connections"`
	SecretNames []string          `json:"secret_names"`
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
		view := VaultSummary{Group: g, Role: role, Members: []string{}, ConnectorIDs: []string{}, Connections: []VaultConnection{}, SecretNames: []string{}}
		for member := range s.members[g.ID] {
			view.Members = append(view.Members, member)
		}
		for _, c := range s.connectors {
			if c.VaultID == g.ID {
				view.ConnectorIDs = append(view.ConnectorIDs, c.ID)
				view.Connections = append(view.Connections, VaultConnection{ID: c.ID, Label: c.Label, Provider: c.Provider, Status: c.Status})
			}
		}
		for name, secret := range s.secretResources {
			if secret.VaultID == g.ID {
				view.SecretNames = append(view.SecretNames, name)
			}
		}
		sort.Strings(view.Members)
		sort.Strings(view.ConnectorIDs)
		sort.Slice(view.Connections, func(i, j int) bool { return view.Connections[i].ID < view.Connections[j].ID })
		sort.Strings(view.SecretNames)
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

// VaultAttachConnector lets the vault's members use all tools of a connection that was created inside the vault (it
// carries the vault's ID from the start, so it was never granted to everyone). A connection that is not already the
// vault's is refused: an owner cannot take over an existing connection.
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
	if c.VaultID != vaultID {
		return errors.New("connection does not belong to this vault")
	}
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
	for _, secret := range s.secretResources {
		if secret.VaultID == vaultID {
			return errors.New("remove this vault's secrets first")
		}
	}
	delete(s.groups, vaultID)
	delete(s.members, vaultID)
	delete(s.groupGrants, vaultID)
	delete(s.groupServers, vaultID)
	return nil
}

// VaultAddSecret records a secret's name as belonging to a vault the actor owns and lets the vault's members use it, and
// nobody else: it is never granted to the Platform group. The name must be new (the host namespaces vault secret names
// by vault so they cannot collide); the value never reaches the Vault service.
func (s *MemoryStore) VaultAddSecret(actor, vaultID, name string) error {
	s.mu.Lock()
	defer s.persistUnlock()
	g, err := s.ownedVaultLocked(actor, vaultID)
	if err != nil {
		return err
	}
	if existing, ok := s.secretResources[name]; ok && existing.VaultID != vaultID {
		return errors.New("a secret with that name already exists")
	}
	s.secretResources[name] = SecretResource{Name: name, WorkspaceID: g.WorkspaceID, Managed: true, VaultID: vaultID}
	if s.secretGrants[vaultID] == nil {
		s.secretGrants[vaultID] = map[string]bool{}
	}
	s.secretGrants[vaultID][name] = true
	return nil
}

// VaultRemoveSecret removes a vault secret's name and every grant of it. Only an owner may.
func (s *MemoryStore) VaultRemoveSecret(actor, vaultID, name string) error {
	s.mu.Lock()
	defer s.persistUnlock()
	if _, err := s.ownedVaultLocked(actor, vaultID); err != nil {
		return err
	}
	if existing, ok := s.secretResources[name]; !ok || existing.VaultID != vaultID {
		return errors.New("unknown secret")
	}
	delete(s.secretResources, name)
	for _, grants := range s.secretGrants {
		delete(grants, name)
	}
	return nil
}
