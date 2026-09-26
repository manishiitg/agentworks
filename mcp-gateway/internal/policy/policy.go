// Package policy evaluates the gateway's deny-by-default authorization.
//
// Rule order (M0): disabled connector/user-object → missing grant. M1 adds
// groups, PII policy, and schema validation ahead of the upstream call.
package policy

import (
	"errors"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

var (
	ErrUnknownTool       = errors.New("unknown tool")
	ErrToolNotActive     = errors.New("tool is not active")
	ErrConnectorDisabled = errors.New("connector is disabled")
	ErrNoGrant           = errors.New("no grant for tool")
)

// Authorize resolves the registered tool and checks the caller's grant.
// It returns the snapshot on allow.
func Authorize(s *store.MemoryStore, id auth.Identity, publicName string) (store.ToolSnapshot, error) {
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
	if !s.HasGrant(id.UserID, publicName) {
		return store.ToolSnapshot{}, ErrNoGrant
	}
	return t, nil
}

// Visible reports whether the tool may appear in the caller's tools/list.
// Execution-time Authorize stays authoritative; this only shapes the listing.
func Visible(s *store.MemoryStore, id auth.Identity, publicName string) bool {
	_, err := Authorize(s, id, publicName)
	return err == nil
}
