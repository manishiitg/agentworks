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
				if rule.PublicName == t.PublicName && rule.Fingerprint == t.Fingerprint && t.ApprovedFingerprint == t.Fingerprint {
					rules = append(rules, rule)
				}
			}
		}
	}
	return rules, true, nil
}

// Authorize resolves the registered tool and checks the caller's grant.
// It returns the snapshot on allow.
func Authorize(s *store.MemoryStore, id auth.Identity, publicName string) (store.ToolSnapshot, error) {
	t, err := authorizeActiveTool(s, id, publicName)
	if err != nil {
		return store.ToolSnapshot{}, err
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
func authorizeActiveTool(s *store.MemoryStore, id auth.Identity, publicName string) (store.ToolSnapshot, error) {
	if err := s.PersistenceError(); err != nil {
		return store.ToolSnapshot{}, err
	}
	t, ok := s.GetTool(publicName)
	if !ok || t.WorkspaceID != id.WorkspaceID {
		return store.ToolSnapshot{}, ErrUnknownTool
	}
	if t.Status != store.StatusActive {
		return store.ToolSnapshot{}, ErrToolNotActive
	}
	c, ok := s.GetConnector(t.ConnectorID)
	if !ok || c.Status != store.StatusActive {
		return store.ToolSnapshot{}, ErrConnectorDisabled
	}
	if c.WorkspaceID != id.WorkspaceID {
		return store.ToolSnapshot{}, ErrConnectorDisabled
	}
	if t.Fingerprint == "" || t.ApprovedFingerprint != t.Fingerprint {
		return store.ToolSnapshot{}, ErrToolNotActive
	}
	return t, nil
}

// AuthorizeSetup is used only by the service-authorized Vault builder handler.
// Other transports call Authorize, which also checks group and user grants.
func AuthorizeSetup(s *store.MemoryStore, id auth.Identity, publicName string) (store.ToolSnapshot, error) {
	t, err := authorizeActiveTool(s, id, publicName)
	if err != nil {
		return store.ToolSnapshot{}, err
	}
	user, ok := s.GetUser(id.UserID)
	if !ok || user.WorkspaceID != id.WorkspaceID {
		return store.ToolSnapshot{}, ErrUnknownUser
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
