package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type costProductBinding struct {
	Kind, ID, Name, Root, OwnerID string
}

// Read identities from native manifests, never invent a product ID from a
// display name. Callers pass an admitted workspace or a historical ledger
// workspace, not an arbitrary cost-tool argument. Authorization is separate.
func costProductForWorkspace(ctx context.Context, workspace string) costProductBinding {
	clean, err := wf.CleanRelative(workspace)
	if err != nil || clean != workspace || clean == "." {
		return costProductBinding{}
	}
	// Published Relay copies carry a validated source namespace. Reuse the
	// existing cost-copy provenance check rather than trusting folder names.
	if copies := costledger.WorkspaceCostCopies(clean); len(copies) > 1 {
		clean = copies[len(copies)-1]
	}
	root, kind, _, _ := costOverviewRoot(clean)
	if kind == costOverviewKindWorkflow {
		manifest, found, err := ReadWorkflowManifest(ctx, root)
		if err != nil || !found || manifest == nil || manifest.ID == "" {
			return costProductBinding{}
		}
		if manifest.Kind == "relay" {
			kind = "relay"
		}
		return costProductBinding{kind, manifest.ID, firstNonEmptyTrimmed(manifest.Label, manifest.ID), root, manifest.CreatedBy}
	}
	if kind == costOverviewKindCrew {
		manifest, err := readCrewProjectManifests(ctx, "work", root)
		if err != nil || manifest.ID == "" {
			return costProductBinding{}
		}
		owner, _ := crewProjectOwnerID(root)
		return costProductBinding{"crew", manifest.ID, firstNonEmptyTrimmed(manifest.Title, manifest.Identity.Name, manifest.Label, manifest.ID), root, owner}
	}
	if kind == costOverviewKindProduct {
		raw, found, err := readFileFromWorkspace(ctx, root+"/product.json")
		var manifest productProjectManifest
		if err != nil || !found || json.Unmarshal([]byte(raw), &manifest) != nil || manifest.ID == "" || manifest.Product == "" {
			return costProductBinding{}
		}
		kind = manifest.Product
		switch kind {
		case "work":
			kind = "crew"
		case "knowledgebase", "brain":
			kind = "brain"
		case "caplayer", "mcp-gateway", "vault":
			kind = "vault"
		}
		owner := firstNonEmptyTrimmed(manifest.OwnerID, workspaceref.MustParse(root).Owner())
		if isProjectWorkspacePath(root) {
			owner = projectStorageOwner(root)
		}
		return costProductBinding{kind, manifest.ID, firstNonEmptyTrimmed(manifest.Title, manifest.Label, manifest.ID), root, owner}
	}
	if clean == brainFolderName || strings.HasPrefix(clean, brainFolderName+"/") {
		return costProductBinding{"brain", "installation", "Brain", brainFolderName, ""}
	}
	return costProductBinding{}
}

// Trusted launch sites annotate the context before changing runtime identity.
// These fields are never accepted from client JSON or model arguments.
func costActivityContext(ctx context.Context, actor, activity, callID string) context.Context {
	a := costobserver.AttributionFromContext(ctx)
	if a.ActorID == "" {
		a.ActorID = actor
	}
	a.Activity = activity
	if callID != "" {
		a.FunctionCallID = callID
	}
	return costobserver.ContextWithAttribution(ctx, a)
}

func (api *StreamingAPI) costTurnAttribution(ctx context.Context, req QueryRequest, actor, workspace, scope string) costledger.Attribution {
	a := costobserver.AttributionFromContext(ctx)
	if a.ActorID == "" {
		a.ActorID = actor
	}
	p := costProductForWorkspace(ctx, workspace)
	if p.ID == "" && api != nil && api.agentProfiles != nil && req.AgentProfileID != "" {
		// Profile resolution already admitted this turn; use its platform
		// product for the installation-wide Brain/Vault agent workspaces.
		if profile, err := api.agentProfiles.Resolve(req.AgentProfileID, req.AgentProfileVersion, actor); err == nil {
			switch productpolicy.Canonical(profile.Product) {
			case "knowledgebase":
				p = costProductBinding{Kind: "brain", ID: "installation", Name: "Brain"}
			case "mcp-gateway":
				p = costProductBinding{Kind: "vault", ID: "installation", Name: "Vault"}
			}
		}
	}
	if p.ID != "" {
		a.ProductKind, a.ProductID, a.ProductName, a.ProductOwnerID = p.Kind, p.ID, p.Name, p.OwnerID
	}
	if a.Activity == "" {
		a.Activity = "person_chat"
		// The phase marker was set after native admission, not client JSON.
		if req.admittedWorkflowPhase {
			switch scope {
			case costobserver.ScopeBuilder:
				a.Activity = "builder"
			case costobserver.ScopePulse:
				a.Activity = "pulse"
			case costobserver.ScopeWorkflowExecution:
				a.Activity = "workflow_run"
			}
		}
		if claims := GetUserFromContext(ctx); claims != nil && strings.HasPrefix(claims.Provider, "bot_") {
			switch req.BotPlatform {
			case "slack":
				a.Activity = "slack_bot"
			case "whatsapp":
				a.Activity = "whatsapp_bot"
			}
		}
	}
	a.AttributionVersion = costledger.AttributionVersion
	return a
}

func (api *StreamingAPI) setSessionCostAttribution(sessionID string, a costledger.Attribution) {
	api.activeSessionsMux.Lock()
	defer api.activeSessionsMux.Unlock()
	if session := api.activeSessions[sessionID]; session != nil {
		session.CostAttribution = a
	}
}

func costAgentMessageContext(ctx context.Context, source agentMessageEndpoint) context.Context {
	a := costobserver.AttributionFromContext(ctx)
	if a.ActorID == "" {
		a.ActorID = source.UserID
	}
	if a.Activity == "" {
		a.Activity = "agent_message"
		if source.Kind == triggerCallerUser {
			a.Activity = "person_chat"
		}
	}
	return costobserver.ContextWithAttribution(ctx, a)
}
