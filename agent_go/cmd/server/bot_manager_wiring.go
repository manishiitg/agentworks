package server

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// wireBotManager installs every server hook on a bot manager: session start
// and follow-up, resume, workflow access, running workflows, the workflow and
// crew-conversation turn builders, progressive text and the Slack dedicated
// route. Startup and the bot dry-run tests share it, so a test exercises the
// production wiring rather than a copy.
func (api *StreamingAPI) wireBotManager(m *services.BotConversationManager) {
	m.SetStartSessionFunc(api.startSessionInternal)
	m.SetFollowUpFunc(api.sendFollowUpInternal)
	m.SetResumeTargetFunc(api.resolveBotResumeTarget)
	m.SetResumeListFunc(api.listBotResumeTargets)
	m.SetWorkflowAccessFunc(api.checkBotWorkflowAccess)
	m.SetRunningWorkflowsFunc(api.botRunningWorkflows)
	// Product/profile routes are shared by Slack and WhatsApp; Slack profile
	// routes need the handler even when WhatsApp is disabled.
	m.SetProfileTurnFunc(api.botProfileTurn)
	m.SetWorkflowTurnFunc(api.botWorkflowTurn)
	m.SetUserWorkflowChatFunc(api.userWorkflowChat)
	// Progressive text: while a bot turn runs, forward each completed
	// assistant reply as its own message rather than waiting for the whole
	// turn to finish — reads the same durable conversation_history the UI's
	// own chat-restore path already trusts, kept current as the turn runs.
	m.SetChatHistoryReader(botProgressiveChatHistoryReader)
	services.SetDedicatedSlackRouteFunc(api.dedicatedSlackRoute)
	// Slack slugs: one app reaches many targets, per channel list and per
	// DM sender (slack_slugs.go, PLAT-668).
	services.SetSlackRoutingHooks(api.slackRoutingHooksFor())
	// "<workflow-slug>-pulse" talks to the workflow's Pulse (PLAT-697).
	services.SetGoalLeadSlackHandler(api.handleGoalLeadSlack)
	// A 1:1 Slack DM runs as the one enabled account its sender's email
	// maps to (slack_dm.go).
	services.SetSlackDMUserResolver(slackDMUserForEmail)
	// WhatsApp lists other owners' crews (read-only) beside the user's own.
	services.SetWhatsAppOtherCrewsFunc(api.whatsappOtherCrews)
	// A Crew that moved to the shared root (PLAT-442 step 4) names no owner in its path and keeps its old spellings
	// as aliases: the bot code asks the server's registry and alias map.
	services.SetCrewScopeHooks(botCrewOwner, foldCrewScopePath, api.ownSharedCrewListings)
}

// botCrewOwner is the registered owner of a crew path (Crew/<f>, or any spelling of a migrated crew); "" when the
// path is no crew's.
func botCrewOwner(workspacePath string) string {
	owner, _ := crewProjectOwnerID(workspacePath)
	return owner
}

// foldCrewScopePath maps any spelling of a migrated crew to its Crew/<f> path, keeping anything below it; every
// other path is returned unchanged.
func foldCrewScopePath(workspacePath string) string {
	trimmed := strings.TrimSpace(workspacePath)
	if trimmed == "" {
		return workspacePath
	}
	ref := workspaceref.MustParse(filepath.ToSlash(trimmed))
	if ref.IsShared() {
		return workspacePath
	}
	folder, shared, ok := ref.AnyCrewProject()
	if !ok || shared {
		return workspacePath
	}
	moved := crewPathAliases.lookupFolder(context.Background(), folder)
	if moved == "" {
		return workspacePath
	}
	rest := strings.TrimPrefix(ref.Logical(), workspaceref.CrewProjectsRoot+"/"+folder)
	return moved + rest
}

// ownSharedCrewListings lists the Crews at the shared root the user owns, for the WhatsApp destination list.
func (api *StreamingAPI) ownSharedCrewListings(ctx context.Context, userID string) []services.SharedCrewListing {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil
	}
	store := defaultProductProjectStore()
	manifests, err := listSharedCrewManifestPaths(ctx, store, userID)
	if err != nil {
		return nil
	}
	var out []services.SharedCrewListing
	for _, manifestPath := range manifests {
		root := strings.TrimSuffix(manifestPath, "/product.json")
		manifest, err := readCrewProjectManifests(ctx, crewProfileID, root)
		if err != nil {
			continue
		}
		out = append(out, services.SharedCrewListing{ID: manifest.ID, Title: firstNonEmptyTrimmed(manifest.Title, manifest.Identity.Name), WorkspacePath: root})
	}
	return out
}

// botRunningWorkflows lists a user's running workflows for bot status replies.
func (api *StreamingAPI) botRunningWorkflows(userID string) []services.BotRunningWorkflow {
	running := api.listRunningWorkflowExecutions(userID)
	out := make([]services.BotRunningWorkflow, 0, len(running))
	for _, wf := range running {
		label := strings.TrimSpace(wf.PresetName)
		if label == "" && wf.WorkspacePath != "" {
			label = workflowNameFromWorkspacePath(wf.WorkspacePath)
		}
		out = append(out, services.BotRunningWorkflow{
			WorkflowLabel:    label,
			WorkspacePath:    wf.WorkspacePath,
			Status:           wf.Status,
			CurrentStepTitle: wf.CurrentStepTitle,
			PhaseName:        wf.PhaseName,
			Title:            wf.Title,
			SessionID:        wf.SessionID,
			StartedAt:        wf.StartedAt,
		})
	}
	return out
}
