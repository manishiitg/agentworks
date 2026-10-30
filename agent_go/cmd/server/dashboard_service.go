package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	workshop "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
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

// Dashboard drafts follow website project roles and existing product authoring
// consent. A nil target is catalog discovery; dispatch always binds a target.
func dashboardAuthoringAllowed(claims *UserClaims, target *dashboardTarget) bool {
	if claims == nil {
		return false
	}
	access := userAccessForClaims(claims)
	if access.Disabled || !access.CanEdit || target != nil && !target.Edit {
		return false
	}
	if target != nil {
		product := map[string]string{"workflow": "agentworks", "relay": "relays", "crew": "work"}[target.Kind]
		if product == "" || !userAllowedProduct(claims, product) {
			return false
		}
	}
	if claims.AccessToken == nil {
		return true
	}
	token := claims.AccessToken
	if !token.Allows("dashboards:read") {
		return false
	}
	if target == nil {
		return externalFileAuthoringAllowed(claims, nil) || token.Allows("crews:write") && userAllowedProduct(claims, "work") || externalCodeRunAllowed(claims)
	}
	switch target.Kind {
	case "workflow":
		return token.BuilderAccess() && token.AllowsWorkflow(target.ID)
	case "relay":
		return (token.BuilderAccess() || token.RelayBuilderAccess()) && token.AllowsWorkflow(target.ID)
	case "crew":
		return token.Allows("crews:write") && token.AllowsCrew(target.ID)
	}
	return false
}

func dashboardScopeAllowed(claims *UserClaims, action string) bool {
	// Uploads reuse Crew attachment use-access, not dashboard authoring consent.
	if action == "upload" {
		return claims != nil && claims.Scope == "" && !userAccessForClaims(claims).Disabled
	}
	if claims == nil || userAccessForClaims(claims).Disabled {
		return false
	}
	if (dashboardMutation(action) || action == "share" || dashboardOwnersWrite(action) || action == "export_motion" || action == "export_static") && !userAccessForClaims(claims).CanEdit {
		return false
	}
	if claims.AccessToken == nil {
		return true
	}
	if !claims.AccessToken.Allows("dashboards:read") {
		return false
	}
	if (dashboardMutation(action) || action == "share" || dashboardOwnersWrite(action) || action == "export_motion" || action == "export_static") && !dashboardAuthoringAllowed(claims, nil) {
		return false
	}
	if (action == "preview" || action == "export_motion" || action == "export_static") && !claims.AccessToken.Allows("runs:execute") {
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
		product, _, _ := projectProductForPath(crew.Root)
		manifest, err := readCrewProjectManifests(ctx, product.ProfileID, crew.Root)
		if err != nil {
			return target, dashboardFailure(404, "project unavailable")
		}
		if manifest.ID != "" {
			id = manifest.ID
		}
		if displayTitle := manifest.displayTitle(); displayTitle != "" {
			title = displayTitle
		}
		if claims.AccessToken != nil && (!workspaceDashboardTokenAllows(claims, product.ProfileID, id)) {
			return target, dashboardFailure(404, "project unavailable")
		}
		kind := "crew"

		return dashboardTarget{Root: crew.Root, Kind: kind, ID: id, Title: title, Edit: crewAccessFor(claims, crew) == crewAccessOwner && userAccessForClaims(claims).CanEdit}, nil
	}

	if !isReportRunWorkflowRoot(clean) {
		return target, dashboardFailure(400, "Workflow, Flow, Crew or owned Code root required")
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
	if !dashboardMutation(req.Action) {
		if publication, e := readPublishedProject(ctx, req.Root); e != nil {
			return dashboards.Result{}, e
		} else if publishedDashboardSource(publication) {
			req.Root = publication.sourceRoot()
		}
	}
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

func publishedDashboardSource(publication *publishedProject) bool {
	if publication != nil {
		for _, file := range publication.SourcePaths {
			if strings.HasPrefix(file, "db/reports/") && strings.HasSuffix(strings.ToLower(file), ".html") {
				return true
			}
		}
	}
	return false
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
	result, err := dashboardWorkspaceRequest(r.Context(), dashboards.Request{Root: target.Root, Action: "resolve", DocumentPath: p, IncludeDraft: draft || dashboardAuthoringAllowed(claims, &target), Guard: dashboardFileGuard(claims)})
	return result.ResolvedPath, err
}
func (api *StreamingAPI) dashboardValidation(ctx context.Context, claims *UserClaims, target dashboardTarget, p string) (string, error) {
	client := api.reportPreviewWorkspaceClient(ctx, claims, target.Root)
	data, err := client.DownloadFile(ctx, publishedReadPath(ctx, target.Root+"/"+p))
	if err != nil {
		return "", err
	}
	hooks := workshop.ReportHTMLValidationHooks{
		ExplainSQL: func(ctx context.Context, sql string) error {
			_, err := client.QueryAuthorizedWorkflowDB(ctx, workspace.QueryWorkflowDBParams{DBPath: target.Root + "/db/db.sqlite", SQL: "EXPLAIN " + sql, PrepareOnly: true})
			return err
		},
		FileExists: func(ctx context.Context, p string) (bool, error) {
			_, err := client.DownloadFile(ctx, publishedReadPath(ctx, target.Root+"/"+p))
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
	code, _ := wf.ErrorDetails(err)
	// Keep the workspace or adapter's actual refusal. Generic file-error text
	// hides the reason a dashboard could not be opened, checked or previewed.
	message := strings.ReplaceAll(err.Error(), strings.TrimRight(fsutil.WorkspaceDocsRoot(), "/\\")+string(os.PathSeparator), "")
	externalError(w, wf.StatusCode(err), code, message)
}

// handleDashboards is shared by the browser API and the external MCP adapter.
// The authenticated transport supplies claims; JSON cannot set roles or draft rights.
func (api *StreamingAPI) handleDashboards(w http.ResponseWriter, r *http.Request) {
	var args struct {
		ExportID      string            `json:"export_id"`
		Submission    string            `json:"submission_id"`
		Composition   string            `json:"composition_path"`
		SourceMode    string            `json:"source_mode"`
		ExportWidth   int               `json:"viewport_width"`
		ExportHeight  int               `json:"viewport_height"`
		Duration      float64           `json:"duration_s"`
		FPS           int               `json:"fps"`
		Workspace     string            `json:"workspace"`
		Action        string            `json:"action"`
		FileName      string            `json:"file_name"`
		ContentBase64 string            `json:"content_base64"`
		ID            string            `json:"dashboard_id"`
		Title         string            `json:"title"`
		Motion        *bool             `json:"motion"`
		Artifact      *bool             `json:"artifact"`
		Files         map[string]string `json:"files"`
		Remove        []string          `json:"remove"`
		Expected      string            `json:"expected_revision"`
		Revision      string            `json:"revision"`
		Document      string            `json:"document_path"`
		Theme         string            `json:"theme"`
		Width         string            `json:"width"`
		Limit         int               `json:"limit"`
		Offset        int               `json:"offset"`
		Mode          string            `json:"mode"`
		SharedWithMe  bool              `json:"shared_with_me"`
		PublishedOnly bool              `json:"published_only"`
		Emails        []string          `json:"emails"`
		crewAgentShareChange
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
		if args.Action == "share" {
			logDashboardShareRefusal(claims, dashboardTarget{Root: args.Workspace}, args.Mode, "scope", dashboardFailure(403, "dashboard access and project authoring consent required"), nil)
		}
		externalError(w, 403, "insufficient_scope", "dashboard access or explicit dashboard consent required; preview and exports additionally require runs:execute")
		return
	}
	if args.Action == "catalog" {
		externalJSON(w, dashboards.AuthoringCatalog())
		return
	}
	if args.Action == "list" && args.Workspace == "" {
		api.listAccessibleDashboards(w, r, args.Limit, args.Offset, args.SharedWithMe, args.PublishedOnly)
		return
	}
	target, err := api.dashboardTarget(r.Context(), claims, args.Workspace)
	if err != nil {
		if args.Action == "share" {
			logDashboardShareRefusal(claims, dashboardTarget{Root: args.Workspace}, args.Mode, "target", err, nil)
		}
		dashboardError(w, err)
		return
	}
	if args.Action == "list" && !target.Edit && (target.Kind == "crew") && claims.AccessToken == nil && claims.Scope == "" && claims.Provider != "bot_route" {
		if _, _, err := api.ensureDefaultDashboard(r.Context(), claims, target); err != nil {
			dashboardError(w, err)
			return
		}
	}
	if args.Action == "upload" {
		api.dashboardUploadBase64(w, r, target.Root, args.FileName, args.ContentBase64)
		return
	}
	if dashboardOwnersAction(args.Action) {
		api.handleDashboardOwners(w, r, claims, target, args.Action, args.Emails)
		return
	}
	if dashboardShareAction(args.Action) {
		api.handleDashboardShare(w, r, claims, target, args.Action, args.Mode, args.Emails, args.crewAgentShareChange)
		return
	}
	if args.Action == "export_static" {
		api.dashboardStaticExport(w, r, target, dashboardExportOptions{Dashboard: args.ID, Revision: args.Revision, Theme: args.Theme, Width: args.ExportWidth})
		return
	}
	if args.Action == "export_motion" {
		api.dashboardMotionOperation(w, r, target, args.Composition, args.ExportID, args.Submission, dashboardExportOptions{SourceMode: args.SourceMode, Dashboard: args.ID, Revision: args.Revision, Theme: args.Theme, Width: args.ExportWidth, Height: args.ExportHeight, Duration: args.Duration, FPS: args.FPS})
		return
	}
	if (dashboardMutation(args.Action) || args.Action == "preview") && !target.Edit {
		externalError(w, 403, "forbidden", "project edit access required")
		return
	}
	if dashboardMutation(args.Action) {
		if p, e := readPublishedProject(r.Context(), target.Root); e != nil {
			dashboardError(w, e)
			return
		} else if p != nil {
			externalError(w, 409, "published_source", "To change this dashboard, publish again from your computer.")
			return
		}
	}
	includeDraft := dashboardAuthoringAllowed(claims, &target)
	if (dashboardMutation(args.Action) || args.Action == "preview") && !includeDraft {
		externalError(w, 403, "insufficient_scope", "Dashboard editing requires existing authoring consent for this project and its current owner/editor role.")
		return
	}
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
	req := dashboards.Request{Root: target.Root, Action: args.Action, ID: args.ID, Title: args.Title, Motion: args.Motion, Artifact: args.Artifact, Files: args.Files, Remove: args.Remove, ExpectedRevision: args.Expected, Revision: args.Revision, DocumentPath: args.Document, IncludeDraft: includeDraft}
	if args.Action == "list" && args.PublishedOnly {
		req.IncludeDraft = false
	}
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
				api.dashboardDocumentAction(w, r, target, args.Action, p, args.Theme, args.Width, "")
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
		log.Printf("[DASHBOARD_REQUEST] failed: action=%q kind=%q project_id=%q dashboard_id=%q document_path=%q status=%d", args.Action, target.Kind, target.ID, args.ID, args.Document, wf.StatusCode(err))
		dashboardError(w, err)
		return
	}
	if args.Action == "validate" || args.Action == "preview" || args.Action == "link" {
		p := result.ResolvedPath
		shortURL := ""
		if args.Action == "link" {
			if result.Dashboard.Published == "" {
				externalError(w, 404, "not_published", "publish the dashboard before sharing its live link")
				return
			}
			p = result.Dashboard.DocumentPath
			// A managed dashboard has a short /d/<code> link; people get that one, not the long path.
			if result.Dashboard.Managed {
				row := dashboardDiscoveryRow(target, *result.Dashboard)
				addDashboardQuestionResources(r.Context(), []dashboardQuestionDiscovery{{target, *result.Dashboard, row}})
				shortURL, _ = row["short_url"].(string)
			}
		}
		api.dashboardDocumentAction(w, r, target, args.Action, p, args.Theme, args.Width, shortURL)
		return
	}
	if args.Action == "list" {
		// A new private Crew or Goal needs the same starting dashboard as a shared
		// one. Only its Builder with existing authoring consent creates it;
		// Run requests, bounded file connections, Local projects and published
		// laptop source never acquire writes through catalog discovery.
		if len(result.Dashboards) == 0 && includeDraft && req.Guard == nil && (target.Kind == "crew" || target.Kind == "workflow") {
			publication, err := readPublishedProject(r.Context(), target.Root)
			if err != nil {
				dashboardError(w, err)
				return
			}
			if publication == nil {
				if _, _, err := api.ensureDefaultDashboard(r.Context(), claims, target); err != nil {
					dashboardError(w, err)
					return
				}
				result, err = dashboardWorkspaceRequest(r.Context(), req)
				if err != nil {
					dashboardError(w, err)
					return
				}
			}
		}
		rows := []map[string]any{}
		resources := []dashboardQuestionDiscovery{}
		for _, d := range result.Dashboards {
			if args.PublishedOnly && d.Managed && d.Published == "" {
				continue
			}
			viewTarget := target
			viewTarget.Edit = includeDraft
			row := dashboardDiscoveryRow(viewTarget, d)
			resources = append(resources, dashboardQuestionDiscovery{viewTarget, d, row})
			rows = append(rows, row)
		}
		addDashboardQuestionResources(r.Context(), resources)
		writeDashboardPage(w, rows, args.Limit, args.Offset)
		return
	}
	if args.Action == "get" && result.Dashboard != nil && result.Dashboard.Managed {
		resource := map[string]any{}
		addDashboardQuestionResource(r.Context(), target, *result.Dashboard, resource)
		id, _ := resource["resource_id"].(string)
		shortPath, _ := resource["short_path"].(string)
		externalJSON(w, struct {
			dashboards.Result
			ResourceID string `json:"resource_id,omitempty"`
			ShortPath  string `json:"short_path,omitempty"`
		}{result, id, shortPath})
		return
	}
	externalJSON(w, result)
}
func (api *StreamingAPI) dashboardDocumentAction(w http.ResponseWriter, r *http.Request, target dashboardTarget, action, p, theme, width, shortURL string) {
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
		if shortURL != "" {
			out["long_url"], out["url"] = parsed, shortURL
		}
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

// Code-only connections historically have no Crew audience field. Explicit
// machine Crew allowlists still cap every project they address.
func workspaceDashboardTokenAllows(claims *UserClaims, profileID, id string) bool {
	if claims == nil || claims.AccessToken == nil {
		return claims != nil
	}
	t := claims.AccessToken
	if t.ProjectKind != "" {
		return t.ProjectKind == profileID && t.AllowsCrew(id) && t.Allows("crews:read")
	}
	if profileID == crewProfileID {
		return userAllowedProduct(claims, crewProfileID) && (t.Allows("crews:read") || t.Allows("crews:run") || t.Allows("crews:write")) && (len(t.CrewIDs) == 0 || t.AllowsCrew(id))
	}
	return t.AllowsCrew(id)
}
