package store

import (
	"errors"
	"sort"
)

// Vaults (PLAT-856-400). A vault is a group with owners. Each person has exactly one personal vault, named
// PersonalVaultName, holding their own connections and secrets; nobody else is a member and nobody creates more.
// Sharing with colleagues goes through ordinary administrator-managed groups. A project vault (ProjectRoot set) keeps
// a project's secrets and follows the project's owner.
const (
	GroupKindVault    = "vault"
	PersonalVaultName = "My accounts"
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

// EnsurePersonalVault returns ownerID's personal vault, creating it under id the first time.
func (s *MemoryStore) EnsurePersonalVault(workspace, ownerID, id string) (string, error) {
	s.mu.Lock()
	defer s.persistUnlock()
	if u, ok := s.users[ownerID]; !ok || u.WorkspaceID != workspace {
		return "", errors.New("unknown user")
	}
	for _, g := range s.groups {
		if g.WorkspaceID == workspace && g.IsVault() && g.ProjectRoot == "" && g.Name == PersonalVaultName && g.HasOwner(ownerID) {
			return g.ID, nil
		}
	}
	if _, exists := s.groups[id]; exists {
		return "", errors.New("vault exists")
	}
	s.groups[id] = Group{ID: id, WorkspaceID: workspace, Name: PersonalVaultName, Description: "Your own accounts, used only by you and the projects you build.", Kind: GroupKindVault, Owners: []string{ownerID}}
	s.members[id] = map[string]bool{ownerID: true}
	return id, nil
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

// EnsureProjectVault keeps the existing project secret boundary in Vault. The host
// supplies the owner after its native project access checks; Run users are lent
// the selected secrets through the existing owner execution identity.
func (s *MemoryStore) EnsureProjectVault(workspace, owner, id, root, name string) error {
	s.mu.Lock()
	defer s.persistUnlock()
	if u, ok := s.users[owner]; !ok || u.WorkspaceID != workspace {
		return errors.New("unknown project owner")
	}
	g, exists := s.groups[id]
	if exists && (g.WorkspaceID != workspace || g.ProjectRoot != root) {
		return errors.New("project vault identity mismatch")
	}
	if exists && g.HasOwner(owner) && len(g.Owners) == 1 {
		return nil
	}
	s.groups[id] = Group{ID: id, WorkspaceID: workspace, Name: name, Kind: GroupKindVault, ProjectRoot: root, Owners: []string{owner}}
	s.members[id] = map[string]bool{owner: true}
	return nil
}
