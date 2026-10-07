package server

import (
	"context"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

// ExecutionPrincipal separates execution authority from the resource namespace
// and the external audit actor. Only application adapters create this value.
// It is never accepted as part of the public query/JWT contract.
type ExecutionPrincipal struct {
	Kind            string
	ID              string
	ResourceOwnerID string
	Access          WorkflowAccessLevel
	Target          services.ChannelRoute
	AuditActor      string
}

// Revalidate at the shared query boundary, including resumed and live-input
// turns. Listener validation alone is insufficient for a queued turn.
func (api *StreamingAPI) revalidateExecutionPrincipal(ctx context.Context, req QueryRequest) (context.Context, error) {
	claims := GetUserFromContext(ctx)
	if claims != nil && claims.Provider == slackDMProvider {
		return api.revalidateSlackDMPrincipal(ctx, claims, req)
	}
	if claims == nil || claims.Provider != "bot_route" {
		return ctx, nil
	}
	if req.BotPlatform != "slack" || req.BotChannelID == "" {
		return ctx, fmt.Errorf("bot route has no trusted channel binding")
	}
	cfg, routes, err := api.slackRoutes(ctx)
	if err != nil {
		return ctx, err
	}
	expected := services.ChannelRoute{WorkflowID: claims.BotRouteWorkflowID, ProfileID: claims.BotRouteProfileID, ConversationKey: claims.BotRouteConversationKey, WorkspacePath: claims.BotRouteWorkspacePath}
	// The turn's target must still be on this channel's allowed list on its
	// arrival app (and, on the platform bot, still switched on): a slug is a
	// name, never a grant (PLAT-668).
	route, found, dedicated := api.slackRouteForTurn(ctx, req.BotConnectionID, req.BotChannelID, routes, expected, req)
	if !found {
		return ctx, fmt.Errorf("Slack bot route was revoked")
	}
	// An explicit route (or the workflow's own app) is the destination's own
	// enablement: it authorizes bot turns regardless of the platform switch.
	if !slackTrafficAllowed(cfg, true, dedicated) {
		return ctx, fmt.Errorf("Slack bot route is disabled")
	}
	if route.BotGrant != "run" && route.BotGrant != "owner" {
		return ctx, fmt.Errorf("Slack bot route has no explicit grant; save it in Setup > Bots")
	}
	route.BotGrant = "run"
	route.WorkshopMode = "run"
	if !claims.SlackTrustedApp && !services.SlackRouteAllowsEmail(route, req.BotUserEmail) {
		return ctx, fmt.Errorf("Slack email is blocked or unverifiable")
	}
	// A Slack thread runs in its own chat of the route's crew
	// ("<crew>:slack-<hash>"); the route names the crew itself.
	if sameProjectConversation(route.ConversationKey, expected.ConversationKey) {
		expected.ConversationKey = route.ConversationKey
	}
	if !sameSlackRouteDestination(route, expected) {
		return ctx, fmt.Errorf("bot route target changed; start a new conversation")
	}
	if !slackRouteFolderMatches(route, req.SelectedFolder) || req.AgentProfileID != route.ProfileID || req.PresetQueryID != route.WorkflowID || !sameProjectConversation(route.ConversationKey, req.AgentProfileConversationKey) {
		return ctx, fmt.Errorf("query does not match the bot route target")
	}
	principal := &ExecutionPrincipal{Kind: "bot_route", ID: services.BotPrincipalIDForRoute("slack", route), Target: route, Access: WorkflowAccessRead, AuditActor: req.BotUserID}
	copy := *claims
	copy.BotRouteGrant = route.BotGrant
	if route.ProfileID != "" {
		if route.WorkspaceUserID == "" {
			return ctx, fmt.Errorf("product route has no resource owner")
		}
		principal.ResourceOwnerID = route.WorkspaceUserID
		copy.UserID = route.WorkspaceUserID
	} else if req.TriggeredBy == "bot:slack" && route.Trigger != nil {
		// A configured Slack workflow trigger is an unattended workflow run,
		// just like an API webhook or clock schedule. The bot principal still
		// constrains authorization and identifies the external actor, but the
		// workflow owner supplies the resource/secrets namespace.
		manifest, exists, err := ReadWorkflowManifest(ctx, route.WorkspacePath)
		if err != nil {
			return ctx, fmt.Errorf("load workflow trigger owner: %w", err)
		}
		if !exists || !strings.EqualFold(strings.TrimSpace(manifest.ID), strings.TrimSpace(route.WorkflowID)) {
			return ctx, fmt.Errorf("workflow trigger target is unavailable")
		}
		ownerID := workflowExecutionOwnerUserID(manifest)
		if ownerID == "" {
			return ctx, fmt.Errorf("workflow trigger has no execution owner")
		}
		principal.ResourceOwnerID = ownerID
		copy.UserID = ownerID
	} else {
		copy.UserID = principal.ID
	}
	copy.ExecutionPrincipal = principal
	return context.WithValue(ctx, UserContextKey, &copy), nil
}

// conversationTargetAccess is shared by all adapters. Resolve authority from
// the concrete workflow or Crew, rechecking it for each tool invocation.
// backfillWorkflowPhaseFolder resolves a workflow-phase request's workspace
// folder from its preset when the client did not send one. Callers must run
// this before conversationTargetAccess so manifest membership answers for
// these turns. An unresolvable preset leaves the request untouched and access
// falls through to the account tier, as before.
func (api *StreamingAPI) backfillWorkflowPhaseFolder(ctx context.Context, req *QueryRequest) {
	if req == nil || req.AgentMode != "workflow_phase" || strings.TrimSpace(req.SelectedFolder) != "" {
		return
	}
	resolved, err := api.resolveWorkspacePathFromPreset(ctx, req.PresetQueryID)
	if err != nil {
		return
	}
	if folder := strings.Trim(strings.TrimSpace(resolved), "/"); strings.HasPrefix(folder, "Workflow/") {
		req.SelectedFolder = folder
	}
}

func (api *StreamingAPI) conversationTargetAccess(ctx context.Context, req QueryRequest) (WorkflowAccessLevel, error) {
	claims := GetUserFromContext(ctx)
	if level, scoped := botRouteProfileAccessForRequest(claims, req); scoped {
		if level == WorkflowAccessNone {
			return level, fmt.Errorf("profile route access denied")
		}
		return level, nil
	}
	if strings.EqualFold(strings.TrimSpace(req.AgentProfileID), codeproduct.ProfileID) || isCodeProjectPath(req.SelectedFolder) {
		// Only its owner can run Code; audited inspection has separate routes.
		if claims == nil || strings.TrimSpace(claims.UserID) == "" || api.agentProfiles == nil {
			return WorkflowAccessNone, fmt.Errorf("Code access denied")
		}
		profile, err := api.agentProfiles.Resolve(codeproduct.ProfileID, req.AgentProfileVersion, claims.UserID)
		if err != nil || !userAllowedProduct(claims, profile.Product) {
			return WorkflowAccessNone, fmt.Errorf("Code access denied")
		}
		key := strings.TrimSpace(req.AgentProfileConversationKey)
		if key == "" {
			return WorkflowAccessNone, fmt.Errorf("Code conversation is required")
		}
		project, err := resolveCrewProjectBinding(ctx, claims.UserID, profile, key, req.SelectedFolder)
		if err != nil || !workspacePathsMatchForUser(claims.UserID, project.Binding.WorkspacePath, req.SelectedFolder) {
			return WorkflowAccessNone, fmt.Errorf("Code access denied")
		}
		role := codeRoleFor(ctx, claims.UserID, project.OwnerID, project.Binding.ResourceID)
		if !role.atLeast(codeRoleEditor) {
			return WorkflowAccessNone, fmt.Errorf("Code access denied")
		}
		return role.workflowAccess(), nil
	}
	if strings.EqualFold(strings.TrimSpace(req.AgentProfileID), "work") {
		if claims == nil || strings.TrimSpace(claims.UserID) == "" || api.agentProfiles == nil {
			return WorkflowAccessNone, fmt.Errorf("Crew access denied")
		}
		profile, err := api.agentProfiles.Resolve(req.AgentProfileID, req.AgentProfileVersion, claims.UserID)
		if err != nil || !userAllowedProduct(claims, profile.Product) {
			return WorkflowAccessNone, fmt.Errorf("Crew access denied")
		}
		key := strings.TrimSpace(req.AgentProfileConversationKey)
		if key == "" {
			return WorkflowAccessNone, fmt.Errorf("Crew conversation is required")
		}
		// Crew Run mode: the owner chats with full access; anyone else
		// with the Crew product chats read-only. The binding resolves
		// under whichever owner holds the project.
		crew, err := resolveCrewProjectBinding(ctx, claims.UserID, profile, key, req.SelectedFolder)
		if err != nil || !workspacePathsMatchForUser(claims.UserID, crew.Binding.WorkspacePath, req.SelectedFolder) {
			return WorkflowAccessNone, fmt.Errorf("Crew access denied")
		}
		if !crew.OwnedByCaller {
			return WorkflowAccessRead, nil
		}
		return WorkflowAccessOwner, nil
	}
	if strings.HasPrefix(req.SelectedFolder, "Workflow/") {
		manifest, exists, err := ReadWorkflowManifest(ctx, req.SelectedFolder)
		if err != nil {
			return WorkflowAccessNone, err
		}
		if exists {
			level := workflowAccessForManifest(claims, manifest)
			if !userAllowedWorkflowID(claims, manifest.ID) || level == WorkflowAccessNone {
				return WorkflowAccessNone, fmt.Errorf("workflow access denied")
			}
			return level, nil
		}
		if claims != nil && claims.Provider == "bot_route" {
			return WorkflowAccessNone, fmt.Errorf("workflow route manifest is missing")
		}
	}
	return workflowAccessForClaims(claims), nil
}

// slackRouteFolderMatches compares a route's folder with the turn's. A crew
// route may hold the logical path while the turn runs in the owner's
// physical folder; both name the same crew for that owner. Workflow routes
// compare exactly.
func slackRouteFolderMatches(route services.ChannelRoute, selectedFolder string) bool {
	if strings.TrimSpace(route.ProfileID) != "" && strings.TrimSpace(route.WorkspaceUserID) != "" {
		return workspacePathsMatchForUser(route.WorkspaceUserID, route.WorkspacePath, selectedFolder)
	}
	return filepath.Clean(strings.TrimSpace(selectedFolder)) == filepath.Clean(strings.TrimSpace(route.WorkspacePath))
}
