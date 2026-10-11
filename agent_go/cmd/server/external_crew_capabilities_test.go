package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
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
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func crewCapabilityHTTP(t *testing.T, f *externalToolsFixture, claims *UserClaims, name string, args map[string]any, status int) string {
	t.Helper()
	args["crew_id"] = "team"
	body, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	w := httptest.NewRecorder()
	f.api.handleExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(body), claims, nil))
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
		result := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "crew_files", "arguments": args})
		requireRemoteSuccess(t, result, "crew_files")
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
	call(map[string]any{"action": "delete", "path": "archive"})
	f.write(t, externalCommandCrewRoot+"/code/locked/keep.md", "Keep")
	guarded := crewWriter("owner")
	guarded.AccessToken.FileGuard = &wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"code", "archive"}, ReadOnlyPaths: []string{"code/locked"}}
	crewCapabilityHTTP(t, f, guarded, "crew_files", map[string]any{"action": "move", "path": "code", "destination": "archive"}, 403)
	crewCapabilityHTTP(t, f, guarded, "crew_files", map[string]any{"action": "delete", "path": "code/locked/keep.md", "expected_revision": wf.Revision([]byte("Keep"))}, 403)
	crewCapabilityHTTP(t, f, guarded, "files", map[string]any{"action": "write", "path": "code/locked/keep.md", "content": "Denied"}, 403)
	guarded.AccessToken.FileGuard.BlockedPaths = []string{"code/locked"}
	crewCapabilityHTTP(t, f, guarded, "files", map[string]any{"action": "read", "path": "code/locked/keep.md"}, 403)

	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_files", map[string]any{"action": "delete", "path": "code"}, 409)
	f.write(t, externalCommandCrewRoot+"/code/functions.json", "{}")
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_files", map[string]any{"action": "move", "path": "code", "destination": "archive"}, 403)
	for _, denied := range []*UserClaims{crewWriter("other"), crewReader("owner")} {
		crewCapabilityHTTP(t, f, denied, "crew_files", map[string]any{"action": "mkdir", "path": "denied", "expected_revision": "missing"}, 403)
	}
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_files", map[string]any{"action": "delete", "path": "../workflow.json"}, 403)
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_files", map[string]any{"action": "patch", "path": "code/locked/keep.md", "diff": "bad", "request_id": "bad", "expected_revision": "stale"}, 409)
}
func crewReader(user string) *UserClaims {
	claims := crewWriter(user)
	claims.AccessToken.Scopes = []string{"crews:read"}
	return claims
}

func TestExternalCrewConnectionsAndReferenceBounds(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("PUBLIC_URL", "")
	catalogPath := filepath.Join(t.TempDir(), "mcp.json")
	catalog := `{"mcpServers":{"Example":{"url":"https://mcp.example.com/mcp","protocol":"http","oauth":{"auth_url":"https://auth.example.com/authorize","token_url":"https://auth.example.com/token"}}}}`
	if err := os.WriteFile(catalogPath, []byte(catalog), 0600); err != nil {
		t.Fatal(err)
	}
	f.api.mcpConfigPath = catalogPath
	f.api.logger = loggerv2.NewNoop()
	config, err := mcpclient.LoadConfig(catalogPath, f.api.logger)
	if err != nil {
		t.Fatal(err)
	}
	f.api.mcpConfig = config
	result := crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_mcp", map[string]any{"action": "install", "name": "Example"}, 200)
	if !strings.Contains(result, "Integrations") {
		t.Fatal(result)
	}
	attachments, _ := placeMCPAttachmentsFor(externalCommandCrewRoot)
	if len(attachments) != 1 {
		t.Fatalf("attachments=%v", attachments)
	}
	for _, args := range []map[string]any{
		{"action": "install", "name": "untrusted", "url": "https://untrusted.example.com/mcp"},
		{"action": "install", "name": "Example", "catalog": "Example", "url": "https://untrusted.example.com/mcp"},
	} {
		if refusal := crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_mcp", args, 400); !strings.Contains(refusal, "Integrations") {
			t.Fatal(refusal)
		}
	}
	// Reusing a named catalog account is valid; guessing a catalog name does
	// not approve a different endpoint in the personal store.
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_mcp", map[string]any{"action": "install", "name": attachments[0].Server}, 200)
	// The same personal login is used elsewhere; removing the Crew attachment
	// must leave that other attachment and the login untouched.
	otherRoot := "_users/owner/Chats/Work/projects/another"
	if err = attachPersonalLoginToPlace("owner", attachments[0].Server, otherRoot); err != nil {
		t.Fatal(err)
	}
	crewCapabilityHTTP(t, f, crewReader("other"), "crew_mcp", map[string]any{"action": "list"}, 200)
	crewCapabilityHTTP(t, f, crewWriter("other"), "crew_mcp", map[string]any{"action": "remove", "name": attachments[0].Server}, 403)
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_mcp", map[string]any{"action": "install", "name": "Example", "client_secret": "denied"}, 400)
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_mcp", map[string]any{"action": "remove", "name": attachments[0].Server}, 200)
	current, _ := placeMCPAttachmentsFor(externalCommandCrewRoot)
	other, _ := placeMCPAttachmentsFor(otherRoot)
	personal, _ := listPlaceMCPServers("owner")
	if len(current) != 0 || len(other) != 1 || len(personal) != 1 {
		t.Fatalf("detach changed other state: %v %v %v", current, other, personal)
	}
	// Integrations still adds custom URLs through its existing shared path.
	appCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	customHeaders := map[string]placeMCPHeader{"X-Api-Key": {Secret: "EXAMPLE_KEY"}}
	custom, _, err := f.api.addPlaceMCP(appCtx, "owner", placeMCPServer{Name: "custom", URL: "https://custom.example.com/mcp", Headers: customHeaders}, "")
	if err != nil {
		t.Fatal(err)
	}
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_mcp", map[string]any{"action": "install", "name": custom.Name}, 400)
	// Function and message runs can execute as the owner without external
	// claims. Exercise their registered agent tool with the server's Crew
	// session binding, including legacy roots and output subfolders.
	reg := &recordingRegistrar{}
	f.api.eventStore = events.NewEventStore(10)
	f.api.sessionWorkspaceFolders = map[string]string{}
	if err := f.api.registerMultiAgentMCPServerTools(reg, nil); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{externalCommandCrewRoot, "_users/owner/Chats/Work/projects/legacy"} {
		for _, run := range []string{"function", "messages"} {
			session := run + "-" + root
			f.api.eventStore.SetSessionOwner(session, "owner")
			f.api.sessionWorkspaceFolders[session] = root + "/runs/" + run
			ctx := executor.WithSessionID(appCtx, session)
			for _, operation := range []string{"install_mcp_server", "add_mcp_server", "edit_mcp_server"} {
				for _, args := range []map[string]any{
					{"name": "untrusted", "url": "https://untrusted.example.com/mcp"},
					{"name": "Example", "server": map[string]any{"url": "https://untrusted.example.com/mcp"}},
					{"name": custom.Name},
				} {
					if _, err := reg.tools[operation].exec(ctx, args); err == nil || !strings.Contains(err.Error(), "Integrations") {
						t.Fatalf("%s %s %s refusal = %v", root, run, operation, err)
					}
				}
			}
		}
	}
	// Approved catalog accounts remain usable by an owner-authority Crew run.
	approvedCtx := executor.WithSessionID(appCtx, "messages-"+externalCommandCrewRoot)
	if _, err := reg.tools["install_mcp_server"].exec(approvedCtx, map[string]any{"name": "Example"}); err != nil {
		t.Fatalf("Crew catalog reuse = %v", err)
	}
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_mcp", map[string]any{"action": "remove", "name": attachments[0].Server}, 200)
	// All product adapters share this gate, including Builder-originated
	// operations. Refuse both nested URLs and endpoint edits before mutation.
	for _, claims := range []*UserClaims{crewWriter("owner"), {UserID: "owner", ExternalBuilderOperationID: "builder-operation"}} {
		ctx := context.WithValue(context.Background(), UserContextKey, claims)
		for _, operation := range []string{"install_mcp_server", "edit_mcp_server"} {
			if _, err := f.api.mcpConnectionTool(ctx, "owner", operation, map[string]any{"name": "Example", "server": map[string]any{"url": "https://untrusted.example.com/mcp"}}); err == nil || !strings.Contains(err.Error(), "Integrations") {
				t.Fatalf("%s nested URL refusal = %v", operation, err)
			}
		}
	}
	// A pre-existing custom URL with a catalog-like account name cannot be
	// attached through catalog reuse either.
	stale := personal[0]
	stale.Transport = "sse"
	if _, err = addPlaceMCPServer("owner", stale); err != nil {
		t.Fatal(err)
	}
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_mcp", map[string]any{"action": "install", "name": "Example"}, 400)
	if _, _, err = f.api.addPlaceMCP(appCtx, "owner", placeMCPServer{Name: personal[0].Name, URL: "https://custom.example.com/mcp", Headers: customHeaders}, ""); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"", " \t"} {
		crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_mcp", map[string]any{"action": "install", "name": "Example", "label": label}, 400)
	}
	current, _ = placeMCPAttachmentsFor(externalCommandCrewRoot)
	if len(current) != 0 {
		t.Fatalf("refused install attached a custom server: %v", current)
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
	if !strings.Contains(f.read(t, externalCommandCrewRoot+"/workflow.json"), "Workflow/research") {
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
	for _, group := range []string{"crew_skills", "crew_mcp", "crew_folders", "crew_references"} {
		action := map[string]string{"crew_skills": "uninstall", "crew_mcp": "remove", "crew_folders": "attach", "crew_references": "attach"}[group]
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
		return map[string]any{"action": "attach", "path": root, "alias": "references", "access": "read_only"}
	}
	bounded := crewWriter("owner")
	bounded.AccessToken.AllCrews = false
	bounded.AccessToken.CrewIDs = []string{"team"}
	crewCapabilityHTTP(t, f, bounded, "crew_folders", args(), 403)
	guarded := crewWriter("owner")
	guarded.AccessToken.FileGuard = &wf.FolderGuard{WritePaths: []string{"."}}
	crewCapabilityHTTP(t, f, guarded, "crew_folders", args(), 403)
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_folders", args(), 200)
	result := crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_folders", map[string]any{"action": "list"}, 200)
	if !strings.Contains(result, "references") {
		t.Fatal(result)
	}
	grants := workFolderGrantsForClaims(context.Background(), crewWriter("owner"))
	if len(grants) != 1 {
		t.Fatalf("grants=%v", grants)
	}
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_folders", map[string]any{"action": "detach", "id": grants[0].ID}, 200)
	if grants = workFolderGrantsForClaims(context.Background(), crewWriter("owner")); len(grants) != 0 {
		t.Fatalf("detach left grants=%v", grants)
	}
	outside := args()
	outside["path"] = t.TempDir()
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_folders", outside, 400)
}

// Real MCP calls read the disk-backed Crew while unrelated browsers, private
// accounts, endpoint config and raw cache errors stay outside the response.
func TestExternalCrewDiagnosticsOverMCP(t *testing.T) {
	f, workspaceURL := newExternalCrewCommandFixture(t)
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	f.api.logger = loggerv2.NewNoop()
	for _, name := range []string{"attached", "unattached"} {
		if _, err := addPlaceMCPServer("owner", placeMCPServer{Name: name, URL: "https://mcp.example.com/hidden-endpoint", Transport: "http"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := attachPersonalLoginToPlace("owner", "attached", externalCommandCrewRoot); err != nil {
		t.Fatal(err)
	}
	internal, config, err := placeMCPServerConfig("owner", "attached")
	if err != nil {
		t.Fatal(err)
	}
	cache := mcpcache.GetCacheManager(f.api.logger)
	entry := &mcpcache.CacheEntry{ServerName: internal, CreatedAt: time.Now(), IsValid: true, TTLMinutes: 30, ErrorMessage: "private-diagnostic-marker", Tools: []llmtypes.Tool{
		{Function: &llmtypes.FunctionDefinition{Name: "lookup", Description: "Find a record", Parameters: &llmtypes.Parameters{Type: "object", Properties: map[string]any{"query": map[string]any{"type": "string"}}, Required: []string{"query"}}}},
		{Function: &llmtypes.FunctionDefinition{Name: "count", Description: "Count records"}},
	}}
	if err := cache.Put(entry, config); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Invalidate(mcpcache.GenerateUnifiedCacheKey(internal, config)) })
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
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "true")
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
	tools := call(map[string]any{"action": "connections", "name": "attached", "limit": 1})["connection"].(map[string]any)
	page := tools["tools"].(map[string]any)
	if tools["status"] != "cached" || page["total"] != float64(2) || page["has_more"] != true || len(page["items"].([]any)) != 1 {
		t.Fatalf("tool metadata page = %v", tools)
	}
	if _, exposed := page["items"].([]any)[0].(map[string]any)["parameters"]; exposed {
		t.Fatal("reader saw full schemas")
	}
	crewCapabilityHTTP(t, f, reader, "crew_diagnostics", map[string]any{"action": "connections", "name": "attached", "include_tools": true}, 403)
	ownerSrv := serveExternalMCP(t, f.api, crewReader("owner"))
	ownerClient := dialExternalMCP(t, ctx, ownerSrv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, ownerClient)
	ownerTools := callRemoteTool(t, ctx, ownerClient, "call_tool", map[string]any{"name": "crew_diagnostics", "arguments": map[string]any{"crew_id": "team", "action": "connections", "name": "attached", "include_tools": true}})
	requireRemoteSuccess(t, ownerTools, "crew_diagnostics")
	if raw := marshalStructured(t, ownerTools); !strings.Contains(raw, `"parameters"`) || !strings.Contains(raw, `"required":["query"]`) {
		t.Fatalf("owner full schema = %s", raw)
	}
	crewCapabilityHTTP(t, f, reader, "crew_diagnostics", map[string]any{"action": "connections", "name": "unattached"}, 403)
	bounded := crewReader("other")
	bounded.AccessToken.AllCrews, bounded.AccessToken.CrewIDs = false, []string{"different"}
	crewCapabilityHTTP(t, f, bounded, "crew_diagnostics", map[string]any{"action": "browser"}, 403)
}
