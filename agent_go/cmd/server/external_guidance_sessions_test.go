package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/pkoukk/tiktoken-go"
)

// Drives actual HTTP MCP, REST, stdio and CLI through the same authorized
// dispatcher, real dashboard files and durable receipts. Authentication is the
// fixture's existing trusted claims seam; product authorization is not mocked.
func TestExternalGuidanceSessionsNative(t *testing.T) {
	f := newExternalToolsFixture(t)
	withProjectOwnerRegistry(t)
	t.Setenv("AUTH_SECRET", "guidance-session-fixture-secret-for-tests")
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_MCP_BUILDER_ENABLED", "true")
	owner := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{ID: "guidance-owner", Scopes: []string{"dashboards:read", "workflows:read", "files:read", "runs:execute", "builder:chat"}, WorkflowIDs: []string{"invoices"}}}
	var currentAPI atomic.Pointer[StreamingAPI]
	currentAPI.Store(f.api)
	var current atomic.Pointer[UserClaims]
	current.Store(owner)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := current.Load()
		if r.Header.Get("Authorization") == "Bearer another-connection" {
			copy := *owner
			token := *owner.AccessToken
			token.ID = "another-connection"
			copy.AccessToken = &token
			claims = &copy
		}
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
		switch r.URL.Path {
		case externalMCPPath:
			currentAPI.Load().handleExternalMCP(w, r)
		case "/api/external/v1/tools":
			currentAPI.Load().handleExternalTools(w, r)
		case "/api/external/v1/call":
			currentAPI.Load().handleExternalCall(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Cleanup(func() {
		if f.api.guidanceStore != nil {
			_ = f.api.guidanceStore.Close()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cli := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	decode := func(result *mcp.CallToolResult) map[string]any {
		t.Helper()
		requireRemoteSuccess(t, result, "native guidance")
		var body map[string]any
		if err := json.Unmarshal([]byte(marshalStructured(t, result)), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	id := func(body map[string]any) string {
		guidance, _ := body["guidance"].(map[string]any)
		handle, _ := guidance["guidance_session_id"].(string)
		return handle
	}
	status := func(body map[string]any) string {
		if compact, ok := body["guidance"].(string); ok {
			fields := strings.Fields(compact)
			if len(fields) < 2 {
				t.Fatalf("invalid compact hint: %s", compact)
			}
			if fields[1] == "current" {
				return "delivered_to_client"
			}
			// Multi-topic reminders put the non-current status first.
			switch fields[0] {
			case "missing", "stale", "unavailable":
				return fields[0]
			}
			return fields[1]
		}
		guidance, _ := body["guidance"].(map[string]any)
		topics, _ := guidance["topics"].([]any)
		for _, raw := range topics {
			topic := raw.(map[string]any)
			if topic["topic"] == "dashboard-authoring" {
				return topic["status"].(string)
			}
		}
		t.Fatalf("dashboard authoring receipt missing: %+v", body)
		return ""
	}
	call := func(name string, args map[string]any, handle string) map[string]any {
		t.Helper()
		return decode(callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": name, "arguments": args, "guidance_session_id": handle}))
	}
	menu := decode(callRemoteTool(t, ctx, cli, externalMCPToolSpec, map[string]any{}))
	if id(menu) != "" || menu["guidance_skills"] != nil {
		t.Fatal("metadata listing created delivery")
	}
	version := externalTopicVersionFor(owner, *externalGuidanceTopicByName("dashboard-authoring"))
	claimed := call("help", map[string]any{"action": "skills", "topics": []any{"dashboard-authoring"}, "known_versions": map[string]any{"dashboard-authoring": version}}, "")
	if id(claimed) != "" {
		t.Fatal("claimed known version established delivery")
	}
	if call("dashboard", map[string]any{"action": "list"}, "")["guidance"] != nil {
		t.Fatal("ordinary discovery should omit guidance")
	}
	prep := decode(callRemoteTool(t, ctx, cli, externalMCPToolSpec, map[string]any{"names": []any{"dashboard"}}))
	handle := id(prep)
	if handle == "" || prep["guidance_skills"] == nil || status(prep) != "delivered_to_client" {
		t.Fatalf("preparation: %+v", prep)
	}
	// Dashboard preparation includes shared panel, authoring and motion skills.
	prepared := map[string]bool{}
	for _, raw := range prep["guidance_skills"].([]any) {
		prepared[raw.(map[string]any)["name"].(string)] = true
	}
	if len(prepared) != 3 || !prepared["dashboard-authoring"] || !prepared["motion-guide"] || !prepared["panel-dashboard"] {
		t.Fatalf("dashboard preparation skills: %+v", prepared)
	}
	// A second conversation with the same identity has its own independent handle.
	second := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, second)
	prepB := decode(callRemoteTool(t, ctx, second, externalMCPToolSpec, map[string]any{"names": "dashboard"}))
	if id(prepB) == handle || id(prepB) == "" {
		t.Fatal("parallel clients share a context")
	}
	enc, err := tiktoken.GetEncoding("cl100k_base")
	if err != nil {
		t.Fatal(err)
	}
	wire := func(value map[string]any) string {
		data, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		return string(data)
	}
	measured := call("create_dashboard", map[string]any{"workspace": "Workflow/invoices", "dashboard_id": "measured", "title": "Measured", "files": map[string]any{"index.html": "<html><title>Measured</title><body>Data</body></html>"}}, handle)
	revision := measured["dashboard"].(map[string]any)["revision"].(string)
	beforeCalls, err := f.api.guidanceStore.Stats(ctx)
	if err != nil || beforeCalls.Deliveries != int64(2*len(prepared)) {
		t.Fatalf("independent preparations must each deliver the selected skills once: %+v %v", beforeCalls, err)
	}
	baselineBytes, baselineTokens, advisoryBytes, advisoryTokens, noHandleBytes, noHandleTokens := 0, 0, 0, 0, 0, 0
	for i := 0; i < 20; i++ {
		name, args := "dashboard", map[string]any{"action": "update", "workspace": "Workflow/invoices", "dashboard_id": "measured", "title": fmt.Sprintf("Measured %02d", i), "expected_revision": revision}
		if i%2 == 1 {
			name = "update_dashboard"
			delete(args, "action")
		}
		body := call(name, args, handle)
		revision = body["dashboard"].(map[string]any)["revision"].(string)
		if status(body) != "delivered_to_client" || body["guidance_skills"] != nil {
			t.Fatalf("duplicate or alias delivery: %+v", body)
		}
		raw := wire(body)
		advisoryBytes += len(raw)
		advisoryTokens += len(enc.EncodeOrdinary(raw))
		delete(body, "guidance")
		raw = wire(body)
		baselineBytes += len(raw)
		baselineTokens += len(enc.EncodeOrdinary(raw))
		args["expected_revision"] = revision
		args["title"] = fmt.Sprintf("Legacy %02d", i)
		legacy := call(name, args, "")
		revision = legacy["dashboard"].(map[string]any)["revision"].(string)
		if id(legacy) != "" || legacy["guidance_skills"] != nil || status(legacy) != "missing" {
			t.Fatal("legacy client got repeated body")
		}
		raw = wire(legacy)
		noHandleBytes += len(raw)
		noHandleTokens += len(enc.EncodeOrdinary(raw))
	}
	if advisoryTokens-baselineTokens > 20*25 {
		t.Fatalf("ordinary guidance exceeds 25 tokens/call: %d", advisoryTokens-baselineTokens)
	}
	if call("list_dashboards", map[string]any{}, handle)["guidance"] != nil {
		t.Fatal("list reply includes unnecessary guidance")
	}
	if call("get_dashboard", map[string]any{"workflow_id": "invoices", "dashboard_id": "measured"}, handle)["guidance"] != nil {
		t.Fatal("get reply includes unnecessary guidance")
	}
	counts, err := f.api.guidanceStore.Stats(ctx)
	// Ordinary calls do not redeliver either conversation's prepared skills.
	if err != nil || counts.Deliveries != beforeCalls.Deliveries {
		t.Fatalf("20 calls re-delivered skills: %+v %v", counts, err)
	}
	guideBytes := len(wire(prep))
	guideTokens := len(enc.EncodeOrdinary(wire(prep)))
	t.Logf("guidance measurement cl100k_base serialized structured payloads (reference tokens, not billed usage): 20 baseline calls=%d bytes/%d tokens; advisory calls=%d bytes/%d tokens; no-handle calls=%d bytes/%d tokens; preparation=%d bytes/%d tokens; deliveries=%d; single preparation+20 calls=%d bytes/%d tokens; repeated preparation 20x+calls=%d bytes/%d tokens", baselineBytes, baselineTokens, advisoryBytes, advisoryTokens, noHandleBytes, noHandleTokens, guideBytes, guideTokens, counts.Deliveries, guideBytes+advisoryBytes, guideTokens+advisoryTokens, 20*guideBytes+advisoryBytes, 20*guideTokens+advisoryTokens)
	rest, err := agentworksclient.New(srv.URL, "owner-connection")
	if err != nil {
		t.Fatal(err)
	}
	rest.WithGuidanceSession(handle, nil)
	schema, err := rest.Schemas(ctx, []string{"create_dashboard", "dashboard"})
	if err != nil {
		t.Fatal(err)
	}
	var reused map[string]any
	_ = json.Unmarshal(schema, &reused)
	if id(reused) != handle || reused["guidance_skills"] != nil {
		t.Fatal("REST schemas did not reuse canonical topic")
	}
	// Neither an absent receipt nor a forged receipt prevents an authorized mutation.
	raw, err := rest.Call(agentworksclient.WithGuidanceSession(ctx, "awg_forged"), "create_dashboard", map[string]any{"workspace": "Workflow/invoices", "dashboard_id": "guide", "title": "Guide", "files": map[string]any{"index.html": "<html><title>Guide</title><body>Visible</body></html>"}})
	if err != nil {
		t.Fatal(err)
	}
	var created map[string]any
	_ = json.Unmarshal(raw, &created)
	if status(created) != "missing" {
		t.Fatal("forged context gained receipt")
	}
	other, _ := agentworksclient.New(srv.URL, "another-connection")
	other.WithGuidanceSession(handle, nil)
	raw, err = other.Call(ctx, "create_dashboard", map[string]any{"workspace": "Workflow/invoices", "dashboard_id": "connection-probe", "title": "Connection probe", "files": map[string]any{"index.html": "<html><body>Probe</body></html>"}})
	if err != nil {
		t.Fatal(err)
	}
	var foreign map[string]any
	_ = json.Unmarshal(raw, &foreign)
	if id(foreign) != "" || status(foreign) != "missing" {
		t.Fatal("cross-connection receipt leaked")
	}
	// Same user with narrowed live consent cannot reuse guidance to gain editing.
	limited := *owner
	token := *owner.AccessToken
	token.Scopes = []string{"dashboards:read"}
	limited.AccessToken = &token
	current.Store(&limited)
	_, err = rest.Call(ctx, "update_dashboard", map[string]any{"workspace": "Workflow/invoices", "dashboard_id": "guide", "expected_revision": created["dashboard"].(map[string]any)["revision"], "title": "Denied"})
	if err == nil {
		t.Fatal("receipt upgraded consent")
	}
	current.Store(owner)
	// Restart uses the same private database; no new skill body.
	if err = f.api.guidanceStore.Close(); err != nil {
		t.Fatal(err)
	}
	f.api = &StreamingAPI{}
	currentAPI.Store(f.api)
	resumed := decode(callRemoteTool(t, ctx, cli, externalMCPToolSpec, map[string]any{"names": "dashboard", "guidance_session_id": handle}))
	if id(resumed) != handle || resumed["guidance_skills"] != nil {
		t.Fatal("restart/resume re-delivered topic")
	}
	// A changed served version makes only that topic stale, then preparation refreshes it.
	req := httptest.NewRequest("POST", "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, owner))
	_, err = f.api.guidanceStore.Check(ctx, externalGuidanceCaller(req), handle, false, map[string]string{"dashboard-authoring": "previous-version"}, map[string]bool{"dashboard-authoring": true}, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if status(call("create_dashboard", map[string]any{"workspace": "Workflow/invoices", "dashboard_id": "stale-probe", "title": "Stale probe", "files": map[string]any{"index.html": "<html><body>Probe</body></html>"}}, handle)) != "stale" {
		t.Fatal("stale receipt not flagged")
	}
	refreshed := decode(callRemoteTool(t, ctx, cli, externalMCPToolSpec, map[string]any{"names": "dashboard", "guidance_session_id": handle}))
	if id(refreshed) != handle || refreshed["guidance_skills"] == nil {
		t.Fatal("stale preparation not refreshed")
	}
	// Decorating domain data must not round integer IDs through float64.
	precise := httptest.NewRecorder()
	wrapped, finish := f.api.externalGuidanceResponse(precise, req, "create_dashboard", handle)
	_, _ = wrapped.Write([]byte(`{"id":9007199254740993,"nested":{"values":[9007199254740995]}}`))
	finish()
	if !strings.Contains(precise.Body.String(), `"id":9007199254740993`) || !strings.Contains(precise.Body.String(), `[9007199254740995]`) {
		t.Fatalf("integer precision lost: %s", precise.Body.String())
	}
	// Context loss explicitly reloads even an unchanged delivered version.
	reloaded := call("help", map[string]any{"action": "skills", "topics": []any{"dashboard-authoring"}}, handle)
	if id(reloaded) != handle || status(reloaded) != "delivered_to_client" {
		t.Fatal("explicit context-loss reload failed")
	}
	_, err = rest.Call(ctx, "create_dashboard", map[string]any{"workspace": "Workflow/invoices", "dashboard_id": "invalid-probe", "title": "Invalid probe", "files": map[string]any{"index.html": "<html><body>Probe</body></html>"}, "unknown": true})
	if err == nil {
		t.Fatal("domain validator bypassed")
	}
	counts, err = f.api.guidanceStore.Stats(ctx)
	if err != nil || counts.ValidatorFailures != 1 || counts.StaleHits == 0 {
		t.Fatalf("diagnostic counters: %+v %v", counts, err)
	}
	// Real stdio: explicit full skill output, then same handle on a native tool.
	bridge, err := agentworksclient.NewMCPServer(ctx, rest)
	if err != nil {
		t.Fatal(err)
	}
	in, writeIn := io.Pipe()
	readOut, out := io.Pipe()
	defer in.Close()
	defer writeIn.Close()
	defer readOut.Close()
	defer out.Close()
	done := make(chan error, 1)
	go func() { done <- mcpserver.NewStdioServer(bridge).Listen(ctx, in, out) }()
	stdio := mcpclient.NewClient(transport.NewIO(readOut, writeIn, nil))
	defer stdio.Close()
	if err = stdio.Start(ctx); err != nil {
		t.Fatal(err)
	}
	initializeExternalMCP(t, ctx, stdio)
	stdioGuide := decode(callRemoteTool(t, ctx, stdio, "help", map[string]any{"action": "skills", "topics": []any{"dashboard-authoring"}, "guidance_session_id": handle}))
	if id(stdioGuide) != handle {
		t.Fatal("stdio did not return prepared handle")
	}
	stdioValidation := decode(callRemoteTool(t, ctx, stdio, "dashboard", map[string]any{"action": "validate", "workflow_id": "invoices", "dashboard_id": "guide", "guidance_session_id": handle}))
	if stdioValidation["guidance"] != nil {
		t.Fatal("stdio validation includes unnecessary guidance")
	}
	stdioCreated := decode(callRemoteTool(t, ctx, stdio, "dashboard", map[string]any{"action": "create", "workspace": "Workflow/invoices", "dashboard_id": "stdio-probe", "title": "Stdio probe", "files": map[string]any{"index.html": "<html><body>Probe</body></html>"}, "guidance_session_id": handle}))
	if status(stdioCreated) != "delivered_to_client" {
		t.Fatal("stdio lost common envelope")
	}
	_ = writeIn.Close()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Build and run the actual CLI only in this remote verification test. Repository
	// policy forbids running this Go test or a CLI build on the owner's laptop.
	binary := filepath.Join(t.TempDir(), "agentworks")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../agentworks")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("CLI build: %s %v", output, err)
	}
	command := exec.CommandContext(ctx, binary, "--config", filepath.Join(t.TempDir(), "config.json"), "--server", srv.URL, "--guidance-session", handle, "tools", "spec", "dashboard")
	command.Env = append(os.Environ(), "AGENTWORKS_TOKEN=owner-connection")
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	var cliResult map[string]any
	if err = json.Unmarshal(output, &cliResult); err != nil {
		t.Fatalf("CLI output: %s %v", output, err)
	}
	if id(cliResult) != handle || cliResult["guidance_skills"] != nil {
		t.Fatal("CLI failed preparation reuse")
	}
	// Storage failure remains advisory, and never writes receipt state into user docs.
	if err = f.api.guidanceStore.Close(); err != nil {
		t.Fatal(err)
	}
	fallback := call("create_dashboard", map[string]any{"workspace": "Workflow/invoices", "dashboard_id": "unavailable-probe", "title": "Unavailable probe", "files": map[string]any{"index.html": "<html><body>Probe</body></html>"}}, handle)
	if status(fallback) != "unavailable" {
		t.Fatal("storage failure refused ordinary operation")
	}
	// An invalid shared root also refuses creating receipt state in user files;
	// do not use it for a product operation, because other workspace services
	// intentionally rely on that same valid server state root.
	t.Setenv("AGENTWORKS_STATE_ROOT", f.docs)
	if _, err := privateAuthorityStatePath("-guidance.sqlite"); err == nil {
		t.Fatal("guidance state permitted inside product documents")
	}
	files, err := os.ReadDir(f.docs)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.Contains(file.Name(), "guidance") {
			t.Fatal("receipt in product documents")
		}
	}
}
