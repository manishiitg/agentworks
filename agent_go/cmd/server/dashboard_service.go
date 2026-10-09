package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	workshop "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type dashboardTarget struct {
	Root, Kind, ID, Title string
	Edit                  bool
}

func dashboardFailure(status int, message string) error {
	return &wf.FileError{Status: status, Message: message}
}
func dashboardMutation(action string) bool {
	return action == "create" || action == "update" || action == "publish" || action == "restore"
}
func dashboardScopeAllowed(claims *UserClaims, action string) bool {
	if claims == nil || userAccessForClaims(claims).Disabled {
		return false
	}
	if dashboardMutation(action) && !userAccessForClaims(claims).CanEdit {
		return false
	}
	if claims.AccessToken == nil {
		return true
	}
	if !claims.AccessToken.Allows("dashboards:read") {
		return false
	}
	if dashboardMutation(action) && !claims.AccessToken.Allows("dashboards:write") {
		return false
	}
	if action == "preview" && !claims.AccessToken.Allows("runs:execute") {
		return false
	}
	return true
}
func (api *StreamingAPI) dashboardTarget(ctx context.Context, claims *UserClaims, root string) (dashboardTarget, error) {
	var target dashboardTarget
	clean, err := wf.CleanRelative(root)
	if err != nil || clean != root {
		return target, dashboardFailure(400, "canonical project workspace required")
	}
	if crew, ok := resolveCrewPath(ctx, claims.UserID, clean); ok && crew.Rest == "" {
		if crewAccessFor(claims, crew) == crewAccessNone {
			return target, dashboardFailure(404, "project unavailable")
		}
		id := path.Base(crew.Root)
		title := id
		manifest, err := readCrewProjectManifests(ctx, "work", crew.Root)
		if err != nil {
			return target, dashboardFailure(404, "project unavailable")
		}
		if manifest.ID != "" {
			id = manifest.ID
		}
		if manifest.Title != "" {
			title = manifest.Title
		}
		if claims.AccessToken != nil && !claims.AccessToken.AllowsCrew(id) {
			return target, dashboardFailure(404, "project unavailable")
		}
		return dashboardTarget{Root: crew.Root, Kind: "crew", ID: id, Title: title, Edit: crewAccessFor(claims, crew) == crewAccessOwner && userAccessForClaims(claims).CanEdit}, nil
	}
	if ref := workspaceref.MustParse(clean); ref.Logical() != "" {
		root, project, ok := ref.ProjectRoot()
		if ok && root == workspaceref.CodeProjectsRoot {
			owner := ref.Owner()
			if owner == "" {
				owner = sanitizeUserIDForPath(claims.UserID)
			}
			physical := workspaceref.PhysicalPathOf(owner, ref.Logical())
			if codeRoleFor(ctx, claims.UserID, owner, project) != codeRoleOwner || resolveProjectOwner(ctx, physical) != owner {
				return target, dashboardFailure(404, "project unavailable")
			}
			return dashboardTarget{Root: physical, Kind: "code", ID: project, Title: project, Edit: userAccessForClaims(claims).CanEdit}, nil
		}
	}
	if !isReportRunWorkflowRoot(clean) {
		return target, dashboardFailure(400, "Workflow, Relay, Crew or owned Code root required")
	}
	access, manifest := workflowAccessForWorkspacePath(ctx, claims, clean)
	if access == WorkflowAccessNone || manifest == nil || !userAllowedWorkflowID(claims, manifest.ID) {
		return target, dashboardFailure(404, "project unavailable")
	}
	if claims.AccessToken != nil && !claims.AccessToken.AllowsWorkflow(manifest.ID) {
		return target, dashboardFailure(404, "project unavailable")
	}
	kind := "workflow"
	if manifest.Kind == "relay" {
		kind = "relay"
	}
	return dashboardTarget{Root: clean, Kind: kind, ID: manifest.ID, Title: manifest.Label, Edit: (access == WorkflowAccessOwner || access == WorkflowAccessWrite) && userAccessForClaims(claims).CanEdit}, nil
}
func dashboardWorkspaceRequest(ctx context.Context, req dashboards.Request) (dashboards.Result, error) {
	var out dashboards.Result
	data, err := json.Marshal(req)
	if err != nil {
		return out, err
	}
	upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, getWorkspaceAPIURL()+"/api/dashboards", bytes.NewReader(data))
	if err != nil {
		return out, err
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("X-Workspace-Token", os.Getenv("WORKSPACE_API_TOKEN"))
	response, err := workspaceHTTPClient.Do(upstream)
	if err != nil {
		return out, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		var failure struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 16384)).Decode(&failure) != nil {
			failure.Error = "dashboard workspace unavailable"
		}
		return out, dashboardFailure(response.StatusCode, failure.Error)
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&out)
	return out, err
}
func (api *StreamingAPI) resolveDashboardDocument(r *http.Request, root, p string) (string, error) {
	if !strings.HasPrefix(p, dashboards.Prefix) && !strings.HasPrefix(p, "code/reports/managed/") {
		return p, nil
	}
	claims := GetUserFromContext(r.Context())
	target, err := api.dashboardTarget(r.Context(), claims, root)
	// A preview token was minted after the ordinary target authorization.
	draft := false
	if claims != nil && claims.Scope == reportPreviewScope && claims.ScopeWorkspace == root && claims.PreviewConnectionID == "" {
		draft = true
		target.Root = root
		err = nil
	}
	if err != nil {
		return "", err
	}
	result, err := dashboardWorkspaceRequest(r.Context(), dashboards.Request{Root: target.Root, Action: "resolve", DocumentPath: p, IncludeDraft: draft || target.Edit && (claims.AccessToken == nil || claims.AccessToken.Allows("dashboards:write")), Guard: dashboardFileGuard(claims)})
	return result.ResolvedPath, err
}
func (api *StreamingAPI) dashboardValidation(ctx context.Context, claims *UserClaims, target dashboardTarget, p string) (string, error) {
	client := api.reportPreviewWorkspaceClient(ctx, claims, target.Root)
	data, err := client.DownloadFile(ctx, target.Root+"/"+p)
	if err != nil {
		return "", err
	}
	hooks := workshop.ReportHTMLValidationHooks{
		ExplainSQL: func(ctx context.Context, sql string) error {
			_, err := client.QueryAuthorizedWorkflowDB(ctx, workspace.QueryWorkflowDBParams{DBPath: target.Root + "/db/db.sqlite", SQL: "EXPLAIN " + sql, PrepareOnly: true})
			return err
		},
		FileExists: func(ctx context.Context, p string) (bool, error) {
			_, err := client.DownloadFile(ctx, target.Root+"/"+p)
			if err == nil {
				return true, nil
			}
			if strings.Contains(err.Error(), "404") || strings.Contains(strings.ToLower(err.Error()), "not found") {
				return false, nil
			}
			return false, err
		},
	}
	return workshop.ValidateDashboardHTML(ctx, p, string(data), hooks)
}
func dashboardError(w http.ResponseWriter, err error) {
	code, message := wf.ErrorDetails(err)
	if wf.StatusCode(err) == 500 {
		message = "dashboard service unavailable"
	}
	externalError(w, wf.StatusCode(err), code, message)
}

// handleDashboards is shared by the browser API and the external MCP adapter.
// The authenticated transport supplies claims; JSON cannot set roles or draft rights.
func (api *StreamingAPI) handleDashboards(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Workspace string            `json:"workspace"`
		Action    string            `json:"action"`
		ID        string            `json:"dashboard_id"`
		Title     string            `json:"title"`
		Files     map[string]string `json:"files"`
		Remove    []string          `json:"remove"`
		Expected  string            `json:"expected_revision"`
		Revision  string            `json:"revision"`
		Document  string            `json:"document_path"`
		Theme     string            `json:"theme"`
		Width     string            `json:"width"`
		Limit     int               `json:"limit"`
		Offset    int               `json:"offset"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&args) != nil || decoder.Decode(new(any)) != io.EOF {
		externalError(w, 400, "invalid_arguments", "invalid dashboard request")
		return
	}
	claims := GetUserFromContext(r.Context())
	w.Header().Set("Cache-Control", "no-store")
	if !dashboardScopeAllowed(claims, args.Action) {
		externalError(w, 403, "insufficient_scope", "dashboard access or explicit dashboard consent required; preview additionally requires runs:execute")
		return
	}
	if args.Action == "list" && args.Workspace == "" {
		api.listAccessibleDashboards(w, r, args.Limit, args.Offset)
		return
	}
	target, err := api.dashboardTarget(r.Context(), claims, args.Workspace)
	if err != nil {
		dashboardError(w, err)
		return
	}
	if (dashboardMutation(args.Action) || args.Action == "preview") && !target.Edit {
		externalError(w, 403, "forbidden", "project edit access required")
		return
	}
	includeDraft := target.Edit && (claims.AccessToken == nil || claims.AccessToken.Allows("dashboards:write"))
	if strings.HasPrefix(args.ID, "db/reports/") {
		args.Document = args.ID
		args.ID = ""
	}
	if strings.HasPrefix(args.Document, dashboards.Prefix) {
		parts := strings.Split(strings.TrimPrefix(args.Document, dashboards.Prefix), "/")
		if len(parts) < 2 {
			externalError(w, 400, "invalid_path", "invalid dashboard document")
			return
		}
		args.ID = parts[0]
		if len(parts) > 2 {
			args.Revision = parts[1]
		}
	}
	req := dashboards.Request{Root: target.Root, Action: args.Action, ID: args.ID, Title: args.Title, Files: args.Files, Remove: args.Remove, ExpectedRevision: args.Expected, Revision: args.Revision, DocumentPath: args.Document, IncludeDraft: includeDraft}
	if claims.AccessToken != nil {
		req.Guard = claims.AccessToken.FileGuard
	}
	if args.Action == "get" || args.Action == "validate" || args.Action == "preview" || args.Action == "link" {
		req.Action = "get"
		if args.Document != "" {
			p, err := cleanReportLinkPath(args.Document)
			if err != nil {
				externalError(w, 400, "invalid_path", err.Error())
				return
			}
			if guard := dashboardFileGuard(claims); guard != nil && !guard.Allows(p, false) {
				externalError(w, 403, "protected_path", "outside connection read grants")
				return
			}
			// Legacy documents remain available through the same validation/preview/link service.
			if !strings.HasPrefix(p, dashboards.Prefix) {
				api.dashboardDocumentAction(w, r, target, args.Action, p, args.Theme, args.Width)
				return
			}
		}
	}
	if args.Action == "publish" || args.Action == "restore" {
		check := req
		check.Action = "get"
		if args.Action == "publish" {
			check.Revision = args.Expected
		}
		draft, err := dashboardWorkspaceRequest(r.Context(), check)
		if err != nil {
			dashboardError(w, err)
			return
		}
		validation, err := api.dashboardValidation(r.Context(), claims, target, draft.ResolvedPath)
		if err != nil {
			dashboardError(w, err)
			return
		}
		var checked struct {
			Valid bool `json:"valid"`
		}
		if json.Unmarshal([]byte(validation), &checked) != nil || !checked.Valid {
			externalError(w, 422, "invalid_dashboard", validation)
			return
		}
	}
	result, err := dashboardWorkspaceRequest(r.Context(), req)
	if err != nil {
		dashboardError(w, err)
		return
	}
	if args.Action == "validate" || args.Action == "preview" || args.Action == "link" {
		p := result.ResolvedPath
		if args.Action == "link" {
			if result.Dashboard.Published == "" {
				externalError(w, 404, "not_published", "publish the dashboard before sharing its live link")
				return
			}
			p = result.Dashboard.DocumentPath
		}
		api.dashboardDocumentAction(w, r, target, args.Action, p, args.Theme, args.Width)
		return
	}
	if args.Action == "list" {
		rows := []map[string]any{}
		for _, d := range result.Dashboards {
			rows = append(rows, dashboardDiscoveryRow(target, d))
		}
		writeDashboardPage(w, rows, args.Limit, args.Offset)
		return
	}
	externalJSON(w, result)
}
func (api *StreamingAPI) dashboardDocumentAction(w http.ResponseWriter, r *http.Request, target dashboardTarget, action, p, theme, width string) {
	claims := GetUserFromContext(r.Context())
	switch action {
	case "get":
		client := api.reportPreviewWorkspaceClient(r.Context(), claims, target.Root)
		data, err := client.DownloadFile(r.Context(), target.Root+"/"+p)
		if err != nil {
			dashboardError(w, err)
			return
		}
		externalJSON(w, map[string]any{"dashboard_id": p, "document_path": p, "revision": wf.Revision(data), "managed": false, "files": map[string]string{"index.html": string(data)}})
	case "validate":
		out, err := api.dashboardValidation(r.Context(), claims, target, p)
		if err != nil {
			dashboardError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(out))
	case "preview":
		out, err := api.runReportPreview(r.Context(), "dashboard-"+claims.UserID, claims.UserID, target.Root, map[string]interface{}{"document_path": p, "theme": theme, "width": width})
		if err != nil {
			dashboardError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(out))
	case "link":
		resolved, err := api.resolveDashboardDocument(r, target.Root, p)
		if err != nil {
			dashboardError(w, err)
			return
		}
		if _, err = api.reportPreviewWorkspaceClient(r.Context(), claims, target.Root).DownloadFile(r.Context(), target.Root+"/"+resolved); err != nil {
			externalError(w, 404, "not_found", "dashboard document unavailable")
			return
		}
		base := effectiveShareBaseURL()
		if base == "" {
			externalError(w, 503, "public_url_missing", "PUBLIC_URL must be configured")
			return
		}
		url := sharedAssetPublicURLForUser(base, "report", target.Root, "")
		parsed, err := parseDashboardURL(url, p)
		if err != nil {
			dashboardError(w, err)
			return
		}
		out := map[string]any{"url": parsed, "document_path": p, "workspace": target.Root, "authentication": "Recipients must sign in and have current project access. This URL grants no access."}
		addShareabilityMetadata(out, base)
		externalJSON(w, out)
	}
}

func dashboardFileGuard(claims *UserClaims) *wf.FolderGuard {
	if claims != nil && claims.AccessToken != nil {
		return claims.AccessToken.FileGuard
	}
	return nil
}

func activeDashboardPreviewConnection(ctx context.Context, id string) (accesstokens.Token, error) {
	if strings.HasPrefix(id, "oauth-") {
		grant, err := mcpOAuthFamilyActive(ctx, strings.TrimPrefix(id, "oauth-"))
		return mcpOAuthTokenForGrant(grant), err
	}
	store, err := openAccessTokens()
	if err != nil {
		return accesstokens.Token{}, err
	}
	defer store.Close()
	return store.Active(ctx, id, time.Now())
}
