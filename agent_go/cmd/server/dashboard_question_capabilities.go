package server

import (
	"context"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

type dashboardQuestionCapability struct {
	CanView bool   `json:"can_view"`
	CanAsk  bool   `json:"can_ask"`
	Reason  string `json:"reason,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

type dashboardQuestionPolicy struct {
	Authenticated, Active, Product, View, Published, AskScope, Owner bool
}

func dashboardQuestionCapabilities(p dashboardQuestionPolicy) dashboardQuestionCapability {
	capability := dashboardQuestionCapability{}
	switch {
	case !p.Authenticated:
		capability.Reason = "authentication_required"
	case !p.Active:
		capability.Reason = "account_disabled"
	case !p.Product:
		capability.Reason = "product_unavailable"
	case !p.View:
		capability.Reason = "dashboard_unavailable"
	case !p.Published:
		capability.Reason = "not_published"
	default:
		capability.CanView = true
		switch {
		case !p.AskScope:
			capability.Reason = "ask_consent_required"
		default:
			capability.CanAsk = true
			capability.Mode = "question_only"
			if p.Owner {
				capability.Mode = "owner"
			}
		}
	}
	return capability
}

type resolvedDashboardQuestion struct {
	Resource   dashboardQuestionResource
	Target     dashboardTarget
	Document   dashboards.Dashboard
	Snapshot   *dashboards.Snapshot
	Capability dashboardQuestionCapability
}

// Resolve the server-owned id, then reauthorize its *current* target. No client
// workspace, role, session or captured revision can supply that authority.
func (api *StreamingAPI) resolveDashboardQuestion(ctx context.Context, claims *UserClaims, id, revision string) (resolvedDashboardQuestion, error) {
	var out resolvedDashboardQuestion
	if claims == nil || claims.UserID == "" || claims.Scope != "" || claims.Provider == "bot_route" || !dashboardScopeAllowed(claims, "get") {
		return out, dashboardFailure(404, "dashboard unavailable")
	}
	dashboardQuestionRegistryMu.Lock()
	var registry dashboardQuestionRegistry
	err := readDashboardQuestionJSON(ctx, dashboardQuestionRegistryPath, &registry)
	dashboardQuestionRegistryMu.Unlock()
	if err != nil {
		return out, err
	}
	for _, resource := range registry.Resources {
		if resource.ID == id {
			out.Resource = resource
			break
		}
	}
	if out.Resource.ID == "" {
		return out, dashboardFailure(404, "dashboard unavailable")
	}
	return api.resolveDashboardQuestionResource(ctx, claims, out.Resource, revision)
}

// A conversation already resolved its registry identity. Reuse that immutable
// identity while checking each distinct captured publication's live access.
func (api *StreamingAPI) resolveDashboardQuestionResource(ctx context.Context, claims *UserClaims, resource dashboardQuestionResource, revision string) (resolvedDashboardQuestion, error) {
	out := resolvedDashboardQuestion{Resource: resource}
	if claims == nil || claims.UserID == "" || claims.Scope != "" || claims.Provider == "bot_route" || !dashboardScopeAllowed(claims, "get") {
		return out, dashboardFailure(404, "dashboard unavailable")
	}
	target, err := api.dashboardTarget(ctx, claims, out.Resource.Root)
	if err != nil || target.Kind != out.Resource.Kind || target.ID != out.Resource.ProjectID {
		return out, dashboardFailure(404, "dashboard unavailable")
	}
	out.Target = target
	product := map[string]string{"crew": "work", "workflow": "agentworks", "relay": "relays"}[target.Kind]
	if product == "" || !userAllowedProduct(claims, product) {
		return out, dashboardFailure(404, "dashboard unavailable")
	}
	// Owners also get published evidence only. Draft authority from ordinary
	// dashboard editing is intentionally absent from the question resolver.
	result, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: target.Root, Action: "get", ID: out.Resource.Dashboard, Revision: revision, IncludeDraft: false, Guard: dashboardFileGuard(claims)})
	if err != nil {
		return out, err
	}
	if result.Dashboard == nil || result.Dashboard.Published == "" || result.Snapshot == nil {
		return out, dashboardFailure(404, "dashboard is not published")
	}
	if guard := dashboardFileGuard(claims); guard != nil && !guard.Allows(result.ResolvedPath, false) {
		return out, dashboardFailure(404, "dashboard unavailable")
	}
	out.Document = *result.Dashboard
	out.Snapshot = result.Snapshot
	owner := false
	switch target.Kind {
	case "crew":
		crew, ok := resolveCrewPath(ctx, claims.UserID, target.Root)
		if !ok {
			return out, dashboardFailure(404, "dashboard unavailable")
		}
		owner = crewAccessFor(claims, crew) == crewAccessOwner
	case "workflow", "relay":
		access, _ := workflowAccessForWorkspacePath(ctx, claims, target.Root)
		owner = access == WorkflowAccessOwner
	}
	askScope := true
	owner = owner && dashboardAuthoringAllowed(claims, &target)
	if token := claims.AccessToken; token != nil {
		// A run-only, agent-bound or folder-capped connection cannot gain
		// the full owner conversation mode just because its person owns it.
		owner = owner && token.FileGuard == nil && token.BoundAgentID == "" && token.BoundAgentName == ""
		switch target.Kind {
		case "crew":
			askScope = token.Allows("crews:run")
		case "workflow", "relay":
			askScope = token.Allows("runs:execute")
		}
	}
	out.Capability = dashboardQuestionCapabilities(dashboardQuestionPolicy{Authenticated: true, Active: !userAccessForClaims(claims).Disabled, Product: true, View: true, Published: true, AskScope: askScope, Owner: owner})
	// An Artifact is a finished page to read and share, not a dashboard to ask
	// (PLAT-750): no chat pane, Ask or function buttons beside it.
	if out.Snapshot.Artifact {
		out.Capability.CanAsk, out.Capability.Mode, out.Capability.Reason = false, "", "artifact"
	}
	return out, nil
}
