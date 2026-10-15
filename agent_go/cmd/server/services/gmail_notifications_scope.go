package services

import (
	"context"
	"fmt"
	"strings"
)

// Explicit senders reach validation even when the company default is off, so
// removed, disabled or wrong-item accounts produce a visible failure.
func (g *GmailService) IsEnabledForDestination(dest *NotificationDestination) bool {
	return g.IsEnabled() || len(destGmailConnectionIDs(dest)) > 0
}

func gmailNotificationWorkspace(dest *NotificationDestination) string {
	if dest == nil || dest.Gmail == nil {
		return ""
	}
	return strings.TrimSpace(dest.Gmail.Workspace)
}

func (g *GmailService) notificationSendConfig(dest *NotificationDestination, id string) (*GmailConfig, string, string, error) {
	id = strings.TrimSpace(id)
	// Preserve the unscoped legacy API: it cannot use a private account.
	if dest == nil {
		cfg, resolved, err := g.resolveSendConfig(id)
		return cfg, resolved, "company", err
	}
	if id != "" {
		conn, found := g.GetConnection(id)
		if !found {
			return nil, "", "unknown", unavailableNotificationAccount()
		}
		if conn.IsPrivate() {
			item := gmailNotificationWorkspace(dest)
			if item == "" || !conn.Enabled || !conn.UsableFrom(GmailUseScope{Workspace: item, UserID: dest.UserID}) {
				return nil, "", "personal", unavailableNotificationAccount()
			}
			// New scoped notifications require the exact selected credential. A
			// missing OAuth grant must never inherit a server CLI login.
			token := ""
			if conn.AuthBackend != "gog" {
				var err error
				token, err = accessTokenForConnection(context.Background(), conn.ID, conn.ClientName)
				if err != nil || token == "" {
					return nil, conn.ID, "personal", unavailableNotificationAccount()
				}
			}
			cfg := gmailConnectionConfig(conn)
			if token != "" {
				cfg.Token = token
			}
			return cfg, conn.ID, "personal", nil
		}
	}
	if !g.IsEnabled() {
		return nil, "", "company", fmt.Errorf("Company email notifications are off. Choose a Google account added to this item, or ask an administrator to enable company email")
	}
	cfg, resolved, err := g.resolveSendConfig(id)
	return cfg, resolved, "company", err
}

func unavailableNotificationAccount() error {
	return fmt.Errorf("The Google account added to this item for notifications is no longer available; add one again in Google apps")
}
