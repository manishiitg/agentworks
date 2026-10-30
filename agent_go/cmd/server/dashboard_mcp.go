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

var dashboardActions = map[string]string{"upload_dashboard_asset": "upload", "get_dashboard_catalog": "catalog", "export_dashboard_motion": "export_motion", "export_dashboard_static": "export_static", "list_dashboards": "list", "get_dashboard": "get", "create_dashboard": "create", "update_dashboard": "update", "validate_dashboard": "validate", "preview_dashboard": "preview", "publish_dashboard": "publish", "restore_dashboard": "restore", "get_dashboard_link": "link", "get_report_link": "link", "get_dashboard_share": "share_status", "share_dashboard": "share"}

func isDashboardTool(name string) bool { return dashboardActions[name] != "" }
func dashboardToolDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("get_dashboard_catalog", "Read the managed React TSX authoring contract, supported runtime imports, starter source, shared shadcn component props and Recharts imports. Use before authoring. Two formats: TSX (index.tsx) for live dashboards with data, buttons and functions; HTML (index.html) for a quick shareable page such as a decision, a report or a file view. JSON dashboards are not supported. Data, Ask, functions and sharing use the existing project contracts.", false, false, map[string]any{})
	for _, name := range []string{"upload_dashboard_asset", "list_dashboards", "get_dashboard", "create_dashboard", "update_dashboard", "validate_dashboard", "preview_dashboard", "publish_dashboard", "restore_dashboard", "get_dashboard_link"} {
		action := dashboardActions[name]
		props := map[string]any{
			"workflow_id":  externalString("Workflow or Flow ID from workflow action=list or the dashboard list. Pass one of workflow_id, crew_id or workspace."),
			"crew_id":      externalString("Crew ID from workspace action=list or the dashboard list."),
			"workspace":    externalString("Project root from the dashboard list; needed only for an owned Code project."),
			"workspace_id": externalString("Stable work:<id> from workspace list/get. Pass exactly one project selector."),
		}
		required := []string{}
		if action == "upload" {
			props["file_name"] = externalString("Original reference file name. Stored by the existing Crew attachment handler; does not overwrite files.")
			props["content_base64"] = externalString("Base64 file bytes. Existing Crew attachment limits apply: 10 MiB for Builders and 5 MiB for shared users.")
			required = []string{"file_name", "content_base64"}
		}
		if action == "list" {
			props["limit"] = externalInteger(1, 200)
			props["shared_with_me"] = map[string]any{"type": "boolean", "description": "true lists only dashboards other people shared with you."}
			props["published_only"] = map[string]any{"type": "boolean", "description": "true lists live dashboards only, matching the Dashboards page. Unpublished drafts are excluded before pagination."}
			props["offset"] = externalInteger(0, 10000)
			required = nil
			props["workspace"] = externalString("Optional project root. Omit (and workflow_id/crew_id) to discover dashboards across accessible projects.")
		}
		if action != "list" {
			props["document_path"] = externalString("HTML page path from the dashboard list, for get/validate/preview/link.")
			props["dashboard_id"] = externalString("Dashboard slug returned by the dashboard list, or the new slug when creating one.")
			if action == "create" || action == "update" || action == "publish" || action == "restore" {
				required = append(required, "dashboard_id")
			}
		}
		if action == "create" || action == "update" {
			props["title"] = externalString("Dashboard title (at most 200 characters).")
			props["motion"] = map[string]any{"type": "boolean", "description": "true lists this dashboard under Motion dashboards. Use for authored interactive playback; this label does not add animation. Defaults to false on create; omitted on update preserves it. Publish the revision to change the live label."}
			props["artifact"] = map[string]any{"type": "boolean", "description": "true lists this page under Artifacts: a finished page made in a chat to show or share one idea (a proposal, comparison, plan or mock-up), usually index.html alone, with no live data or functions. Defaults to false on create; omitted on update preserves it. Publish the revision to change the live label."}
			props["files"] = map[string]any{"type": "object", "maxProperties": 100, "additionalProperties": map[string]any{"type": "string"}, "description": "File contents by dashboard-relative path. Binary images/fonts use matching base64 data URLs. index.tsx is the React dashboard source; default-export a component and optionally export async loadData(report). Relative TSX/TS/JS/JSON/CSS imports are bundled by the server; see dashboard action=catalog for runtime modules. The server generates index.html/dashboard-app.js/dashboard-app.css. Use shared shadcn components from @agentworks/dashboard and charts from recharts. index.html alone makes a quick shareable HTML page (a decision, a report or a file view). JSON dashboards are not supported. Assets may be HTML/TSX/TS/CSS/JS/JSON/SVG/text/CSV, Python/JS scripts go under scripts/. Use {{dashboard_assets}} and {{dashboard_scripts}} in source to address this exact revision."}
			if action == "create" {
				required = append(required, "title", "files")
			} else {
				props["remove"] = map[string]any{"type": "array", "maxItems": 100, "items": externalString("File to remove.")}
			}
		}
		if dashboardMutation(action) && action != "create" {
			props["expected_revision"] = externalString("Current draft revision from reading the dashboard; stale edits/publications are refused.")
			required = append(required, "expected_revision")
		}
		if action == "restore" || action == "get" || action == "validate" || action == "preview" {
			props["revision"] = externalString("Exact immutable revision. Omit for the current draft (authors with current project edit access) or published version (readers).")
			if action == "restore" {
				required = append(required, "revision")
			}
		}
		if action == "preview" {
			props["theme"] = map[string]any{"type": "string", "enum": []any{"light", "dark", "both"}}
			props["width"] = map[string]any{"type": "string", "enum": []any{"mobile", "tablet", "desktop", "all"}}
		}
		descriptions := map[string]string{
			"upload":   "Upload an attachment to a Crew using the same route and limits as its chat. The server selects the caller’s upload area. Requires current Crew access and crews:run or crews:write. Returns a saved path for chat/functions; never edits dashboard source.",
			"list":     "Discover only accessible published dashboards (drafts for authorized editors), with project identities, document paths and authenticated live URLs. HTML pages are included.",
			"get":      "Read a dashboard bundle and revision. Draft source requires edit rights and existing product authoring consent.",
			"create":   "Create a managed React TSX dashboard draft from index.tsx (dashboard action=catalog). The server compiles and hosts it in Dashboards; no separate npm app/server. TSX is for live dashboards with data, buttons and functions; HTML (index.html) is for a quick shareable page. JSON dashboards are not supported. Does not publish or select new data sources. Uses existing workflow/Flow/Crew/Code authoring consent and current project edit access.",
			"update":   "Update supplied draft files/title and optionally remove files, preserving omitted files. Creates an immutable revision; published viewers keep seeing the published bundle.",
			"validate": "Validate the exact dashboard revision with the same HTML, SQL schema and referenced-file checks as the app Builder.",
			"preview":  "Render the exact dashboard revision with the app's real browser runtime; returns state, data/script errors and screenshots. Requires project edit rights, dashboards:read and runs:execute (live scripts may use your allowed selected sources).",
			"publish":  "Validate and publish the expected immutable draft. Atomically switches the live dashboard pointer without changing its authenticated URL.",
			"restore":  "Restore a previously published revision, using the current draft revision as a concurrency check.",
			"link":     "Return a published dashboard's authenticated live URL directly. Recipients need current project access; URLs grant no access and contain no credential.",
		}
		add(name, descriptions[action], dashboardMutation(action), false, props, required...)
	}
	add("export_dashboard_static", "Export the same published dashboard and current authorized data as a portable static site for Surge, Vercel, Netlify, Cloudflare Pages, GitHub Pages or any file host. Packages the platform React/CSS runtime and captures real report data calls; no SQL extraction, npm app, database or login token is shipped. Returns a folder under publish/exports to deploy in full. Use on every Publish pass to refresh the snapshot. Builder only; existing authoring, dashboards:read, runs:execute and folder grants apply. Ask/functions lead back to the authenticated live dashboard. This exports files only; it does not change sharing or deploy to a host.", true, false, map[string]any{
		"workflow_id":    externalString("Goal or Flow ID. Pass exactly one project selector."),
		"crew_id":        externalString("Crew ID."),
		"workspace":      externalString("Existing project root."),
		"dashboard_id":   externalString("Published dashboard slug. May be omitted when the project has one published dashboard."),
		"revision":       externalString("Exact published revision. Omit for the current publication."),
		"theme":          map[string]any{"type": "string", "enum": []any{"dark", "light"}},
		"viewport_width": externalInteger(320, 3840),
	})
	add("export_dashboard_motion", "Export a published dashboard as an MP4 with its layout, selected theme and frozen data. Native dashboard motion is seeked through its final state; a page without a shared motion clock exports as a still video with a note. A separate explainer is optional via composition_path. Use a unique submission_id to start; retrying returns the original snapshot. Poll with export_id. Builder only; existing authoring and runs:execute consent. Returns the saved video path under db/assets for playback/download. Never runs on page load.", true, false, map[string]any{
		"workflow_id":      externalString("Workflow or Flow ID. Pass exactly one of workflow_id, crew_id or workspace."),
		"crew_id":          externalString("Crew ID."),
		"workspace":        externalString("Project root, for an owned Code project."),
		"export_id":        externalString("Export ID from an earlier call; pass to check its progress/result without starting another export."),
		"submission_id":    externalString("Stable unique request name for a new export (max 128 characters); reuse only to retry the same composition."),
		"composition_path": externalString("Folder under code/motion/ with a standalone HyperFrames index.html, local assets and snapshot.json containing as_of, source and data."),
		"source_mode":      map[string]any{"type": "string", "enum": []any{"dashboard", "composition"}, "description": "dashboard is the default; composition explicitly exports a separately authored video. Passing composition_path also selects the separate video for compatibility."},
		"dashboard_id":     externalString("Published dashboard slug to export. May be omitted when the project has one published dashboard."),
		"revision":         externalString("Exact published revision; defaults to the current publication. Retries retain the original snapshot."),
		"theme":            map[string]any{"type": "string", "enum": []any{"dark", "light"}, "description": "Theme to capture; pass the person's selected dashboard theme. Defaults to dark."},
		"viewport_width":   externalInteger(320, 3840),
		"viewport_height":  externalInteger(180, 3840),
		"duration_s":       map[string]any{"type": "number", "minimum": 0, "maximum": 600, "description": "Playback seconds. Omit to use the dashboard clock's duration, or one second for a still page."},
		"fps":              externalInteger(1, 60),
	})
	shareProps := map[string]any{
		"workflow_id":  externalString("Goal/workflow ID from workflow action=list. Also accepts an existing Flow ID for compatibility. Pass exactly one project selector."),
		"flow_id":      externalString("Flow ID from workflow action=list or relay. Pass exactly one of workflow_id, flow_id, crew_id or workspace."),
		"crew_id":      externalString("Crew ID from workspace action=list or the dashboard list."),
		"workspace":    externalString("Existing project root or stable work:<id> from workspace list/get."),
		"workspace_id": externalString("Stable work:<id> from workspace list/get. Pass exactly one project selector."),
	}
	add("get_dashboard_share", "Read who a Crew, Goal or Flow is shared with: everyone, chosen people (emails) or private. Anyone with access to the product may inspect sharing. "+runShareAccessDescription, false, false, shareProps)
	shareSet := map[string]any{}
	for key, value := range shareProps {
		shareSet[key] = value
	}
	shareSet["create_dashboard"] = map[string]any{"type": "boolean", "description": "Crew only: create and publish the standard dashboard if none exists. Set true only after the person agrees to creating it for sharing; otherwise a missing dashboard returns dashboard_required without changing access."}
	shareSet["mode"] = map[string]any{"type": "string", "enum": []any{"everyone", "people", "private"}, "description": "everyone: every account allowed to use this product. people: only the emails you list; an email with no account is reported back. private: only the owner. Shared people open the dashboard, Ask, use published functions and read product files, runs and logs through MCP; secret values and private logs stay excluded."}
	shareSet["shared_agents"] = map[string]any{"type": "array", "maxItems": 100, "items": externalString("Stable chat_id from Share agents."), "description": "Crew only: replaces the selected existing agents for this same audience. Empty clears; omitted preserves. Messages from sharing onward and their configured tools are shared; tab controls remain with their owner."}
	shareSet["agent_purposes"] = map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string", "maxLength": 300}, "description": "Crew only: saved one-line purpose by chat_id. Only the tab owner can change its purpose."}
	shareSet["emails"] = map[string]any{"type": "array", "maxItems": 200, "items": externalString("Email of an account to share with, for mode people."), "description": "Replaces the people list. Ignored for everyone and private."}
	add("share_dashboard", "Share a Crew, Goal or Flow using its owner's existing authoring consent. "+runShareAccessDescription+" A Crew without a published dashboard needs the person to agree to creating one first; inspect needs_dashboard, ask, and set create_dashboard=true only after agreement. Nothing is shared until the owner asks: use their choice of everyone, chosen people or private; never pick the audience for them. Mode people REPLACES the whole list: inspect sharing first and keep everyone the owner did not ask to remove; the reply shows the previous list.", true, false, shareSet, "mode")
	// Replace the old Run-assistant proxy with a native read operation. Keep its call shape.
	add("get_report_link", "Return a live dashboard URL directly, without starting a Run conversation. document_path defaults to db/reports/index.html; supports Goals and Flows. Requires dashboards:read consent.", false, true, map[string]any{"document_path": externalString("HTML document under db/reports/.")})
}
func (api *StreamingAPI) externalDashboardCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) {
	payload := map[string]any{"action": dashboardActions[name]}
	for key, value := range args {
		payload[key] = value
	}
	// Projects are named by ID like every other MCP tool; dashboardTarget
	// re-checks access on the resolved root.
	if name != "get_report_link" {
		workflowID, flowID, crewID, workspace := externalArg(args, "workflow_id"), externalArg(args, "flow_id"), externalArg(args, "crew_id"), externalArg(args, "workspace")
		workspaceID := externalArg(args, "workspace_id")
		named := 0
		for _, value := range []string{workflowID, flowID, crewID, workspace, workspaceID} {
			if value != "" {
				named++
			}
		}
		if named > 1 || (named == 0 && dashboardActions[name] != "list" && dashboardActions[name] != "catalog") {
			if name == "share_dashboard" {
				logDashboardShareRefusal(GetUserFromContext(r.Context()), dashboardTarget{Root: workspace, ID: workflowID + flowID + crewID}, externalArg(args, "mode"), "selector", dashboardFailure(400, "pass exactly one project"), nil)
			}
			externalError(w, 400, "invalid_arguments", "Pass exactly one project ID or workspace.")
			return
		}
		if workflowID == "" {
			workflowID = flowID
		}
		delete(payload, "workflow_id")
		delete(payload, "flow_id")
		delete(payload, "crew_id")
		delete(payload, "workspace_id")
		if workspace == "" {
			workspace = workspaceID
			if workspace != "" {
				payload["workspace"] = workspace
			}
		}
		if strings.HasPrefix(workspace, "work:") {
			binding, _, ok := api.resolveExternalWorkspace(r.Context(), GetUserFromContext(r.Context()), workspace)
			if !ok {
				if name == "share_dashboard" {
					logDashboardShareRefusal(GetUserFromContext(r.Context()), dashboardTarget{Root: workspace}, externalArg(args, "mode"), "target", dashboardFailure(404, "project unavailable"), nil)
				}
				externalError(w, 404, "not_found", "project unavailable")
				return
			}
			payload["workspace"] = binding.Binding.WorkspacePath
		}
		switch {
		case workflowID != "":
			root, ok := dashboardWorkflowRoot(r, workflowID)
			if !ok {
				if name == "share_dashboard" {
					logDashboardShareRefusal(GetUserFromContext(r.Context()), dashboardTarget{Kind: "workflow", ID: workflowID}, externalArg(args, "mode"), "target", dashboardFailure(404, "project unavailable"), nil)
				}
				externalError(w, 404, "not_found", "project unavailable")
				return
			}
			payload["workspace"] = root
		case crewID != "":
			crew, _, _, ok := api.externalCrewResolve(r.Context(), GetUserFromContext(r.Context()), crewID)
			if !ok {
				if name == "share_dashboard" {
					logDashboardShareRefusal(GetUserFromContext(r.Context()), dashboardTarget{Kind: "crew", ID: crewID}, externalArg(args, "mode"), "target", dashboardFailure(404, "project unavailable"), nil)
				}
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

// dashboardWorkflowRoot is the workspace of a workflow or Flow ID.
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
func (api *StreamingAPI) listAccessibleDashboards(w http.ResponseWriter, r *http.Request, limit, offset int, sharedOnly, publishedOnly bool) {
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

	rows := []map[string]any{}
	resources := []dashboardQuestionDiscovery{}
	seen := map[string]bool{}
	failed := []string{}
	for _, root := range roots {
		target, err := api.dashboardTarget(r.Context(), claims, root)
		if err != nil || seen[target.Root] {
			continue
		}
		seen[target.Root] = true
		result, err := dashboardWorkspaceRequest(r.Context(), dashboards.Request{Root: target.Root, Action: "list", Guard: dashboardFileGuard(claims), IncludeDraft: dashboardAuthoringAllowed(claims, &target) && !publishedOnly})
		if err != nil {
			failed = append(failed, target.Root)
			continue
		}
		// "Shared with me": someone else's project that its owner shared with this caller.
		sharedWithMe := !api.dashboardShareOwner(r.Context(), claims, target)
		if sharedOnly && !sharedWithMe {
			continue
		}
		for _, d := range result.Dashboards {
			if publishedOnly && d.Managed && d.Published == "" {
				continue
			}
			viewTarget := target
			viewTarget.Edit = dashboardAuthoringAllowed(claims, &target)
			row := dashboardDiscoveryRow(viewTarget, d)
			row["shared_with_me"] = sharedWithMe
			resources = append(resources, dashboardQuestionDiscovery{viewTarget, d, row})
			rows = append(rows, row)
		}
	}
	addDashboardQuestionResources(r.Context(), resources)
	sort.Slice(rows, func(i, j int) bool {
		return strings.Compare(rows[i]["workspace"].(string)+"/"+rows[i]["dashboard_id"].(string), rows[j]["workspace"].(string)+"/"+rows[j]["dashboard_id"].(string)) < 0
	})
	start, end := dashboardPageBounds(rows, limit, offset)
	externalJSON(w, map[string]any{"dashboards": rows[start:end], "total": len(rows), "next_offset": end, "has_more": end < len(rows), "unavailable_projects": failed, "authentication": "Sign in with current project access; URLs grant no access.", "note": "Private Code dashboards remain owner-only. For HTML pages use document_path with validation/preview/link."})
}

func dashboardDiscoveryRow(target dashboardTarget, d dashboards.Dashboard) map[string]any {
	row := map[string]any{"dashboard_id": d.ID, "title": d.Title, "motion": d.Motion, "artifact": d.Artifact, "workspace": target.Root, "project_id": target.ID, "project_title": target.Title, "product": target.Kind, "document_path": d.DocumentPath, "revision": d.Revision, "published_revision": d.Published, "managed": d.Managed, "can_edit": target.Edit}
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
