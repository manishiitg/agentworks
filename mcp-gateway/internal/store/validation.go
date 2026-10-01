package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/access"
	"regexp"
	"strings"
)

var sqlIdentity = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

func (s *MemoryStore) ValidateAccessPackage(p access.Package, workspaceID string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return validatePackageState(p, workspaceID, s.durableState())
}
func validatePackageState(p access.Package, workspaceID string, state durableState) error {
	if p.WorkspaceID != workspaceID || !sqlIdentity.MatchString(p.ID) ||
		strings.TrimSpace(p.Name) == "" || len(p.Name) > 100 {
		return errors.New("invalid package identity or name")
	}
	g, ok := state.Groups[p.GroupID]
	if !ok || g.WorkspaceID != workspaceID {
		return errors.New("unknown group")
	}
	if len(p.Rules) == 0 || len(p.Rules) > 100 {
		return errors.New("package must contain 1 to 100 tools")
	}
	seen := map[string]bool{}
	for _, rule := range p.Rules {
		if seen[rule.PublicName] {
			return fmt.Errorf("duplicate tool %s", rule.PublicName)
		}
		seen[rule.PublicName] = true
		t, ok := state.Tools[rule.PublicName]
		if !ok || t.WorkspaceID != workspaceID || t.Status != StatusActive ||
			t.Fingerprint == "" || t.Fingerprint != t.ApprovedFingerprint || rule.Fingerprint != t.Fingerprint {
			return fmt.Errorf("tool %s is not the approved version", rule.PublicName)
		}
		if len(rule.Conditions) > 10 {
			return fmt.Errorf("too many conditions for %s", rule.PublicName)
		}
		var schema map[string]any
		if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
			return fmt.Errorf("invalid schema for %s", rule.PublicName)
		}
		for _, condition := range rule.Conditions {
			if err := access.ValidateCondition(condition); err != nil {
				return fmt.Errorf("%s: %w", rule.PublicName, err)
			}
			if !access.SchemaHasStringPath(schema, condition.Path) {
				return fmt.Errorf("%s: %s is not an explicit string argument", rule.PublicName, condition.Path)
			}
		}
	}
	return nil
}
