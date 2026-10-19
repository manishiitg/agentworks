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

// ResolvePersonalGoogleIdentity keeps one saved account for an owner's verified
// Google identity. Only OAuth's verified address is accepted here.
func (g *GmailService) ResolvePersonalGoogleIdentity(ctx context.Context, id, email string) (GmailConnection, error) {
	pending, ok := g.GetConnection(id)
	if !ok {
		return GmailConnection{}, fmt.Errorf("This Google connection no longer exists. Connect again in My Vault.")
	}
	if !pending.IsPrivate() || pending.Email != "" {
		return pending, nil
	}
	for _, saved := range g.ListConnections() {
		if saved.ID == id || !saved.IsPrivate() || saved.OwnerID != pending.OwnerID || !strings.EqualFold(strings.TrimSpace(saved.Email), strings.TrimSpace(email)) {
			continue
		}
		// A different client cannot import tokens into the existing client's login.
		if saved.ClientName != pending.ClientName {
			if err := g.DeleteConnection(ctx, id); err != nil {
				return GmailConnection{}, err
			}
			return GmailConnection{}, fmt.Errorf("This Google account is already saved with another sign-in app. Use its saved account or Change access in My Vault.")
		}
		for _, workspace := range pending.Workspaces {
			if err := g.SelectPersonalConnection(ctx, saved.ID, workspace, true); err != nil {
				return GmailConnection{}, err
			}
		}
		if err := g.DeleteConnection(ctx, id); err != nil {
			return GmailConnection{}, err
		}
		saved, _ = g.GetConnection(saved.ID)
		log.Printf("[GOOGLE_ACCOUNTS] connection=%s reason=reuse_verified_identity", saved.ID)
		return saved, nil
	}
	return pending, nil
}

// Repeated Connect clicks reuse the unfinished personal sign-in, with no item
// selected. A verified email distinguishes separate Google accounts.
func (g *GmailService) CreateOrReusePersonalGooglePending(ctx context.Context, input GmailConnectionInput) (GmailConnection, error) {
	if input.Personal && len(input.Workspaces) == 0 {
		for _, c := range g.ListConnections() {
			if c.Personal && c.OwnerID == input.OwnerID && c.ClientName == input.ClientName && c.Email == "" && len(c.Workspaces) == 0 && c.ScopeWorkspace == "" {
				return g.UpdateConnection(ctx, c.ID, GmailConnectionInput{AllowReadAccess: input.AllowReadAccess, AllowAgentWriteAccess: input.AllowAgentWriteAccess, Services: input.Services, ServicesSet: true})
			}
		}
	}
	return g.CreateConnection(ctx, input)
}
