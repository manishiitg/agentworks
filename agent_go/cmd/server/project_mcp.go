package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

const projectMCPPrefix = externalMCPPath + "/project/"

var projectMCPID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,199}$`)

// The OAuth resource is the immutable project audience. Refresh retains it in
// the existing grant store; neither a client selector nor a moved folder can
// replace it. This URL is public configuration, never an access credential.
type projectMCPAudience struct{ Kind, ID, Access string }

// Consent intentionally excludes storage paths and editing authority.
type projectMCPConsent struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	Access string `json:"access"`
}

func projectMCPAudienceForPath(path string) (projectMCPAudience, bool) {
	var audience projectMCPAudience
	if !strings.HasPrefix(path, projectMCPPrefix) {
		return audience, false
	}
	parts := strings.Split(strings.TrimPrefix(path, projectMCPPrefix), "/")
	if len(parts) != 3 || !projectMCPID.MatchString(parts[1]) || (parts[2] != "view" && parts[2] != "use") {
		return audience, false
	}
	switch parts[0] {
	case "work", "workflow", "relay":
	default:
		return audience, false
	}
	return projectMCPAudience{parts[0], parts[1], parts[2]}, true
}

func projectMCPAudienceForResource(resource string) (projectMCPAudience, bool) {
	u, err := url.Parse(resource)
	if err != nil || u.RawQuery != "" || u.Fragment != "" || u.User != nil || u.RawPath != "" {
		return projectMCPAudience{}, false
	}
	return projectMCPAudienceForPath(u.Path)
}

func validMCPResource(resource string) bool {
	origin, global, ok := mcpOAuthURLs()
	if !ok {
		return false
	}
	if resource == global {
		return true
	}
	u, err := url.Parse(resource)
	if err != nil || resource != origin+u.Path {
		return false
	}
	_, ok = projectMCPAudienceForResource(resource)
	return ok
}

func isMCPRequestPath(path string) bool {
	if path == externalMCPPath {
		return true
	}
	_, ok := projectMCPAudienceForPath(path)
	return ok
}

// Project connections intentionally omit global administration, settings,
// account inventory and Builder actions. Merged schemas are reduced using
// this same member gate; the existing scope/role checks still run afterwards.
func projectMCPToolAllowed(kind, name string) bool {
	switch name {
	case "list_dashboards", "get_dashboard", "get_dashboard_link", "get_file_link", "get_report_link",
		"get_agent_context", "list_guidance_topics", "get_guidance_topic", "get_skill":
		return true
	}
	if kind == "work" {
		switch name {
		case "workspace", "list_crews", "get_crew", "list_crew_files", "search_crew_files", "read_crew_file",
			"project_functions_list", "project_functions_call", "project_functions_get_call", "project_functions_calls", "project_functions_reply":
			return true
		}
		return false
	}
	switch name {
	case "list_workflows", "get_workflow", "get_plan", "list_files", "search_files", "read_file", "list_step_code",
		"list_runs", "get_run", "get_logs", "run_status", "list_executions", "run_reply_input", "stop_step", "stop_all_executions",
		"project_functions_list", "project_functions_call", "project_functions_get_call", "project_functions_reply",
		"list_workflow_knowledge", "read_workflow_knowledge":
		return true
	case "run_relay", "get_relay_run", "list_relay_runs", "get_relay_releases":
		return kind == "relay"
	}
	return false
}

func (a projectMCPAudience) scopes() []string {
	scopes := []string{"dashboards:read"}
	if a.Kind == "work" {
		scopes = append(scopes, "crews:read")
		if a.Access == "use" {
			scopes = append(scopes, "crews:run")
		}
	} else {
		scopes = append(scopes, "workflows:read", "files:read")
		if a.Access == "use" {
			scopes = append(scopes, "runs:execute")
		}
	}
	return scopes
}

func (a projectMCPAudience) token(token accesstokens.Token) accesstokens.Token {
	// Never use the person-sign-in upgrade for a project connection. Its
	// saved consent is an upper bound even if the account becomes an admin.
	token.Scopes = slices.DeleteFunc(slices.Clone(token.Scopes), func(s string) bool { return !slices.Contains(a.scopes(), s) })
	token.AllWorkflows, token.AllCrews = false, false
	token.WorkflowIDs, token.CrewIDs = nil, nil
	token.ProjectKind, token.ProjectID, token.ProjectAccess = a.Kind, a.ID, a.Access
	if a.Kind == "work" {
		token.CrewIDs = []string{a.ID}
	} else {
		token.WorkflowIDs = []string{a.ID}
	}
	return token
}

func (api *StreamingAPI) projectMCPTarget(ctx context.Context, claims *UserClaims, a projectMCPAudience) (dashboardTarget, error) {
	if claims == nil || userAccessForClaims(claims).Disabled {
		return dashboardTarget{}, dashboardFailure(403, "Project access is unavailable.")
	}
	if a.Kind == "work" {
		binding, _, ok := api.resolveExternalWorkspace(ctx, claims, a.ID)
		if ok {
			return api.dashboardTarget(ctx, claims, binding.Binding.WorkspacePath)
		}
	} else {
		// The existing manifest index supplies only the ID-to-folder lookup.
		// dashboardTarget reads that manifest again for live ACL authorization;
		// no cached role or sharing decision can keep a revoked grant alive.
		items, err := workflowManifests.discover(ctx, 5*time.Second)
		if err != nil {
			return dashboardTarget{}, dashboardFailure(503, "Could not check project access.")
		}
		root := ""
		for _, item := range items {
			if item.Manifest != nil && item.Manifest.ID == a.ID && (item.Manifest.Kind == "relay") == (a.Kind == "relay") {
				if root != "" {
					return dashboardTarget{}, dashboardFailure(403, "Project identity is ambiguous.")
				}
				root = item.WorkspacePath
			}
		}
		if root != "" {
			target, err := api.dashboardTarget(ctx, claims, root)
			if err == nil && target.ID == a.ID && target.Kind == a.Kind {
				return target, nil
			}
			if err != nil {
				return dashboardTarget{}, err
			}
		}

	}
	return dashboardTarget{}, dashboardFailure(403, "Project access is unavailable.")
}

// Configuration only. Issuance still requires the recipient's own OAuth
// sign-in, approval and live project access. Opening this panel grants nothing.
func (api *StreamingAPI) handleProjectMCPSetup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.AccessToken != nil || claims.Scope != "" {
		externalError(w, 403, "forbidden", "Use your app login to connect this project.")
		return
	}
	target, err := api.dashboardTarget(r.Context(), claims, r.URL.Query().Get("workspace"))
	if err != nil {
		dashboardError(w, err)
		return
	}
	kind := target.Kind
	if kind == "crew" {
		kind = "work"
	}
	if !projectMCPID.MatchString(target.ID) {
		externalError(w, 400, "invalid_arguments", "Project identity is unavailable.")
		return
	}
	origin, _, ok := mcpOAuthURLs()
	if !ok {
		externalError(w, 503, "oauth_unavailable", "Configure PUBLIC_URL for browser sign-in.")
		return
	}
	base := origin + projectMCPPrefix + kind + "/" + target.ID + "/"
	externalJSON(w, map[string]any{"id": target.ID, "kind": kind, "title": target.Title, "view_url": base + "view", "use_url": base + "use"})
}

func projectMCPChallenge(w http.ResponseWriter, r *http.Request) {
	origin, _, ok := mcpOAuthURLs()
	a, bound := projectMCPAudienceForPath(r.URL.Path)
	if !ok || !bound {
		mcpOAuthChallenge(w)
		return
	}
	w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+origin+"/.well-known/oauth-protected-resource"+r.URL.Path+`", scope="`+strings.Join(a.scopes(), " ")+`"`)
}

// Bind consent to the project's creation identity, independent of title, folder
// moves, updated timestamps and current ACL. Legacy records without creation
// identity fail closed rather than silently retargeting a saved connection.
func projectMCPCreationIdentity(ctx context.Context, a projectMCPAudience, target dashboardTarget) (string, error) {
	var owner, created string
	if a.Kind == "work" {
		manifest, err := readCrewProjectManifests(ctx, a.Kind, target.Root)
		if err != nil || manifest.ID != a.ID {
			return "", dashboardFailure(403, "Project identity is unavailable.")
		}
		created = manifest.CreatedAt
	} else {
		manifest, exists, err := readWorkflowManifest(ctx, target.Root, false)
		if err != nil || !exists || manifest == nil || manifest.ID != a.ID {
			return "", dashboardFailure(403, "Project identity is unavailable.")
		}
		owner, created = manifest.CreatedBy, manifest.CreatedAt
	}
	if strings.TrimSpace(created) == "" {
		return "", dashboardFailure(403, "This project cannot connect yet. Ask its administrator to repair the project, then try connecting again.")
	}
	data, _ := json.Marshal([]string{a.Kind, a.ID, owner, created})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

func projectMCPAudit(grant mcpOAuthGrant, event, reason string) {
	if a, ok := projectMCPAudienceForResource(grant.Resource); ok {
		log.Printf("project_mcp event=%q family=%q user_id=%q kind=%q project_id=%q access=%q reason=%q", event, grant.FamilyID, grant.UserID, a.Kind, a.ID, a.Access, reason)
	}
}

func (api *StreamingAPI) checkProjectMCPGrant(ctx context.Context, grant mcpOAuthGrant) error {
	a, bound := projectMCPAudienceForResource(grant.Resource)
	if !bound {
		return nil
	}
	claims, err := accessTokenClaims(mcpOAuthTokenForGrant(grant))
	if err != nil {
		projectMCPAudit(grant, "project_check_failed", "account_access")
		return dashboardFailure(403, "Project access is unavailable.")
	}
	target, err := api.projectMCPTarget(ctx, claims, a)
	if err == nil {
		identity, identityErr := projectMCPCreationIdentity(ctx, a, target)
		err = identityErr
		if err == nil && (grant.ProjectIdentity == "" || grant.ProjectIdentity != identity) {
			err = dashboardFailure(403, "Project identity changed. Approve a new connection.")
		}
	}
	if err != nil {
		projectMCPAudit(grant, "project_check_failed", "access_or_creation_identity")
	}
	return err
}

func (api *StreamingAPI) handleProjectMCP(w http.ResponseWriter, r *http.Request) {
	a, ok := projectMCPAudienceForPath(r.URL.Path)
	claims := GetUserFromContext(r.Context())
	if !ok || claims == nil || claims.AccessToken == nil {
		externalError(w, 403, "forbidden", "Approve this project's MCP connection first.")
		return
	}
	token := claims.AccessToken
	grant := mcpOAuthGrant{FamilyID: strings.TrimPrefix(token.ID, "oauth-"), UserID: claims.UserID, Resource: "https://audit.invalid" + r.URL.Path}
	if token.ProjectKind != a.Kind || token.ProjectID != a.ID || token.ProjectAccess != a.Access || token.AllCrews || token.AllWorkflows ||
		((a.Kind == "work") && (!slices.Equal(token.CrewIDs, []string{a.ID}) || len(token.WorkflowIDs) != 0)) ||
		((a.Kind == "workflow" || a.Kind == "relay") && (!slices.Equal(token.WorkflowIDs, []string{a.ID}) || len(token.CrewIDs) != 0)) {
		projectMCPAudit(grant, "resource_mismatch", "handler_audience")
		externalError(w, 403, "forbidden", "Approve this project's MCP connection first.")
		return
	}
	target, err := api.projectMCPTarget(r.Context(), claims, a)
	if err == nil {
		identity, identityErr := projectMCPCreationIdentity(r.Context(), a, target)
		err = identityErr
		if err == nil && (token.ProjectIdentity == "" || token.ProjectIdentity != identity) {
			err = dashboardFailure(403, "Project identity changed. Approve a new connection.")
		}
	}
	if err != nil {
		projectMCPAudit(grant, "project_check_failed", "access_or_creation_identity")
		dashboardError(w, err)
		return
	}
	api.handleExternalMCP(w, r)
}
