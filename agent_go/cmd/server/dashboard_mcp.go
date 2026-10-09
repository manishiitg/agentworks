package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

var dashboardActions = map[string]string{"list_dashboards": "list", "get_dashboard": "get", "create_dashboard": "create", "update_dashboard": "update", "validate_dashboard": "validate", "preview_dashboard": "preview", "publish_dashboard": "publish", "restore_dashboard": "restore", "get_dashboard_link": "link", "get_report_link": "link"}

func isDashboardTool(name string) bool { return dashboardActions[name] != "" }
func dashboardToolDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	for _, name := range []string{"list_dashboards", "get_dashboard", "create_dashboard", "update_dashboard", "validate_dashboard", "preview_dashboard", "publish_dashboard", "restore_dashboard", "get_dashboard_link"} {
		action := dashboardActions[name]
		props := map[string]any{
			"workflow_id": externalString("Workflow or Relay ID from list_workflows or list_dashboards. Pass one of workflow_id, crew_id or workspace."),
			"crew_id":     externalString("Crew ID from list_crews or list_dashboards."),
			"workspace":   externalString("Project root from list_dashboards; needed only for an owned Code project."),
		}
		required := []string{}
		if action == "list" {
			props["limit"] = externalInteger(1, 200)
			props["offset"] = externalInteger(0, 10000)
			required = nil
			props["workspace"] = externalString("Optional project root. Omit (and workflow_id/crew_id) to discover dashboards across accessible projects.")
		}
		if action != "list" {
			props["document_path"] = externalString("Existing HTML dashboard path from list_dashboards, for get/validate/preview/link.")
			props["dashboard_id"] = externalString("Dashboard slug returned by list_dashboards, or the new slug for create_dashboard.")
			if action == "create" || action == "update" || action == "publish" || action == "restore" {
				required = append(required, "dashboard_id")
			}
		}
		if action == "create" || action == "update" {
			props["title"] = externalString("Dashboard title (at most 200 characters).")
			props["files"] = map[string]any{"type": "object", "maxProperties": 100, "additionalProperties": map[string]any{"type": "string"}, "description": "File contents by dashboard-relative path. Binary images/fonts use matching base64 data URLs. index.html is the entry, assets may be HTML/CSS/JS/JSON/SVG/text/CSV, Python/JS scripts go under scripts/. Use {{dashboard_assets}} and {{dashboard_scripts}} in source to address this exact revision."}
			if action == "create" {
				required = append(required, "title", "files")
			} else {
				props["remove"] = map[string]any{"type": "array", "maxItems": 100, "items": externalString("File to remove.")}
			}
		}
		if dashboardMutation(action) && action != "create" {
			props["expected_revision"] = externalString("Current draft revision from get_dashboard; stale edits/publications are refused.")
			required = append(required, "expected_revision")
		}
		if action == "restore" || action == "get" || action == "validate" || action == "preview" {
			props["revision"] = externalString("Exact immutable revision. Omit for the current draft (editors with dashboards:write) or published version (readers).")
			if action == "restore" {
				required = append(required, "revision")
			}
		}
		if action == "preview" {
			props["theme"] = map[string]any{"type": "string", "enum": []any{"light", "dark", "both"}}
			props["width"] = map[string]any{"type": "string", "enum": []any{"mobile", "tablet", "desktop", "all"}}
		}
		descriptions := map[string]string{
			"list":     "Discover only accessible published dashboards (drafts for authorized editors), with project identities, document paths and authenticated live URLs. Existing HTML dashboards are included.",
			"get":      "Read a dashboard bundle and revision. Draft source requires edit rights and dashboards:write consent.",
			"create":   "Create a new dashboard draft from supplied HTML, assets and optional scripts. Does not publish or select new data sources. Requires dashboards:write consent and project edit access.",
			"update":   "Update supplied draft files/title and optionally remove files, preserving omitted files. Creates an immutable revision; published viewers keep seeing the published bundle.",
			"validate": "Validate the exact dashboard revision with the same HTML, SQL schema and referenced-file checks as the app Builder.",
			"preview":  "Render the exact dashboard revision with the app's real browser runtime; returns state, data/script errors and screenshots. Requires project edit rights, dashboards:read and runs:execute (live scripts may use your allowed selected sources).",
			"publish":  "Validate and publish the expected immutable draft. Atomically switches the live dashboard pointer without changing its authenticated URL.",
			"restore":  "Restore a previously published revision, using the current draft revision as a concurrency check.",
			"link":     "Return a published dashboard's authenticated live URL directly. Recipients need current project access; URLs grant no access and contain no credential.",
		}
		add(name, descriptions[action], dashboardMutation(action), false, props, required...)
	}
	// Replace the old Run-assistant proxy with a native read operation. Keep its call shape.
	add("get_report_link", "Return a live dashboard URL directly, without starting a Run conversation. document_path defaults to db/reports/index.html; supports Goals and Relays. Requires dashboards:read consent.", false, true, map[string]any{"document_path": externalString("HTML document under db/reports/.")})
}
func (api *StreamingAPI) externalDashboardCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) {
	payload := map[string]any{"action": dashboardActions[name]}
	for key, value := range args {
		payload[key] = value
	}
	// Projects are named by ID like every other MCP tool; dashboardTarget
	// re-checks access on the resolved root.
	if name != "get_report_link" {
		workflowID, crewID, workspace := externalArg(args, "workflow_id"), externalArg(args, "crew_id"), externalArg(args, "workspace")
		named := 0
		for _, value := range []string{workflowID, crewID, workspace} {
			if value != "" {
				named++
			}
		}
		if named > 1 || (named == 0 && dashboardActions[name] != "list") {
			externalError(w, 400, "invalid_arguments", "Pass exactly one of workflow_id, crew_id or workspace.")
			return
		}
		delete(payload, "workflow_id")
		delete(payload, "crew_id")
		switch {
		case workflowID != "":
			root, ok := dashboardWorkflowRoot(r, workflowID)
			if !ok {
				externalError(w, 404, "not_found", "project unavailable")
				return
			}
			payload["workspace"] = root
		case crewID != "":
			crew, _, _, ok := api.externalCrewResolve(r.Context(), GetUserFromContext(r.Context()), crewID)
			if !ok {
				externalError(w, 404, "not_found", "project unavailable")
				return
			}
			payload["workspace"] = crew.Binding.WorkspacePath
		}
	}
	if name == "get_report_link" {
		discovered, err := DiscoverWorkflowManifests(r.Context())
		if err != nil {
			dashboardError(w, err)
			return
		}
		delete(payload, "workflow_id")
		delete(payload, "session_id")
		found := false
		for _, item := range discovered {
			if item.Manifest != nil && item.Manifest.ID == externalArg(args, "workflow_id") {
				payload["workspace"] = item.WorkspacePath
				found = true
				break
			}
		}
		if !found {
			externalError(w, 404, "not_found", "project unavailable")
			return
		}
		if payload["document_path"] == nil {
			payload["document_path"] = "db/reports/index.html"
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		dashboardError(w, err)
		return
	}
	clone := r.Clone(r.Context())
	clone.Body = io.NopCloser(bytes.NewReader(encoded))
	api.handleDashboards(w, clone)
}

// dashboardWorkflowRoot is the workspace of a workflow or Relay ID.
func dashboardWorkflowRoot(r *http.Request, workflowID string) (string, bool) {
	discovered, err := DiscoverWorkflowManifests(r.Context())
	if err != nil {
		return "", false
	}
	for _, item := range discovered {
		if item.Manifest != nil && item.Manifest.ID == workflowID {
			return item.WorkspacePath, true
		}
	}
	return "", false
}

func parseDashboardURL(raw, p string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	q := u.Query()
	if p != "db/reports/index.html" {
		q.Set("document", p)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
func (api *StreamingAPI) listAccessibleDashboards(w http.ResponseWriter, r *http.Request, limit, offset int) {
	claims := GetUserFromContext(r.Context())
	roots := []string{}
	discovered, err := DiscoverWorkflowManifests(r.Context())
	if err != nil {
		dashboardError(w, err)
		return
	}
	for _, item := range filterWorkflowManifestsForUser(claims, discovered) {
		if item.Manifest == nil {
			continue
		}
		if claims.AccessToken == nil || claims.AccessToken.AllowsWorkflow(item.Manifest.ID) {
			roots = append(roots, item.WorkspacePath)
		}
	}
	if userAllowedProduct(claims, "work") {
		crews, err := api.externalCrewsVisible(r.Context(), claims, "")
		if err != nil {
			dashboardError(w, err)
			return
		}
		for _, crew := range crews {
			if root, ok := crew["workspace_path"].(string); ok {
				roots = append(roots, root)
			}
		}
	}
	// Codes are private and enumerated only under this caller's own tree.
	if userAllowedProduct(claims, "code") && api.agentProfiles != nil {
		profile, err := api.agentProfiles.Resolve("code", 0, claims.UserID)
		if err == nil {
			paths, _, err := listProjectManifestPaths(r.Context(), defaultProductProjectStore(), claims.UserID, profile)
			if err == nil {
				for _, p := range paths {
					roots = append(roots, strings.TrimSuffix(p, "/product.json"))
				}
			}
		}
	}
	rows := []map[string]any{}
	seen := map[string]bool{}
	failed := []string{}
	for _, root := range roots {
		target, err := api.dashboardTarget(r.Context(), claims, root)
		if err != nil || seen[target.Root] {
			continue
		}
		seen[target.Root] = true
		result, err := dashboardWorkspaceRequest(r.Context(), dashboards.Request{Root: target.Root, Action: "list", Guard: dashboardFileGuard(claims), IncludeDraft: target.Edit && (claims.AccessToken == nil || claims.AccessToken.Allows("dashboards:write"))})
		if err != nil {
			failed = append(failed, target.Root)
			continue
		}
		for _, d := range result.Dashboards {
			row := dashboardDiscoveryRow(target, d)
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		return strings.Compare(rows[i]["workspace"].(string)+"/"+rows[i]["dashboard_id"].(string), rows[j]["workspace"].(string)+"/"+rows[j]["dashboard_id"].(string)) < 0
	})
	start, end := dashboardPageBounds(rows, limit, offset)
	externalJSON(w, map[string]any{"dashboards": rows[start:end], "total": len(rows), "next_offset": end, "has_more": end < len(rows), "unavailable_projects": failed, "authentication": "Sign in with current project access; URLs grant no access.", "note": "Private Code dashboards remain owner-only. For existing HTML documents use document_path with validation/preview/link."})
}

func dashboardDiscoveryRow(target dashboardTarget, d dashboards.Dashboard) map[string]any {
	row := map[string]any{"dashboard_id": d.ID, "title": d.Title, "workspace": target.Root, "project_id": target.ID, "project_title": target.Title, "product": target.Kind, "document_path": d.DocumentPath, "revision": d.Revision, "published_revision": d.Published, "managed": d.Managed, "can_edit": target.Edit}
	switch target.Kind {
	case "workflow", "relay":
		row["workflow_id"] = target.ID
	case "crew":
		row["crew_id"] = target.ID
	}
	if (!d.Managed || d.Published != "") && effectiveShareBaseURL() != "" {
		raw := sharedAssetPublicURLForUser(effectiveShareBaseURL(), "report", target.Root, "")
		link, err := parseDashboardURL(raw, d.DocumentPath)
		if err == nil {
			row["url"] = link
			addShareabilityMetadata(row, effectiveShareBaseURL())
		}
	}
	return row
}

func dashboardPageBounds(rows []map[string]any, limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, 200)
	offset = max(0, offset)
	start := min(offset, len(rows))
	return start, min(start+limit, len(rows))
}
func writeDashboardPage(w http.ResponseWriter, rows []map[string]any, limit, offset int) {
	start, end := dashboardPageBounds(rows, limit, offset)
	externalJSON(w, map[string]any{"dashboards": rows[start:end], "total": len(rows), "next_offset": end, "has_more": end < len(rows)})
}
