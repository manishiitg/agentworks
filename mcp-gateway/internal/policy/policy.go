// Package policy evaluates the gateway's deny-by-default authorization.
//
// Rule order: disabled connector/tool → missing grant (direct or via group).
// Approved schema and argument conditions are checked ahead of the upstream call.
package policy

import (
	"errors"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/access"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

var (
	ErrUnknownTool       = errors.New("unknown tool")
	ErrToolNotActive     = errors.New("tool is not active")
	ErrConnectorDisabled = errors.New("connector is disabled")
	ErrUnknownUser       = errors.New("user is not in this workspace")
	ErrUnknownGroup      = errors.New("group is not in this workspace")
	ErrNoGrant           = errors.New("no grant for tool")
	ErrArgumentsDenied   = errors.New("arguments outside published access package")
)

func callerGroups(s *store.MemoryStore, id auth.Identity) ([]string, error) {
	if id.ViaGroup != "" {
		group, ok := s.GetGroup(id.ViaGroup)
		if !ok || group.WorkspaceID != id.WorkspaceID {
			return nil, ErrUnknownGroup
		}
		return []string{id.ViaGroup}, nil
	}
	user, ok := s.GetUser(id.UserID)
	if !ok || user.WorkspaceID != id.WorkspaceID {
		return nil, ErrUnknownUser
	}
	return s.GroupsOf(id.UserID), nil
}

func matchingRules(s *store.MemoryStore, id auth.Identity, t store.ToolSnapshot) ([]access.ToolRule, bool, error) {
	packages, governed := s.PolicyForTool(id.WorkspaceID, t.PublicName)
	if !governed {
		return nil, false, nil
	}
	groups, err := callerGroups(s, id)
	if err != nil {
		return nil, true, err
	}
	rules := []access.ToolRule{}
	for _, p := range packages {
		if p.Status != "published" {
			continue
		}
		for _, group := range groups {
			if group != p.GroupID {
				continue
			}
			for _, rule := range p.Rules {
				// A rule follows its tool by name across definition changes (every discovered version is active, owner
				// 2026-10-20), so a vendor update never silently drops or strands a group's allows, denies or conditions.
				if rule.PublicName == t.PublicName && t.ApprovedFingerprint == t.Fingerprint {
					rules = append(rules, rule)
				}
			}
		}
	}
	return rules, true, nil
}

// Evaluator amortizes the SQLite PersistenceError check once per request for
// multi-tool listing/inventory loops while preserving live grant checks.
type Evaluator struct {
	store *store.MemoryStore
}

// NewEvaluator verifies store persistence once and returns an Evaluator for
// repeated policy checks within the same request.
func NewEvaluator(s *store.MemoryStore) (*Evaluator, error) {
	if err := s.PersistenceError(); err != nil {
		return nil, err
	}
	return &Evaluator{store: s}, nil
}

func (e *Evaluator) Authorize(id auth.Identity, publicName string) (store.ToolSnapshot, error) {
	return authorize(e.store, id, publicName, false)
}

func (e *Evaluator) AuthorizeSetup(id auth.Identity, publicName string) (store.ToolSnapshot, error) {
	return authorizeSetup(e.store, id, publicName, false)
}

func (e *Evaluator) Visible(id auth.Identity, publicName string) bool {
	_, err := authorize(e.store, id, publicName, false)
	return err == nil
}

// Authorize resolves the registered tool and checks the caller's grant.
// It returns the snapshot on allow.
func Authorize(s *store.MemoryStore, id auth.Identity, publicName string) (store.ToolSnapshot, error) {
	return authorize(s, id, publicName, true)
}

func authorize(s *store.MemoryStore, id auth.Identity, publicName string, checkPersistence bool) (store.ToolSnapshot, error) {
	t, c, err := authorizeActiveTool(s, id, publicName, checkPersistence)
	if err != nil {
		return store.ToolSnapshot{}, err
	}
	if c.VaultID != "" && s.IsCrewVault(c.VaultID) && id.AllowedCrewVault != c.VaultID {
		return store.ToolSnapshot{}, ErrNoGrant
	}
	if rules, governed, err := matchingRules(s, id, t); governed {
		if err != nil {
			return store.ToolSnapshot{}, err
		}
		if len(rules) == 0 {
			return store.ToolSnapshot{}, ErrNoGrant
		}
		return t, nil
	}
	if id.ViaGroup != "" {
		// Group API key: exactly the key's group applies.
		group, ok := s.GetGroup(id.ViaGroup)
		if !ok || group.WorkspaceID != id.WorkspaceID {
			return store.ToolSnapshot{}, ErrUnknownGroup
		}
		if !s.GroupHasTool(id.ViaGroup, publicName) && !s.GroupServerAllows(id.ViaGroup, t) {
			return store.ToolSnapshot{}, ErrNoGrant
		}
		return t, nil
	}
	user, ok := s.GetUser(id.UserID)
	if !ok || user.WorkspaceID != id.WorkspaceID {
		return store.ToolSnapshot{}, ErrUnknownUser
	}
	if !s.HasGrant(id.UserID, publicName) && !s.HasGroupGrant(id.UserID, publicName) &&
		!s.ServerGrantAllows(id.UserID, t) {
		return store.ToolSnapshot{}, ErrNoGrant
	}
	return t, nil
}

// Operational checks apply to both ordinary calls and administrator setup.
func authorizeActiveTool(s *store.MemoryStore, id auth.Identity, publicName string, checkPersistence bool) (store.ToolSnapshot, store.Connector, error) {
	if checkPersistence {
		if err := s.PersistenceError(); err != nil {
			return store.ToolSnapshot{}, store.Connector{}, err
		}
	} else if err := s.FastPersistenceError(); err != nil {
		return store.ToolSnapshot{}, store.Connector{}, err
	}
	t, ok := s.GetTool(publicName)
	if !ok || t.WorkspaceID != id.WorkspaceID {
		return store.ToolSnapshot{}, store.Connector{}, ErrUnknownTool
	}
	if t.Status != store.StatusActive {
		return store.ToolSnapshot{}, store.Connector{}, ErrToolNotActive
	}
	c, ok := s.GetConnector(t.ConnectorID)
	if !ok || c.Status != store.StatusActive {
		return store.ToolSnapshot{}, store.Connector{}, ErrConnectorDisabled
	}
	if c.WorkspaceID != id.WorkspaceID {
		return store.ToolSnapshot{}, store.Connector{}, ErrConnectorDisabled
	}
	if t.Fingerprint == "" || t.ApprovedFingerprint != t.Fingerprint {
		return store.ToolSnapshot{}, store.Connector{}, ErrToolNotActive
	}
	return t, c, nil
}

// AuthorizeSetup is used only by the service-authorized Vault builder handler.
// Other transports call Authorize, which also checks group and user grants.
// Private vault connectors (personal or Crew vaults not owned by the caller)
// stay out of builder setup authority.
func AuthorizeSetup(s *store.MemoryStore, id auth.Identity, publicName string) (store.ToolSnapshot, error) {
	return authorizeSetup(s, id, publicName, true)
}

func authorizeSetup(s *store.MemoryStore, id auth.Identity, publicName string, checkPersistence bool) (store.ToolSnapshot, error) {
	t, c, err := authorizeActiveTool(s, id, publicName, checkPersistence)
	if err != nil {
		return store.ToolSnapshot{}, err
	}
	user, ok := s.GetUser(id.UserID)
	if !ok || user.WorkspaceID != id.WorkspaceID {
		return store.ToolSnapshot{}, ErrUnknownUser
	}
	if c.VaultID != "" && !s.VaultOwnedBy(id.UserID, c.VaultID) {
		return store.ToolSnapshot{}, ErrNoGrant
	}
	return t, nil
}

// AuthorizeArguments evaluates published package conditions after schema
// validation and before forwarding to the upstream. All conditions within a
// tool rule must match; separate packages are alternatives for group members.
func AuthorizeArguments(s *store.MemoryStore, id auth.Identity, t store.ToolSnapshot, args map[string]any) error {
	rules, governed, err := matchingRules(s, id, t)
	if err != nil {
		return err
	}
	if !governed {
		return nil
	}
	for _, rule := range rules {
		allowed := true
		for _, condition := range rule.Conditions {
			if !access.Match(condition, args) {
				allowed = false
				break
			}
		}
		if allowed {
			return nil
		}
	}
	return ErrArgumentsDenied
}

// Visible reports whether the tool may appear in the caller's tools/list.
// Execution-time Authorize stays authoritative; this only shapes the listing.
func Visible(s *store.MemoryStore, id auth.Identity, publicName string) bool {
	_, err := Authorize(s, id, publicName)
	return err == nil
}
