package services

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"
)

// SelectPersonalConnection changes only an item's reference, never credentials.
// The server verifies the Builder and ownership before calling this method.
func (g *GmailService) SelectPersonalConnection(ctx context.Context, id, workspace string, selected bool) error {
	workspace = strings.Trim(strings.TrimSpace(workspace), "/")
	if workspace == "" {
		return fmt.Errorf("Choose an item in Google apps first.")
	}
	cfg := g.GetConfig()
	found := false
	for i := range cfg.Connections {
		c := &cfg.Connections[i]
		if c.ID != id {
			continue
		}
		found = true
		if !c.IsPrivate() {
			return fmt.Errorf("Company accounts are managed by an administrator.")
		}
		// Preserve the original Code reference and store identity when adopting a legacy account.
		if !c.Personal && c.ScopeWorkspace != "" && len(c.Workspaces) == 0 {
			c.Workspaces = []string{strings.Trim(c.ScopeWorkspace, "/")}
		}
		contains := slices.ContainsFunc(c.Workspaces, func(root string) bool { return connectionWorkspaceRoot(root) == connectionWorkspaceRoot(workspace) })
		if !selected && !contains {
			return fmt.Errorf("This account is not added to that item. Open its Google apps to see the selected accounts.")
		}
		c.Personal = true
		if selected && !contains {
			c.Workspaces = append(c.Workspaces, workspace)
		}
		if !selected {
			c.Workspaces = slices.DeleteFunc(c.Workspaces, func(s string) bool { return connectionWorkspaceRoot(s) == connectionWorkspaceRoot(workspace) })
		}
	}
	if !found {
		return fmt.Errorf("Google account not found. Choose an account from Google apps.")
	}
	if err := g.SaveConfig(ctx, cfg); err != nil {
		return err
	}
	log.Printf("[GOOGLE_ACCOUNTS] connection=%s workspace=%s selected=%t", id, workspace, selected)
	return nil
}
