package server

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

// buildConnectedToolsPrompt returns a compact summary of integrations currently
// connected to this project so the agent knows what is available without extra
// discovery calls. It never contacts external providers, never prints
// credentials, and respects caller permissions (Run mode, guest asks, and local
// vs server mode).
func (api *StreamingAPI) buildConnectedToolsPrompt(ctx context.Context, profileID string, claims *UserClaims, workspacePath string, crewAskOnly, readOnly bool) string {
	if !isProjectProfileID(profileID) || strings.TrimSpace(workspacePath) == "" {
		return ""
	}
	callerID := ""
	if claims != nil {
		callerID = strings.TrimSpace(claims.UserID)
	}
	owner, hasOwner := crewProjectOwnerID(workspacePath)
	if !hasOwner {
		owner = callerID
	}
	lines := []string{}

	// 1. GitHub: only when the turn receives project secrets (not guest asks).
	if !crewAskOnly && owner != "" {
		if rec, err := projectGitHubConnection(ctx, owner, workspacePath); err == nil && rec != nil && strings.TrimSpace(rec.Login) != "" {
			lines = append(lines, "- **GitHub**: connected as `"+strings.TrimSpace(rec.Login)+"` (use `integrations action=github` or `$SECRET_GITHUB_TOKEN`)")
		}
	}

	// 2. Slack as me: Vault personal Slack first, falling back to legacy place attachments.
	slackAdded := false
	if vaultConfigured() {
		vaultCtx, cancel := context.WithTimeout(ctx, time.Second)
		servers, chosenOnly, selErr := projectAppSelection(vaultCtx, workspacePath)
		if selErr == nil {
			checkVaultSlack := func(person string, isOwner bool) bool {
				if person == "" {
					return false
				}
				conn, ok, err := ownVaultSlack(vaultCtx, person)
				if err != nil || !ok || conn.Status != "active" || !projectVaultAppInUse(vaultCtx, servers, chosenOnly, conn.ID) {
					return false
				}
				label := firstNonBlank(strings.TrimSpace(conn.Label), "Slack")
				if isOwner {
					lines = append(lines, "- **Slack as me**: "+label+" (full access via Slack MCP tools)")
				} else {
					lines = append(lines, "- **Slack as me**: "+label+" (send-only via Slack MCP tools; only the account owner can read or search)")
				}
				return true
			}
			if checkVaultSlack(callerID, true) {
				slackAdded = true
			} else if owner != "" && owner != callerID && checkVaultSlack(owner, false) {
				slackAdded = true
			}
		}
		cancel()
	}
	if !slackAdded {
		if attachments, err := placeMCPAttachmentsFor(projectRootOf(workspacePath)); err == nil && len(attachments) > 0 {
			for _, a := range attachments {
				if a.Scope != "user" {
					continue
				}
				server, err := personalSlackServer(a.Owner, a.Server)
				if err != nil {
					continue
				}
				dir, err := placeMCPDir(a.Owner)
				if err != nil || !placeMCPServerConnected(dir, a.Owner, server) {
					continue
				}
				label := firstNonBlank(strings.TrimSpace(server.Label), "Slack")
				if callerID != "" && a.Owner == callerID {
					lines = append(lines, "- **Slack as me**: "+label+" (full access via Slack MCP tools)")
				} else {
					lines = append(lines, "- **Slack as me**: "+label+" (send-only via Slack MCP tools; only the account owner can read or search)")
				}
			}
		}
	}

	// 3. Google apps: enabled, connected accounts usable from this project.
	if !crewAskOnly && !readOnly {
		if svc := services.GetGmailService(); svc != nil {
			conns := svc.ConnectionsUsableFrom(gmailToolScope(workspacePath))
			sort.Slice(conns, func(i, j int) bool { return conns[i].ID < conns[j].ID })
			for _, conn := range conns {
				if !conn.Enabled || conn.Status != services.GmailConnectionConnected {
					continue
				}
				label := firstNonBlank(strings.TrimSpace(conn.Email), strings.TrimSpace(conn.DisplayName))
				if label == "" {
					continue
				}
				grants := connectedGoogleGrants(conn)
				if len(grants) > 0 {
					lines = append(lines, "- **Google apps**: "+label+" ("+strings.Join(grants, ", ")+"; use `integrations action=google`)")
				} else {
					lines = append(lines, "- **Google apps**: "+label+" (connected; check grants with `integrations action=google_connections`)")
				}
			}
		}
	}

	// 4. Computer use: local Mac, owner/Builder only, when explicitly enabled.
	if IsLocalMode() && !readOnly && !crewAskOnly && computerUseAllowed(claims) && websiteDeviceClaims(claims) && callerID != "" && crewProjectOwnedByCaller(callerID, workspacePath) {
		if settings, err := readComputerUse(workspacePath); err == nil && settings.Enabled {
			lines = append(lines, "- **Computer use**: enabled on this computer (use `computer_use`; read `crew-laptop` skill)")
		}
	}

	// 5. Slack bot: server projects only.
	if !IsLocalMode() {
		if connID, err := productSlackConnectionID(ctx, profileID, workspacePath); err == nil && connID != "" {
			if svc := services.GetSlackService(); svc != nil {
				if conn, ok := svc.GetConnection(connID); ok && conn.Enabled && conn.BotToken != "" && conn.AppToken != "" {
					name := firstNonBlank(strings.TrimSpace(conn.DisplayName), "Saved Slack bot")
					if name == "Default" {
						name = "Saved Slack bot"
					}
					lines = append(lines, "- **Slack bot**: "+name+" (configured for this project)")
				}
			}
		}
	}

	if len(lines) == 0 {
		return ""
	}
	return "\n## Connected here\n\n" + strings.Join(lines, "\n") + "\n"
}

func connectedGoogleGrants(conn services.GmailConnection) []string {
	grants := []string{}
	gmailRead := conn.AllowReadAccess && services.GoogleScopesGrant(conn.Scopes, services.GmailReadonlyScope)
	gmailWrite := conn.AllowAgentWriteAccess && services.GoogleScopesGrant(conn.Scopes, services.GmailComposeScope)
	switch {
	case gmailRead && gmailWrite:
		grants = append(grants, "Gmail read/compose")
	case gmailRead:
		grants = append(grants, "Gmail read")
	case gmailWrite:
		grants = append(grants, "Gmail compose")
	}
	seen := map[string]bool{}
	for _, g := range conn.Services {
		scope, display, ok := services.GoogleServiceScopeURI(g.Service, g.Write)
		if !ok || !services.GoogleScopesGrant(conn.Scopes, scope) {
			continue
		}
		name := firstNonBlank(strings.TrimSpace(display), strings.TrimSpace(g.Service))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		mode := "read"
		if g.Write {
			mode = "read/write"
		}
		grants = append(grants, name+" "+mode)
	}
	return grants
}
