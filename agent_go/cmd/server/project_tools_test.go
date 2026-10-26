package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// Older file regressions keep their inputs and access assertions, but issue
// the current public family/action request instead of retired source names.
func canonicalFileTestCall(name string, args map[string]any) (string, map[string]any) {
	for source, action := range map[string]string{"read_file": "read", "write_file": "write", "list_files": "list", "search_files": "search", "list_step_code": "code"} {
		if name == source {
			return "files", withFileTestAction(args, action)
		}
	}
	return name, args
}
func withFileTestAction(args map[string]any, action string) map[string]any {
	input := map[string]any{"action": action}
	for k, v := range args {
		input[k] = v
	}
	return input
}

// Existing creation regressions exercise the new public action, rather than
// keeping a callable alias for the old source handler.
func testWorkspaceCreate(kind string, spec map[string]any) map[string]any {
	input := map[string]any{}
	for k, v := range spec {
		input[k] = v
	}
	key := ""
	if kind == "relay" {
		key = externalArg(input, "submission_id")
		delete(input, "submission_id")
	}
	if key == "" {
		raw, _ := json.Marshal(input)
		key = fmt.Sprintf("%x", sha256.Sum256(raw))
	}
	return map[string]any{"action": "create", "kind": kind, "spec": input, "idempotency_key": key}
}
func (f *externalRelayFixture) createGoal(t *testing.T, raw string, spec map[string]any, want int) map[string]any {
	t.Helper()
	return f.relayCall(t, raw, "workspace", testWorkspaceCreate("goal", spec), want)
}
func (f *externalRelayFixture) createRelay(t *testing.T, raw string, spec map[string]any, want int) map[string]any {
	t.Helper()
	return f.relayCall(t, raw, "workspace", testWorkspaceCreate("relay", spec), want)
}

func newProjectToolsFixture(t *testing.T) *externalToolsFixture {
	t.Helper()
	tokenTestSetup(t)
	f, _ := newExternalCrewCommandFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator","products":["agentworks","work","relays"]},{"id":"other","role":"creator","products":["agentworks","work","relays"]},{"id":"reader","role":"viewer","products":["agentworks","work","relays"]}]}`)
	f.api.logger = loggerv2.NewNoop()
	f.write(t, "Workflow/source/workflow.json", `{"schema_version":1,"id":"source","label":"Shared Goal","created_by":"other","access":{"owners":["other"],"readers":["owner"]}}`)
	f.write(t, "Workflow/source/planning/plan.json", `{"steps":[]}`)
	f.write(t, "Workflow/private/workflow.json", `{"schema_version":1,"id":"private","created_by":"other","access":{"owners":["other"]}}`)
	return f
}

func projectTestToken(t *testing.T) *UserClaims {
	t.Helper()
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	token, _, err := store.Issue(t.Context(), accesstokens.Token{Name: "Creation/copy test", UserID: "owner", Username: "owner", Scopes: []string{"builder:chat", "workflows:read", "files:read", "runs:execute", "relays:write", "crews:read", "crews:write"}, AllCrews: true, AllWorkflows: true, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	claims, err := accessTokenClaims(token)
	if err != nil {
		t.Fatal(err)
	}
	return claims
}

func projectCreateTestArgs(kind, key string) map[string]any {
	var spec map[string]any
	switch kind {
	case "goal":
		spec = map[string]any{"name": "Research " + key, "purpose": "Research sample work"}
	case "crew":
		spec = map[string]any{"name": "Research " + key, "purpose": "Research sample work"}
	case "relay":
		spec = map[string]any{"label": "Process " + key}
	}
	return map[string]any{"kind": kind, "spec": spec, "idempotency_key": key}
}

// PLAT-825: drive each actual native registration path and real MCP over
// HTTP against the workspace handlers, then inspect stored ownership/results.
func TestProjectCreationNativeAndMCPUseTheSameAction(t *testing.T) {
	f := newProjectToolsFixture(t)
	caller := &UserClaims{UserID: "owner", Username: "owner"}
	for _, surface := range []string{"goal", "crew", "relay"} {
		t.Run(surface, func(t *testing.T) {
			draft := &productSurfaceDraft{}
			if surface == "crew" {
				profile, err := f.api.agentProfiles.Resolve("work", 0, "owner")
				if err != nil {
					t.Fatal(err)
				}
				resolved := &resolvedAgentProfile{Definition: profile, ToolCallerClaims: caller}
				if err = f.api.registerAgentProfileTools(draft, newProductToolGate(resolved), resolved, "owner", "create-crew-chat", externalCommandCrewRoot, false, QueryRequest{}); err != nil {
					t.Fatal(err)
				}
			} else {
				root := "Workflow/native-" + surface
				f.write(t, root+"/workflow.json", fmt.Sprintf(`{"schema_version":1,"id":"native-%s","label":"Source","created_by":"owner","access":{"owners":["owner"]}}`, surface))
				f.write(t, root+"/planning/plan.json", `{"steps":[]}`)
				session, err := workflow.NewWorkshopChatSession(t.Context(), &workflow.WorkshopConfig{Logger: f.api.logger, WorkspacePath: root})
				if err != nil {
					t.Fatal(err)
				}
				defer session.Close()
				sid := "native-create-" + surface
				f.api.workshopChatSessions.Store(sid, session)
				vars := map[string]string{"WorkshopMode": "workshop"}
				if surface == "relay" {
					vars["WorkflowKind"] = "relay"
				}
				if err = f.api.installWorkflowPhaseTools(context.WithValue(t.Context(), UserContextKey, caller), draft, sid, "owner", "workflow-builder", root, "", vars, nil, nil, nil, nil, nil, QueryRequest{}, false); err != nil {
					t.Fatal(err)
				}
			}
			tool, ok := draft.tools["create_project"]
			if !ok {
				t.Fatalf("%s chat has no workspace.create member", surface)
			}
			if !reflect.DeepEqual(tool.params, projectCreateDefinition().InputSchema) {
				t.Fatal("native create schema differs from the common action")
			}
			for _, kind := range []string{"goal", "crew", "relay"} {
				args := projectCreateTestArgs(kind, surface+"-new-"+kind)
				first, err := tool.exec(t.Context(), args)
				if err != nil {
					t.Fatalf("%s creates %s: %v", surface, kind, err)
				}
				again, err := tool.exec(t.Context(), args)
				if err != nil {
					t.Fatal(err)
				}
				var a, b map[string]any
				_ = json.Unmarshal([]byte(first), &a)
				_ = json.Unmarshal([]byte(again), &b)
				if a["kind"] != kind || b["duplicate"] != true || a["workflow_id"] != b["workflow_id"] || a["crew_id"] != b["crew_id"] {
					t.Fatalf("retry minted another project: %s / %s", first, again)
				}
				if id, ok := a["workflow_id"].(string); ok {
					_, m, e := findWorkflowManifestByID(t.Context(), id)
					if e != nil || m == nil || m.CreatedBy != "owner" {
						t.Fatalf("wrong creator: %+v %v", m, e)
					}
				}
			}
		})
	}
	claims := projectTestToken(t)
	srv := serveExternalMCP(t, f.api, claims)
	cli := dialExternalMCP(t, t.Context(), srv.URL+externalMCPPath)
	initializeExternalMCP(t, t.Context(), cli)
	for _, kind := range []string{"goal", "crew", "relay"} {
		args := projectCreateTestArgs(kind, "mcp-new-"+kind)
		args["action"] = "create"
		got := callRemoteTool(t, t.Context(), cli, "call_tool", map[string]any{"name": "workspace", "arguments": args})
		requireRemoteSuccess(t, got, "MCP create "+kind)
	}
	// The conditional create schema in MCP is the same action definition the
	// native projection validates, with only its public action selector added.
	def, ok := externalCatalogTool("workspace")
	if !ok {
		t.Fatal("workspace absent")
	}
	condition := def.InputSchema["allOf"].([]any)[0].(map[string]any)["then"].(map[string]any)
	want := projectCreateDefinition().InputSchema
	want["properties"].(map[string]any)["action"] = map[string]any{"type": "string", "enum": []any{"create"}}
	want["required"] = append(want["required"].([]any), "action")
	if !reflect.DeepEqual(condition, want) {
		t.Fatal("MCP create action schema differs from native")
	}
	for _, retired := range []string{"create_workflow", "create_crew", "create_relay"} {
		result := callRemoteTool(t, t.Context(), cli, "call_tool", map[string]any{"name": retired, "arguments": map[string]any{}})
		if !result.IsError {
			t.Fatalf("retired creator %s is callable", retired)
		}
	}
	// Unlike an ordinary person token, a device connector keeps its assigned
	// scope even though the account itself may create projects (378af3e910).
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	device, _, err := store.Issue(t.Context(), accesstokens.Token{Name: "Device only", UserID: "owner", Username: "owner", Scopes: []string{"devices:connect"}, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
	store.Close()
	if err != nil {
		t.Fatal(err)
	}
	deviceClaims, err := accessTokenClaims(device)
	if err != nil {
		t.Fatal(err)
	}
	if !userAccessForClaims(deviceClaims).CanCreateIn("work") || deviceClaims.AccessToken.BuilderAccess() || projectCreationAvailable(deviceClaims) {
		t.Fatal("device consent inherited person authoring")
	}
	deviceServer := serveExternalMCP(t, f.api, deviceClaims)
	deviceClient := dialExternalMCP(t, t.Context(), deviceServer.URL+externalMCPPath)
	initializeExternalMCP(t, t.Context(), deviceClient)
	denied := callRemoteTool(t, t.Context(), deviceClient, "call_tool", map[string]any{"name": "workspace", "arguments": testWorkspaceCreate("crew", map[string]any{"name": "Refused", "purpose": "Device cannot author"})})
	if !denied.IsError || !strings.Contains(marshalContent(t, denied)+marshalStructured(t, denied), "insufficient_scope") {
		t.Fatal("device refusal did not come from its real tool scope")
	}
}

// PLAT-825: a shared source is readable, never writable by association;
// both access checks and each descendant's existing file guard must hold.
func TestProjectCopyRequiresSourceReadAndDestinationWrite(t *testing.T) {
	f := newProjectToolsFixture(t)
	claims := projectTestToken(t)
	f.write(t, "Workflow/source/notes/nested/brief.md", "Use SECRET_SAMPLE_KEY by name only.\n")
	f.write(t, "Workflow/source/notes/.env", "PRIVATE_SECRET_CANARY")
	f.write(t, "Workflow/source/builder/private-chat.json", "PRIVATE_CHAT_CANARY")
	reg := &recordingRegistrar{}
	if err := f.api.registerProjectTools(reg, nil, "owner", claims); err != nil {
		t.Fatal(err)
	}
	tool, ok := reg.tools["copy_project_files"]
	if !ok {
		t.Fatal("native files.copy absent")
	}
	def, ok := externalCatalogTool("copy_project_files")
	if !ok || !reflect.DeepEqual(def.InputSchema, tool.params) {
		t.Fatal("native/MCP copy schemas differ")
	}
	args := map[string]any{"source_project": "source", "source_path": "notes", "target_project": "work:team", "target_path": "copied"}
	text, err := tool.exec(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, `"file_count":1`) || f.read(t, externalCommandCrewRoot+"/copied/nested/brief.md") != "Use SECRET_SAMPLE_KEY by name only.\n" {
		t.Fatalf("copy result: %s", text)
	}
	if _, err = os.Stat(filepath.Join(f.docs, externalCommandCrewRoot, "copied/.env")); !os.IsNotExist(err) {
		t.Fatal("copied private secret store")
	}
	if f.read(t, "Workflow/source/notes/nested/brief.md") != "Use SECRET_SAMPLE_KEY by name only.\n" {
		t.Fatal("changed shared source")
	}
	if _, err = tool.exec(t.Context(), args); err == nil {
		t.Fatal("overwrote existing destination")
	}
	for _, bad := range []struct{ key, value string }{{"source_project", "private"}, {"target_project", "source"}, {"source_path", "builder/private-chat.json"}, {"source_path", "notes/.env"}, {"target_path", "planning/plan.json"}, {"source_path", "../private"}} {
		input := map[string]any{}
		for k, v := range args {
			input[k] = v
		}
		input["target_path"] = "refused-" + bad.key
		input[bad.key] = bad.value
		if _, err = tool.exec(t.Context(), input); err == nil {
			t.Fatalf("allowed %s=%s", bad.key, bad.value)
		}
	}
	// Explicit symlinks never become a way to read private source files.
	if err = os.Symlink(filepath.Join(f.docs, "Workflow/source/notes/.env"), filepath.Join(f.docs, "Workflow/source/linked")); err != nil {
		t.Fatal(err)
	}
	if _, err = tool.exec(t.Context(), map[string]any{"source_project": "source", "source_path": "linked", "target_project": "work:team", "target_path": "linked-copy"}); err == nil {
		t.Fatal("copied a symbolic link")
	}
	for i := 0; i < 65; i++ {
		f.write(t, fmt.Sprintf("Workflow/source/many/file-%02d.txt", i), "sample")
	}
	if _, err = tool.exec(t.Context(), map[string]any{"source_project": "source", "source_path": "many", "target_project": "work:team", "target_path": "too-many"}); err == nil {
		t.Fatal("copied over the file count limit")
	}
	if _, err = os.Stat(filepath.Join(f.docs, externalCommandCrewRoot, "too-many")); !os.IsNotExist(err) {
		t.Fatal("over-limit copy wrote a partial folder")
	}
	// Exercise the public MCP copy action as well, through real workspace I/O.
	srv := serveExternalMCP(t, f.api, claims)
	cli := dialExternalMCP(t, t.Context(), srv.URL+externalMCPPath)
	initializeExternalMCP(t, t.Context(), cli)
	args["target_path"] = "from-mcp"
	args["action"] = "copy"
	got := callRemoteTool(t, t.Context(), cli, "call_tool", map[string]any{"name": "files", "arguments": args})
	requireRemoteSuccess(t, got, "MCP file copy")
	if f.read(t, externalCommandCrewRoot+"/from-mcp/nested/brief.md") == "" {
		t.Fatal("MCP did not copy the actual file")
	}
}

func TestProjectCreateRejectsChangedRetryAndCallerSubstitution(t *testing.T) {
	f := newProjectToolsFixture(t)
	caller := &UserClaims{UserID: "owner"}
	reg := &recordingRegistrar{}
	if err := f.api.registerProjectTools(reg, nil, "owner", caller); err != nil {
		t.Fatal(err)
	}
	tool := reg.tools["create_project"]
	args := projectCreateTestArgs("crew", "stable-proposal")
	if _, err := tool.exec(t.Context(), args); err != nil {
		t.Fatal(err)
	}
	args["spec"].(map[string]any)["name"] = "Changed proposal"
	if _, err := tool.exec(t.Context(), args); err == nil {
		t.Fatal("changed proposal reused creation identity")
	}
	if _, err := tool.exec(context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "other"}), args); err == nil {
		t.Fatal("substituted another caller")
	}
	for _, spec := range []map[string]any{{"purpose": "Missing name"}, {"name": "Invalid skill", "purpose": "Check", "selected_skills": []any{"not-an-installed-skill"}}} {
		if _, err := tool.exec(t.Context(), map[string]any{"kind": "crew", "spec": spec, "idempotency_key": fmt.Sprint(spec)}); err == nil {
			t.Fatal("accepted invalid Crew spec")
		}
	}
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"viewer","products":["work"]}]}`)
	if _, err := tool.exec(t.Context(), projectCreateTestArgs("crew", "revoked")); err == nil {
		t.Fatal("retained creator bypassed current role")
	}
}
