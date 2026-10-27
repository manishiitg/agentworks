package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	events "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpcache"
	"github.com/manishiitg/mcpagent/mcpclient"
)

func crewCapabilityHTTP(t *testing.T, f *externalToolsFixture, claims *UserClaims, name string, args map[string]any, status int) string {
	t.Helper()
	args["crew_id"] = "team"
	body, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	w := httptest.NewRecorder()
	// Member tool names are internal since 9a5930513 (PLAT-825 merged tools), so
	// call them the way the server's own callers do (PLAT-856-386).
	f.api.dispatchExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(body), claims, nil), true)
	externalTestBody(t, w, status)
	return w.Body.String()
}
func TestExternalCrewFileLifecycleOverMCP(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	f.write(t, externalCommandCrewRoot+"/code/report.md", "Old text\n")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv := serveExternalMCP(t, f.api, crewWriter("owner"))
	cli := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	call := func(args map[string]any) {
		t.Helper()
		args["crew_id"] = "team"
		result := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "files", "arguments": args})
		requireRemoteSuccess(t, result, "files")
	}
	patch := map[string]any{"action": "patch", "path": "code/report.md", "diff": "--- a/report.md\n+++ b/report.md\n@@ -1 +1 @@\n-Old text\n+New text\n", "expected_revision": wf.Revision([]byte("Old text\n")), "request_id": "patch-report"}
	call(patch)
	call(patch)
	if got := f.read(t, externalCommandCrewRoot+"/code/report.md"); got != "New text\n" {
		t.Fatalf("patch/retry = %q", got)
	}
	call(map[string]any{"action": "mkdir", "path": "archive", "expected_revision": "missing"})
	call(map[string]any{"action": "move", "path": "code/report.md", "destination": "archive/report.md", "expected_revision": wf.Revision([]byte("New text\n"))})
	if got := f.read(t, externalCommandCrewRoot+"/archive/report.md"); got != "New text\n" {
		t.Fatal(got)
	}
	call(map[string]any{"action": "delete", "path": "archive/report.md", "expected_revision": wf.Revision([]byte("New text\n"))})
	crewCapabilityHTTP(t, f, crewWriter("owner"), "files", map[string]any{"action": "delete", "path": "archive", "expected_revision": "bogus"}, 400)
	call(map[string]any{"action": "delete", "path": "archive"})
	f.write(t, externalCommandCrewRoot+"/code/locked/keep.md", "Keep")
	guarded := crewWriter("owner")
	guarded.AccessToken.FileGuard = &wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"code", "archive"}, ReadOnlyPaths: []string{"code/locked"}}
	crewCapabilityHTTP(t, f, guarded, "files", map[string]any{"action": "move", "path": "code", "destination": "archive"}, 403)
	crewCapabilityHTTP(t, f, guarded, "files", map[string]any{"action": "delete", "path": "code/locked/keep.md", "expected_revision": wf.Revision([]byte("Keep"))}, 403)
	crewCapabilityHTTP(t, f, guarded, "files", map[string]any{"action": "write", "path": "code/locked/keep.md", "content": "Denied"}, 403)
	guarded.AccessToken.FileGuard.BlockedPaths = []string{"code/locked"}
	crewCapabilityHTTP(t, f, guarded, "files", map[string]any{"action": "read", "path": "code/locked/keep.md"}, 403)

	crewCapabilityHTTP(t, f, crewWriter("owner"), "files", map[string]any{"action": "delete", "path": "code"}, 409)
	f.write(t, externalCommandCrewRoot+"/code/functions.json", "{}")
	crewCapabilityHTTP(t, f, crewWriter("owner"), "files", map[string]any{"action": "move", "path": "code", "destination": "archive"}, 403)
	for _, denied := range []*UserClaims{crewWriter("other"), crewReader("owner")} {
		crewCapabilityHTTP(t, f, denied, "files", map[string]any{"action": "mkdir", "path": "denied", "expected_revision": "missing"}, 403)
	}
	crewCapabilityHTTP(t, f, crewWriter("owner"), "files", map[string]any{"action": "delete", "path": "../workflow.json"}, 403)
	crewCapabilityHTTP(t, f, crewWriter("owner"), "files", map[string]any{"action": "patch", "path": "code/locked/keep.md", "diff": "bad", "request_id": "bad", "expected_revision": "stale"}, 409)
}
func crewReader(user string) *UserClaims {
	claims := crewWriter(user)
	claims.AccessToken.Scopes = []string{"crews:read"}
	return claims
}

func TestExternalCrewConnectionsAndReferenceBounds(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("PUBLIC_URL", "")
	gateway := newCrewConnectionVaultFixture(t)
	f.api.mcpConfigPath = gateway.catalog
	f.api.logger = loggerv2.NewNoop()
	config, err := mcpclient.LoadConfig(gateway.catalog, f.api.logger)
	if err != nil {
		t.Fatal(err)
	}
	f.api.mcpConfig = config
	result := crewCapabilityHTTP(t, f, crewWriter("owner"), "mcp", map[string]any{"action": "install", "name": "example"}, 200)
	if !strings.Contains(result, "OAuth callback address") {
		t.Fatal("pending account did not explain sign-in setup")
	}
	selected, _, err := projectAppSelection(t.Context(), externalCommandCrewRoot)
	if err != nil || len(selected) != 1 {
		t.Fatalf("project selection: %v %v", selected, err)
	}
	connection := selected[0]
	// Discovery is limited to the Vault apps this Crew uses (not the retired place attachments).
	crewCapabilityHTTP(t, f, crewWriter("owner"), "mcp", map[string]any{"action": "discover", "name": "vault_c-0123456789ab"}, 403)
	crewCapabilityHTTP(t, f, crewWriter("owner"), "mcp", map[string]any{"action": "discover", "name": connection}, 200)
	// The saved Vault account can be selected by more than one project.
	otherRoot := "Crew/another"
	f.write(t, otherRoot+"/workflow.json", `{"id":"another","created_by":"owner","capabilities":{"selected_servers":["`+connection+`"]}}`)
	crewCapabilityHTTP(t, f, crewWriter("owner"), "mcp", map[string]any{"action": "install", "name": connection}, 200)
	crewCapabilityHTTP(t, f, crewReader("other"), "mcp", map[string]any{"action": "list"}, 200)
	crewCapabilityHTTP(t, f, crewWriter("other"), "mcp", map[string]any{"action": "remove", "name": connection}, 403)
	crewCapabilityHTTP(t, f, crewWriter("owner"), "mcp", map[string]any{"action": "install", "name": "Example", "client_secret": "denied"}, 400)
	crewCapabilityHTTP(t, f, crewWriter("owner"), "mcp", map[string]any{"action": "remove", "name": connection}, 200)
	current, _, _ := projectAppSelection(t.Context(), externalCommandCrewRoot)
	other, _, _ := projectAppSelection(t.Context(), otherRoot)
	if slices.Contains(current, connection) || !slices.Contains(other, connection) || gateway.count() != 1 {
		t.Fatal("detach changed another product or deleted the saved account")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv := serveExternalMCP(t, f.api, crewWriter("owner"))
	cli := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	installed := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "mcp", "arguments": map[string]any{"crew_id": "team", "action": "install", "name": "custom", "url": "https://custom.example.com/mcp", "api_key": "synthetic-key"}})
	requireRemoteSuccess(t, installed, "mcp install")
	if gateway.count() != 2 {
		t.Fatal("custom connection was not saved in Vault")
	}
	for _, denied := range []*UserClaims{crewReader("owner"), crewWriter("other")} {
		crewCapabilityHTTP(t, f, denied, "mcp", map[string]any{"action": "install", "name": connection}, 403)
	}
	bounded := crewWriter("owner")
	bounded.AccessToken.AllCrews, bounded.AccessToken.CrewIDs = false, []string{"different"}
	crewCapabilityHTTP(t, f, bounded, "mcp", map[string]any{"action": "install", "name": connection}, 403)
	// Actual native chat/function/message registrations all select the same
	// account store. No retired personal-connection files are involved.
	reg := &recordingRegistrar{}
	f.api.eventStore = events.NewEventStore(10)
	f.api.sessionWorkspaceFolders = map[string]string{}
	if err := f.api.registerMultiAgentMCPServerTools(reg, nil); err != nil {
		t.Fatal(err)
	}
	appCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	for _, run := range []string{"chat", "function", "messages"} {
		session := "connection-test-" + run
		f.api.eventStore.SetSessionOwner(session, "owner")
		f.api.sessionWorkspaceFolders[session] = externalCommandCrewRoot
		if run != "chat" {
			f.api.sessionWorkspaceFolders[session] += "/runs/" + run
		}
		runCtx := executor.WithSessionID(appCtx, session)
		for _, operation := range []string{"install_mcp_server", "add_mcp_server"} {
			if _, err := reg.tools[operation].exec(runCtx, map[string]any{"name": connection}); err != nil {
				t.Fatalf("%s %s: %v", run, operation, err)
			}
		}
	}
	f.write(t, "Workflow/research/workflow.json", `{"id":"research","label":"Research","access":{"owners":["owner"]}}`)
	claims := crewWriter("owner")
	claims.AccessToken.Scopes = append(claims.AccessToken.Scopes, "workflows:read", "files:read")
	claims.AccessToken.AllWorkflows = true
	result = crewCapabilityHTTP(t, f, claims, "crew_references", map[string]any{"action": "list"}, 200)
	if !strings.Contains(result, "Workflow/research") {
		t.Fatal(result)
	}
	crewCapabilityHTTP(t, f, claims, "crew_references", map[string]any{"action": "attach", "workspace_path": "Workflow/research"}, 200)
	if manifest, _, _ := readFileFromWorkspace(t.Context(), externalCommandCrewRoot+"/workflow.json"); !strings.Contains(manifest, "Workflow/research") {
		t.Fatal("reference was not persisted")
	}
	crewCapabilityHTTP(t, f, claims, "crew_references", map[string]any{"action": "detach", "workspace_path": "Workflow/research"}, 200)
	claims.AccessToken.AllWorkflows = false
	claims.AccessToken.WorkflowIDs = []string{"other"}
	crewCapabilityHTTP(t, f, claims, "crew_references", map[string]any{"action": "attach", "workspace_path": "Workflow/research"}, 403)
	result = crewCapabilityHTTP(t, f, claims, "crew_references", map[string]any{"action": "list"}, 200)
	if strings.Contains(result, "Workflow/research") {
		t.Fatal("out-of-bound reference was listed")
	}
	for _, group := range []string{"skills", "mcp", "crew_references"} {
		action := map[string]string{"skills": "uninstall", "mcp": "remove", "crew_references": "attach"}[group]
		crewCapabilityHTTP(t, f, crewReader("owner"), group, map[string]any{"action": action}, 403)
		crewCapabilityHTTP(t, f, crewWriter("other"), group, map[string]any{"action": action}, 403)
	}
}
func TestExternalCrewAccountFoldersRequireBroadConsent(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	root := t.TempDir()
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	document, _ := json.Marshal(map[string]any{"assignments": map[string]any{"owner": map[string]any{"roots": []string{canonical}}}, "grants": map[string]any{}})
	f.write(t, workFolderAccessFilePath(), string(document))
	args := func() map[string]any {
		return map[string]any{"action": "folder_attach", "path": root, "alias": "references", "access": "read_only"}
	}
	for _, denied := range []*UserClaims{crewReader("owner"), crewWriter("other")} {
		crewCapabilityHTTP(t, f, denied, "files", args(), 403)
	}
	bounded := crewWriter("owner")
	bounded.AccessToken.AllCrews = false
	bounded.AccessToken.CrewIDs = []string{"team"}
	crewCapabilityHTTP(t, f, bounded, "files", args(), 403)
	guarded := crewWriter("owner")
	guarded.AccessToken.FileGuard = &wf.FolderGuard{WritePaths: []string{"."}}
	crewCapabilityHTTP(t, f, guarded, "files", args(), 403)
	crewCapabilityHTTP(t, f, crewWriter("owner"), "files", args(), 200)
	result := crewCapabilityHTTP(t, f, crewWriter("owner"), "files", map[string]any{"action": "folders"}, 200)
	if !strings.Contains(result, "references") {
		t.Fatal(result)
	}
	grants := workFolderGrantsForClaims(context.Background(), crewWriter("owner"))
	if len(grants) != 1 {
		t.Fatalf("grants=%v", grants)
	}
	crewCapabilityHTTP(t, f, crewWriter("owner"), "files", map[string]any{"action": "folder_detach", "id": grants[0].ID}, 200)
	if grants = workFolderGrantsForClaims(context.Background(), crewWriter("owner")); len(grants) != 0 {
		t.Fatalf("detach left grants=%v", grants)
	}
	outside := args()
	outside["path"] = t.TempDir()
	crewCapabilityHTTP(t, f, crewWriter("owner"), "files", outside, 400)
}

// Real MCP calls read the disk-backed Crew while unrelated browsers, private
// accounts, endpoint config and raw cache errors stay outside the response.
func TestExternalCrewDiagnosticsOverMCP(t *testing.T) {
	f, workspaceURL := newExternalCrewCommandFixture(t)
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	f.api.logger = loggerv2.NewNoop()
	// Diagnostics now reads central Vault, never the retired personal store
	// or its tool cache. Only the selected account's metadata is shared.
	f.write(t, externalCommandCrewRoot+"/workflow.json", `{"id":"team","created_by":"owner","capabilities":{"selected_servers":["vault_attached"]}}`)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/admin/runtime/servers" || r.Header.Get("X-CapLayer-Actor") != "owner" {
			w.WriteHeader(403)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"servers": []any{
			map[string]any{"id": "attached", "label": "Selected connection", "provider": "example", "upstream_url": "https://example.com/hidden-endpoint", "tools": []any{
				map[string]any{"name": "lookup", "description": "Find a record", "input_schema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []string{"query"}}},
				map[string]any{"name": "count", "description": "Count records", "input_schema": map[string]any{"type": "object"}},
			}},
			map[string]any{"id": "unattached", "label": "private-diagnostic-marker", "tools": []any{}},
		}})
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	selectedBrowser := browserSessionForWorkspace("owner", externalCommandCrewRoot)
	unrelatedBrowser := browserSessionForWorkspace("owner", "Crew/another-crew")
	browser.GetSessionTracker().Touch(selectedBrowser, "crew-chat", "crew-chat")
	browser.GetSessionTracker().Touch(unrelatedBrowser, "unrelated-chat", "unrelated-chat")
	browser.GetSessionTracker().RecordAction(selectedBrowser, "open", []string{"https://example.com/private-command-marker"})
	browser.GetSessionTracker().Touch("owner-private-browser", "owner-run", "owner-run")
	browser.GetSessionTracker().Touch("reader-private-browser", "reader-run", "reader-run")
	f.api.activeSessions = map[string]*ActiveSessionInfo{
		"owner-run":  {UserID: "owner", WorkspacePath: externalCommandCrewRoot},
		"reader-run": {UserID: "other", WorkspacePath: externalCommandCrewRoot},
	}
	f.api.playwrightLive.sessions = map[string]*playwrightLiveSession{
		"shared-test":    {id: "pw-shared-test", owner: "owner", workspace: playwrightScope("owner", externalCommandCrewRoot), run: "private-playwright-run", label: "Crew test"},
		"unrelated-test": {id: "pw-unrelated-test", owner: "owner", workspace: playwrightScope("owner", "Crew/another-crew"), run: "unrelated-run", label: "Unrelated test"},
	}
	t.Cleanup(func() {
		browser.GetSessionTracker().Remove(selectedBrowser)
		browser.GetSessionTracker().Remove(unrelatedBrowser)
		browser.GetSessionTracker().Remove("owner-private-browser")
		browser.GetSessionTracker().Remove("reader-private-browser")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	reader := crewReader("other")
	srv := serveExternalMCP(t, f.api, reader)
	cli := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	call := func(args map[string]any) map[string]any {
		t.Helper()
		args["crew_id"] = "team"
		result := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "crew_diagnostics", "arguments": args})
		requireRemoteSuccess(t, result, "crew_diagnostics")
		raw := marshalStructured(t, result)
		for _, hidden := range []string{"unattached", "hidden-endpoint", "private-diagnostic-marker", unrelatedBrowser, "crew-chat", "private-playwright-run", "private-command-marker", "owner-private-browser", "pw-unrelated-test", "host-probe-error-marker", "authorized_endpoints", "configured_cdp_ports", "reachable_cdp_ports", "check_errors", "recording_error", "agent_session", "workflow_session", `"port"`} {
			if strings.Contains(raw, hidden) {
				t.Fatalf("diagnostics leaked %q", hidden)
			}
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	status := call(map[string]any{"action": "browser"})
	if status["capabilities"].(map[string]any)["effective_mode"] != "headless" || status["extension"].(map[string]any)["available_to_caller"] != false || status["sessions"].(map[string]any)["total"] != float64(3) {
		t.Fatalf("Crew browser status = %v", status)
	}
	for _, item := range status["sessions"].(map[string]any)["items"].([]any) {
		row := item.(map[string]any)
		wantScope := "shared_crew"
		if row["browser_session"] == "reader-private-browser" {
			wantScope = "caller"
		}
		if row["scope"] != wantScope || row["read_only"] != "true" {
			t.Fatalf("browser scope = %v", row)
		}
	}
	// Real capability probes can carry a workspace host/error or CDP endpoint.
	// Exercise both failure and success; neither may appear in diagnostics.
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	raw, found, readErr := readFileFromWorkspace(context.Background(), externalCommandCrewRoot+"/workflow.json")
	if readErr != nil || !found {
		t.Fatal("Crew manifest missing")
	}
	var browserManifest map[string]any
	if err := json.Unmarshal([]byte(raw), &browserManifest); err != nil {
		t.Fatal(err)
	}
	browserManifest["capabilities"].(map[string]any)["browser_mode"] = "cdp"
	encodedBrowserManifest, _ := json.Marshal(browserManifest)
	f.write(t, externalCommandCrewRoot+"/workflow.json", string(encodedBrowserManifest))
	upstream, err := url.Parse(workspaceURL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	connected := false
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/cdp-check" {
			_ = json.NewEncoder(w).Encode(map[string]any{"connected": connected, "error": "host-probe-error-marker"})
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(probe.Close)
	t.Setenv("WORKSPACE_API_URL", probe.URL)
	call(map[string]any{"action": "browser"})
	connected = true
	if got := call(map[string]any{"action": "browser"})["capabilities"].(map[string]any)["effective_mode"]; got != "cdp" {
		t.Fatalf("reachable CDP summary = %v", got)
	}
	connections := call(map[string]any{"action": "connections"})
	if connections["connections"].(map[string]any)["total"] != float64(1) {
		t.Fatalf("attached connections = %v", connections)
	}
	tools := call(map[string]any{"action": "connections", "name": "vault_attached", "limit": 1})["connection"].(map[string]any)
	page := tools["tools"].(map[string]any)
	if tools["status"] != "connected" || page["total"] != float64(2) || page["has_more"] != true || len(page["items"].([]any)) != 1 {
		t.Fatalf("tool metadata page = %v", tools)
	}
	if _, exposed := page["items"].([]any)[0].(map[string]any)["parameters"]; exposed {
		t.Fatal("reader saw full schemas")
	}
	crewCapabilityHTTP(t, f, reader, "crew_diagnostics", map[string]any{"action": "connections", "name": "vault_attached", "include_tools": true}, 403)
	ownerSrv := serveExternalMCP(t, f.api, crewReader("owner"))
	ownerClient := dialExternalMCP(t, ctx, ownerSrv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, ownerClient)
	ownerTools := callRemoteTool(t, ctx, ownerClient, "call_tool", map[string]any{"name": "crew_diagnostics", "arguments": map[string]any{"crew_id": "team", "action": "connections", "name": "vault_attached", "include_tools": true}})
	requireRemoteSuccess(t, ownerTools, "crew_diagnostics")
	if raw := marshalStructured(t, ownerTools); !strings.Contains(raw, `"parameters"`) || !strings.Contains(raw, `"required":["query"]`) {
		t.Fatalf("owner full schema = %s", raw)
	}
	crewCapabilityHTTP(t, f, reader, "crew_diagnostics", map[string]any{"action": "connections", "name": "vault_unattached"}, 403)
	bounded := crewReader("other")
	bounded.AccessToken.AllCrews, bounded.AccessToken.CrewIDs = false, []string{"different"}
	crewCapabilityHTTP(t, f, bounded, "crew_diagnostics", map[string]any{"action": "browser"}, 403)
}

// Discovery connects over the real MCP transport even if an older cache exists.
// Its refreshed metadata must be visible under the runtime/diagnostics key.
func TestCrewMCPDiscoveryRefreshesCache(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	f.api.logger = loggerv2.NewNoop()
	srv := serveExternalMCP(t, f.api, crewWriter("owner"))
	cfg := mcpclient.MCPServerConfig{URL: srv.URL + externalMCPPath, Protocol: mcpclient.ProtocolHTTP}
	name := "crew-discovery-refresh"
	cache := mcpcache.GetCacheManager(f.api.logger)
	key := mcpcache.GenerateUnifiedCacheKey(name, cfg)
	t.Cleanup(func() { _ = cache.Invalidate(key) })
	if err := cache.Put(&mcpcache.CacheEntry{ServerName: name, CreatedAt: time.Now(), IsValid: true, TTLMinutes: 30}, cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	status, err := f.api.discoverResolvedServerTools(ctx, &executor.ResolvedMCPServer{Name: name, Config: cfg})
	if err != nil || status == nil || status.ToolsEnabled == 0 {
		t.Fatalf("fresh discovery = %+v (%v)", status, err)
	}
	entry, found := cache.Get(key)
	if !found || len(entry.Tools) != status.ToolsEnabled || entry.IsExpired() || !entry.IsValid {
		t.Fatalf("discovery did not publish fresh cache: %+v (%v)", entry, found)
	}
}
