package server

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const dashboardAppURI = "ui://agentworks/dashboard/v1.html"
const dashboardAppMIME = "text/html;profile=mcp-app"
const dashboardAppFramePath = "/api/mcp-apps/frame"

//go:embed mcp_apps/frame.html
var dashboardAppFrameHTML string

func externalDashboardAppDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("open_dashboard", "Open a published Crew dashboard inline when the chat host supports MCP Apps, or return its website link. Discover resource_id with dashboard action=list. Opening starts no work.", false, false, map[string]any{"resource_id": map[string]any{"type": "string", "format": "uuid", "description": "Published dashboard resource_id from dashboard action=list."}}, "resource_id")
}

// The resource contains only application code. Project data is delivered by
// open_dashboard, and each subsequent read resolves its current sharing again.
func (api *StreamingAPI) registerExternalDashboardApp(s *server.MCPServer, r *http.Request) {
	if !externalTokenAllows(GetUserFromContext(r.Context()), externalTool{Name: "open_dashboard"}) {
		return
	}
	s.AddResource(mcp.Resource{URI: dashboardAppURI, Name: "AgentWorks dashboard", MIMEType: dashboardAppMIME}, func(ctx context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		if !dashboardScopeAllowed(GetUserFromContext(ctx), "get") {
			return nil, fmt.Errorf("dashboard access is unavailable; open the shared link in AgentWorks")
		}
		page, err := dashboardAppResource()
		if err != nil {
			return nil, err
		}
		base, err := url.Parse(effectiveShareBaseURL())
		if err != nil || base.Host == "" {
			return nil, fmt.Errorf("open this dashboard through the AgentWorks website")
		}
		origin := base.Scheme + "://" + base.Host
		return []mcp.ResourceContents{mcp.TextResourceContents{URI: dashboardAppURI, MIMEType: dashboardAppMIME, Text: page, Meta: map[string]any{"ui": map[string]any{"csp": map[string]any{"connectDomains": []string{}, "resourceDomains": []string{}, "frameDomains": []string{origin}}}}}}, nil
	})
	open := mcp.NewToolWithRawSchema("open_dashboard", "Open a shared Crew's published dashboard inline, with Ask and published actions. Discover its resource_id with dashboard action=list through call_tool. Opening does not start work. Also returns the website link for clients without inline apps.", json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","format":"uuid"}},"required":["resource_id"],"additionalProperties":false}`))
	open.Meta = &mcp.Meta{AdditionalFields: map[string]any{"ui": map[string]any{"resourceUri": dashboardAppURI}}}
	readOnly := true
	open.Annotations.ReadOnlyHint = &readOnly
	s.AddTool(open, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return api.openDashboardApp(r.WithContext(ctx), req.GetArguments()), nil
	})
	read := mcp.NewToolWithRawSchema("read_dashboard_data", "Read this published dashboard's data through its existing project access. Never runs a script, sends a message or changes a database.", json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","format":"uuid"},"revision":{"type":"string"},"action":{"type":"string","enum":["query","file"]},"sql":{"type":"string","maxLength":65536},"params":{"type":"array"},"path":{"type":"string","maxLength":1024}},"required":["resource_id","action"],"additionalProperties":false}`))
	read.Meta = &mcp.Meta{AdditionalFields: map[string]any{"ui": map[string]any{"visibility": []string{"app"}}}}
	read.Annotations.ReadOnlyHint = &readOnly
	s.AddTool(read, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return api.readDashboardAppData(r.WithContext(ctx), req.GetArguments()), nil
	})
}

func (api *StreamingAPI) dashboardAppTarget(r *http.Request, args map[string]any) (resolvedDashboardQuestion, error) {
	if !externalTokenAllows(GetUserFromContext(r.Context()), externalTool{Name: "open_dashboard"}) {
		return resolvedDashboardQuestion{}, dashboardFailure(404, "Dashboard access is unavailable. Open the shared website link.")
	}
	id := externalArg(args, "resource_id")
	if _, err := uuid.Parse(id); err != nil {
		return resolvedDashboardQuestion{}, dashboardFailure(400, "Choose a dashboard from the dashboard list.")
	}
	resolved, err := api.resolveDashboardQuestion(r.Context(), GetUserFromContext(r.Context()), id, externalArg(args, "revision"))
	if err != nil {
		return resolved, err
	}
	if !resolved.Capability.CanView || resolved.Target.Kind != "crew" {
		return resolvedDashboardQuestion{}, dashboardFailure(404, "Open an accessible Crew dashboard; other products use their website link.")
	}
	return resolved, nil
}

func (api *StreamingAPI) openDashboardApp(r *http.Request, args map[string]any) *mcp.CallToolResult {
	resolved, err := api.dashboardAppTarget(r, args)
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	claims := GetUserFromContext(r.Context())
	prefix := path.Dir(dashboards.Document(resolved.Document.ID, resolved.Document.Published))
	files := map[string]string{}
	for name, content := range resolved.Snapshot.Files {
		if strings.HasPrefix(name, "scripts/") {
			continue
		}
		if guard := dashboardFileGuard(claims); guard != nil && !guard.Allows(prefix+"/"+name, false) {
			continue
		}
		files[name] = strings.ReplaceAll(strings.ReplaceAll(content, "{{dashboard_assets}}", prefix), "{{dashboard_scripts}}", "code/reports/managed/"+resolved.Document.ID+"/"+resolved.Document.Published)
	}
	page, _, err := api.liveDefaultDashboard(r.Context(), claims, resolved.Target.Root, dashboards.Document(resolved.Document.ID, ""), []byte(files["index.html"]))
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	link := strings.TrimRight(effectiveShareBaseURL(), "/") + "/d/" + resolved.Resource.Code
	if resolved.Resource.Code == "" {
		link = strings.TrimRight(effectiveShareBaseURL(), "/") + "/dashboards"
	}
	data := map[string]any{"resource_id": resolved.Resource.ID, "workspace_id": resolved.Target.ID, "project_id": resolved.Target.ID, "revision": resolved.Document.Published, "title": resolved.Document.Title, "url": link, "html": string(page), "files": files, "asset_prefix": prefix, "capability": resolved.Capability}
	result := mcp.NewToolResultText("Opened " + resolved.Document.Title + ". Use Ask or a published action to start work. Website: " + link)
	result.StructuredContent = map[string]any{"resource_id": resolved.Resource.ID, "title": resolved.Document.Title, "url": link, "revision": resolved.Document.Published}
	result.Meta = &mcp.Meta{AdditionalFields: map[string]any{"dashboard": data}}
	log.Printf("[MCP_APPS] operation=open resource_id=%q outcome=ok", resolved.Resource.ID)
	return result
}

func (api *StreamingAPI) readDashboardAppData(r *http.Request, args map[string]any) *mcp.CallToolResult {
	resolved, err := api.dashboardAppTarget(r, args)
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	var request *http.Request
	rec := httptest.NewRecorder()
	switch externalArg(args, "action") {
	case "query":
		body, err := json.Marshal(map[string]any{"workspace": resolved.Target.Root, "sql": args["sql"], "params": args["params"]})
		if err != nil {
			return mcp.NewToolResultError("Invalid dashboard query.")
		}
		request = httptest.NewRequest(http.MethodPost, reportPreviewAPIPrefix+"query", bytes.NewReader(body)).WithContext(r.Context())
		api.handleReportPreviewQuery(rec, request)
	case "file":
		p := externalArg(args, "path")
		// Bundle-relative files refer to this immutable publication, never a draft.
		if p != "" && p != "MEMORY.md" && p != "soul.md" && p != "workflow.json" && !strings.HasPrefix(p, "db/") && !strings.HasPrefix(p, "knowledgebase/") && !strings.HasPrefix(p, "docs/") {
			p = path.Dir(dashboards.Document(resolved.Document.ID, resolved.Document.Published)) + "/" + p
		}
		query := url.Values{"workspace": {resolved.Target.Root}, "path": {p}}
		request = httptest.NewRequest(http.MethodGet, reportPreviewAPIPrefix+"file?"+query.Encode(), nil).WithContext(r.Context())
		api.handleReportPreviewFile(rec, request)
		if rec.Code == 200 {
			result, _ := mcp.NewToolResultJSON(map[string]any{"content_base64": base64.StdEncoding.EncodeToString(rec.Body.Bytes()), "mime_type": rec.Header().Get("Content-Type")})
			return result
		}
	default:
		return mcp.NewToolResultError("Choose query or file. Open AgentWorks for scripts or database changes.")
	}
	if rec.Code != 200 {
		return mcp.NewToolResultError(strings.TrimSpace(rec.Body.String()))
	}
	var value map[string]any
	if json.Unmarshal(rec.Body.Bytes(), &value) != nil {
		return mcp.NewToolResultError("The dashboard data could not be read. Open its website link.")
	}
	result, _ := mcp.NewToolResultJSON(value)
	return result
}

func dashboardAppResource() (string, error) {
	root := staticFrontendDir()
	main, err := os.ReadFile(filepath.Join(root, "mcp-dashboard.js"))
	if err != nil {
		return "", fmt.Errorf("inline dashboard runtime is unavailable; open its AgentWorks website link")
	}
	child, err := os.ReadFile(filepath.Join(root, "mcp-dashboard-document.js"))
	if err != nil {
		return "", fmt.Errorf("inline dashboard document runtime is unavailable; open its AgentWorks website link")
	}
	assets := map[string]string{"document": string(child), "frame_url": strings.TrimRight(effectiveShareBaseURL(), "/") + dashboardAppFramePath}
	// The independent renderer package is bundled when installed. Existing HTML
	// does not need the React runtime; no remote credential fetch is introduced.
	for _, name := range []string{"dashboard-react.js", "dashboard.css"} {
		if data, err := os.ReadFile(filepath.Join(root, name)); err == nil {
			assets[name] = string(data)
		}
	}
	encoded, _ := json.Marshal(assets)
	return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"></head><body><main id="app">Opening dashboard…</main><script id="aw-runtimes" type="application/json">` + string(encoded) + `</script><script>` + strings.ReplaceAll(string(main), "</script", `<\/script`) + `</script></body></html>`, nil
}

// Public, credential-free bootstrap for the opaque dashboard document. It
// contains no project data and reads no cookies, tokens or workspace files.
func (api *StreamingAPI) dashboardAppFrame(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline' blob:; style-src 'unsafe-inline' blob:; img-src data: blob:; font-src data: blob:; connect-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'")
	_, _ = w.Write([]byte(dashboardAppFrameHTML))
}
