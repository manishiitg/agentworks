package server

import (
	"context"
	"encoding/json"
	"log"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
)

// migrateCrewBotScopes rewrites every stored crew bot destination saved in
// its logical form ("Chats/Work/projects/<id>") to the owner's physical folder
// (docs/design/bot_destination_scope.md): Slack connections, their
// per-channel routes, and the platform channel routes. A logical crew path has
// no owner, so the crew's own bot answered "This Slack route is no longer
// configured" and shared-bot crew channels were refused (server A 2026-09-26).
//
// The owner is the recorded one when its tree holds the project, else the one
// user whose tree does; zero or several candidates leave the record as it is
// and log it. A repaired connection is selected for its crew when the crew
// has none, the step its original save failed to complete.
func (api *StreamingAPI) migrateCrewBotScopes(ctx context.Context, svc *services.SlackService) {
	resolve := func(workspacePath, profileID, ownerHint string) string {
		return physicalCrewScopeForMigration(ctx, workspacePath, profileID, ownerHint)
	}
	if svc != nil {
		rescoped, err := svc.RewriteSlackScopes(ctx, resolve)
		if err != nil {
			log.Printf("[SLACK] crew scope migration of connections failed: %v", err)
		}
		for _, connID := range rescoped {
			conn, ok := svc.GetConnection(connID)
			if !ok {
				continue
			}
			log.Printf("[SLACK] crew app %s scope repaired: %s", conn.ID, conn.WorkspacePath)
			if selected, err := productSlackConnectionID(ctx, conn.ProfileID, conn.WorkspacePath); err == nil && selected == "" {
				if err := updateProductSlackConnectionID(ctx, conn.ProfileID, conn.WorkspacePath, conn.ID); err != nil {
					log.Printf("[SLACK] crew app %s could not be selected for %s: %v", conn.ID, conn.WorkspacePath, err)
				} else {
					log.Printf("[SLACK] crew app %s selected for %s", conn.ID, conn.WorkspacePath)
				}
			}
		}
	}
	api.migrateSlackPlatformRouteScopes(ctx, resolve)
}

// migrateSlackPlatformRouteScopes rewrites crew destinations in the shared
// bot's channel routes.
func (api *StreamingAPI) migrateSlackPlatformRouteScopes(ctx context.Context, resolve services.ScopeResolver) {
	if api == nil || api.chatStore == nil {
		return
	}
	cfg, routes, err := api.slackRoutes(ctx)
	if err != nil || cfg == nil || len(routes) == 0 {
		return
	}
	changed := false
	for channelID, route := range routes {
		if services.ValidateBotScope(route.WorkspacePath, route.ProfileID) == nil {
			continue
		}
		physical := resolve(route.WorkspacePath, route.ProfileID, route.WorkspaceUserID)
		if physical == "" {
			continue
		}
		route.WorkspacePath = physical
		if strings.TrimSpace(route.WorkspaceUserID) == "" {
			route.WorkspaceUserID = services.RouteWorkspaceUserID(route, "")
		}
		routes[channelID] = route
		changed = true
		log.Printf("[SLACK] channel route %s crew scope repaired: %s", channelID, physical)
	}
	if !changed {
		return
	}
	encoded, err := json.Marshal(routes)
	if err != nil {
		return
	}
	if _, err := api.chatStore.UpsertBotConnectorConfig(ctx, &chathistory.CreateBotConnectorConfigRequest{
		ID: "slack", Enabled: cfg.Enabled, BotMode: cfg.BotMode, ConfigJSON: cfg.ConfigJSON, DefaultPresetID: cfg.DefaultPresetID, AutoConfirm: cfg.AutoConfirm, AllowedChannels: string(encoded),
	}); err != nil {
		log.Printf("[SLACK] crew scope migration of channel routes failed: %v", err)
	}
}

// physicalCrewScopeForMigration resolves a logical crew path to the physical
// folder of the recorded owner (when that tree holds the project) or of the
// one user whose tree does. It returns "" when the path is already physical, a
// workflow, or not resolvable to exactly one owner.
func physicalCrewScopeForMigration(ctx context.Context, workspacePath, profileID, ownerHint string) string {
	profileID = strings.TrimSpace(profileID)
	logical := strings.Trim(filepath.ToSlash(strings.TrimSpace(workspacePath)), "/")
	if profileID == "" || !strings.HasPrefix(logical, "Chats/") {
		return ""
	}
	// A crew that has since moved to the shared root is addressed there (PLAT-442 step 4).
	if moved := foldCrewScopePath(logical); moved != logical {
		return moved
	}
	if hint := strings.TrimSpace(ownerHint); hint != "" {
		physical := agentProfileRuntimeWorkspace(hint, logical)
		if _, found, err := readProjectRuntimeManifest(ctx, profileID, physical); err == nil && found {
			return physical
		}
	}
	owners := crewProjectOwners(ctx, profileID, logical)
	if len(owners) != 1 {
		log.Printf("[SLACK] crew scope %s kept: %d candidate owners", logical, len(owners))
		return ""
	}
	return agentProfileRuntimeWorkspace(owners[0], logical)
}

// crewProjectOwners lists the users whose tree holds the project manifest at
// the logical crew path.
func crewProjectOwners(ctx context.Context, profileID, logical string) []string {
	candidates := map[string]struct{}{}
	if dir, err := loadUserDirectory(); err == nil && dir != nil {
		for _, user := range dir.Users {
			if id := strings.TrimSpace(user.ID); id != "" {
				candidates[id] = struct{}{}
			}
		}
	}
	if id := strings.TrimSpace(GetDefaultUserID()); id != "" {
		candidates[id] = struct{}{}
	}
	var owners []string
	for userID := range candidates {
		physical := agentProfileRuntimeWorkspace(userID, logical)
		if _, found, err := readProjectRuntimeManifest(ctx, profileID, physical); err == nil && found {
			owners = append(owners, userID)
		}
	}
	return owners
}
