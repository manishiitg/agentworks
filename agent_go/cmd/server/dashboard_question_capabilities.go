package server

import (
	"context"
	"encoding/json"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

type dashboardQuestionCapability struct {
	CanView bool   `json:"can_view"`
	CanAsk  bool   `json:"can_ask"`
	Reason  string `json:"reason,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

type dashboardQuestionPolicy struct {
	Authenticated, Active, Product, View, Published, AskEnabled, AskScope, Owner bool
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
		case !p.AskEnabled:
			capability.Reason = "ask_disabled"
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
	Capability dashboardQuestionCapability
}

// Resolve the server-owned id, then reauthorize its *current* target. No client
// workspace, role, session or captured revision can supply that authority.
func (api *StreamingAPI) resolveDashboardQuestion(ctx context.Context, claims *UserClaims, id, revision string) (resolvedDashboardQuestion, error) {
	var out resolvedDashboardQuestion
	if claims == nil || claims.UserID == "" || claims.Scope != "" || claims.Provider == "bot_route" || !dashboardScopeAllowed(claims, "get") {
		return out, dashboardFailure(404, "dashboard unavailable")
	}
	dashboardQuestionMu.Lock()
	var registry dashboardQuestionRegistry
	err := readDashboardQuestionJSON(ctx, dashboardQuestionRegistryPath, &registry)
	dashboardQuestionMu.Unlock()
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
	target, err := api.dashboardTarget(ctx, claims, out.Resource.Root)
	if err != nil || target.Kind != out.Resource.Kind || target.ID != out.Resource.ProjectID {
		return out, dashboardFailure(404, "dashboard unavailable")
	}
	out.Target = target
	product := map[string]string{"crew": "work", "workflow": "agentworks", "relay": "relays", "code": "code"}[target.Kind]
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
	askEnabled, owner := true, false
	switch target.Kind {
	case "crew":
		crew, ok := resolveCrewPath(ctx, claims.UserID, target.Root)
		if !ok {
			return out, dashboardFailure(404, "dashboard unavailable")
		}
		owner = crewAccessFor(claims, crew) == crewAccessOwner
		// Same persisted free_text_ask switch as Crew messaging; unlike its
		// legacy helper, a failed manifest read fails closed here.
		raw, found, err := readProjectRuntimeManifest(ctx, "work", target.Root)
		if err != nil || !found {
			return out, dashboardFailure(503, "Crew question settings unavailable")
		}
		var manifest struct {
			Capabilities struct {
				FreeTextAsk *bool `json:"free_text_ask"`
			} `json:"capabilities"`
		}
		if json.Unmarshal([]byte(raw), &manifest) != nil {
			return out, dashboardFailure(503, "Crew question settings unavailable")
		}
		askEnabled = manifest.Capabilities.FreeTextAsk == nil || *manifest.Capabilities.FreeTextAsk
	case "workflow", "relay":
		access, _ := workflowAccessForWorkspacePath(ctx, claims, target.Root)
		owner = access == WorkflowAccessOwner
	case "code":
		owner = true // dashboardTarget already requires the Code owner.
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
		case "code":
			askScope = externalCodeRunAllowed(claims)
		}
	}
	out.Capability = dashboardQuestionCapabilities(dashboardQuestionPolicy{Authenticated: true, Active: !userAccessForClaims(claims).Disabled, Product: true, View: true, Published: true, AskEnabled: askEnabled, AskScope: askScope, Owner: owner})
	return out, nil
}
