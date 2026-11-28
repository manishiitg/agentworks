package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	agent "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentwrapper"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PLAT-750-75: registration alone hid this failure. Inspect the actual file
// prepared for the native bridge, then execute its projected tool against SQLite.
func TestCrewDatabaseBridgeReadsInBuilderAndRun(t *testing.T) {
	f, upstream := newExternalCrewCommandFixture(t)
	f.api.logger = loggerv2.NewNoop()
	f.write(t, externalCommandCrewAutomation+"/db/README.md", "Shared sample data.")
	// The Crew database lives in its .agentworks/db (PLAT-746-924).
	if err := os.MkdirAll(filepath.Join(f.docs, externalCommandCrewAutomation, "db"), 0o700); err != nil {
		t.Fatal(err)
	}
	live, err := sql.Open("sqlite", filepath.Join(f.docs, externalCommandCrewAutomation, "db", "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if _, err := live.Exec("CREATE TABLE sample_items (amount INTEGER); INSERT INTO sample_items VALUES (42)"); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.POST("/api/query", workspacehandlers.QueryWorkflowDB)
	parsed, _ := url.Parse(upstream)
	router.NoRoute(gin.WrapH(httputil.NewSingleHostReverseProxy(parsed)))
	service := httptest.NewServer(router)
	defer service.Close()
	t.Setenv("WORKSPACE_API_URL", service.URL)
	t.Setenv("MCP_API_URL", service.URL)
	t.Setenv("MCP_API_TOKEN", "catalog-test-token")
	// The SDK prepares the real bridge catalog before invoking the provider.
	// Keep the CLI unavailable: this check makes no model call or external effect.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("MCP_BRIDGE_BINARY", "/bin/false")
	config := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(config, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, run := range []bool{false, true} {
		name, user := "builder", "owner"
		if run {
			name, user = "run", "other"
		}
		t.Run(name, func(t *testing.T) {
			session := "crew-database-catalog-" + name
			defer common.ClearSessionShellConfig(session)
			common.SetSessionFolderGuard(session, []string{externalCommandCrewRoot}, nil)
			common.SetSessionCrewReader(session, run)
			common.SetSessionCrewAskReadOnly(session, run)
			stepworkflow.ConfigureManagedWorkflowDBSession(session, externalCommandCrewRoot, !run)
			profile := &resolvedAgentProfile{Definition: workproduct.BuiltinAgentProfile()}
			req := QueryRequest{AgentProfileID: "work", SelectedFolder: externalCommandCrewRoot}
			if err := applyCrewChatMode(profile, &req, run); err != nil {
				t.Fatal(err)
			}
			gate := newProductToolGate(profile)
			if run {
				gate.DenyReaderTools(crewReaderDeniedTools()...)
				gate.DenyWhere(func(name string) bool { return !crewAskAllowedTools[name] })
			}
			bridge := []string{}
			for _, name := range profile.Definition.Runtime.BridgeTools {
				if gate.Admit(name) {
					bridge = append(bridge, name)
				}
			}
			ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, session)
			dir := t.TempDir()
			wrapper, err := agent.NewLLMAgentWrapper(ctx, agent.LLMAgentConfig{
				Name: "crew-db-catalog", Provider: llm.ProviderCodexCLI, ModelID: "gpt-6-luna",
				ServerName: "NO_SERVERS", ConfigPath: config, SessionID: session, MaxTurns: 1,
				UseCodeExecutionMode: true, CodingAgentToolsMode: "mcp_only", ForceStructuredCodingAgent: true,
				CodingAgentWorkingDir: dir, AdditionalBridgeTools: bridge, AdmitTool: gate.Admit,
			}, nil, loggerv2.NewNoop())
			if err != nil {
				t.Fatal(err)
			}
			defer wrapper.Close()
			if err := f.api.registerWorkDashboardTools(wrapper, profile, session, user, externalCommandCrewRoot, run); err != nil {
				t.Fatal(err)
			}
			callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_, launchErr := wrapper.Invoke(callCtx, "Prepare the shared data tool catalog.")
			paths, err := filepath.Glob(filepath.Join(dir, ".mcpbridge-tools-*.json"))
			if err != nil || len(paths) != 1 {
				t.Fatalf("native bridge catalog missing: files=%v err=%v launch=%v", paths, err, launchErr)
			}
			raw, err := os.ReadFile(paths[0])
			if err != nil {
				t.Fatal(err)
			}
			var catalog []struct {
				Name   string         `json:"name"`
				Schema map[string]any `json:"input_schema"`
			}
			if err := json.Unmarshal(raw, &catalog); err != nil {
				t.Fatal(err)
			}
			var actions []any
			var hasFromProject bool
			for _, tool := range catalog {
				if tool.Name != "database" {
					continue
				}
				props, _ := tool.Schema["properties"].(map[string]any)
				choice, _ := props["action"].(map[string]any)
				actions, _ = choice["enum"].([]any)
				_, hasFromProject = props["from_project"]
			}
			if hasFromProject == run {
				t.Fatalf("wrong from_project exposure in native catalog (run=%t, has=%t)", run, hasFromProject)
			}
			for _, action := range []string{"query", "describe", "integrity_check"} {
				if !slices.Contains(actions, any(action)) {
					t.Fatalf("native catalog lost %s: %v", action, actions)
				}
			}
			for _, action := range []string{"mutate", "snapshot"} {
				if slices.Contains(actions, any(action)) == run {
					t.Fatalf("wrong %s authority in native catalog: %v", action, actions)
				}
			}
			// The migration-file action is retired (PLAT-746, 8 Nov): additive setup uses mutate. No mode lists it.
			if slices.Contains(actions, any("migrate")) {
				t.Fatalf("retired migrate action in native catalog: %v", actions)
			}
			readArgs := map[string]any{"action": "query", "sql": "SELECT amount FROM sample_items"}
			payload, _ := json.Marshal(readArgs)
			request := httptest.NewRequest("POST", "/tools/custom/database", bytes.NewReader(payload))
			if refused := refuseReaderDeniedToolRequest(httptest.NewRecorder(), request, ctx, "database"); refused {
				t.Fatal("native HTTP role fence refused the admitted database read")
			}
			var forwarded map[string]any
			if err := json.NewDecoder(request.Body).Decode(&forwarded); err != nil {
				t.Fatalf("role fence consumed the executor body: %v", err)
			}
			result, err := codeexec.CallCustomToolWithSession(ctx, session, "database", forwarded)
			if err != nil || !strings.Contains(result, "42") {
				t.Fatalf("shared stored row unavailable: %s %v", result, err)
			}
			if run {
				writeBody := strings.NewReader(`{"action":"mutate","sql":"DELETE FROM sample_items"}`)
				writeRequest := httptest.NewRequest("POST", "/tools/custom/database", writeBody)
				if !refuseReaderDeniedToolRequest(httptest.NewRecorder(), writeRequest, ctx, "database") {
					t.Fatal("Run HTTP role fence admitted database mutation")
				}
				if _, err := codeexec.CallCustomToolWithSession(ctx, session, "database", map[string]any{"action": "mutate", "sql": "DELETE FROM sample_items"}); err == nil {
					t.Fatal("Run database admitted mutation")
				}
			}
			t.Logf("CREW_DATABASE_BRIDGE mode=%s actions=%v stored_row=42", name, actions)
		})
	}
}

func TestCrewDatabaseFromProjectReadOnly(t *testing.T) {
	f, upstream := newExternalCrewCommandFixture(t)
	f.api.logger = loggerv2.NewNoop()
	owners := projectOwnersAt(os.Getenv("AGENTWORKS_STATE_ROOT"))
	if err := owners.Register(projectOwnerRecord{Product: "work", Folder: "source-proj", ProjectID: "source", OwnerID: "owner"}); err != nil {
		t.Fatal(err)
	}
	if err := owners.Register(projectOwnerRecord{Product: "work", Folder: "foreign-proj", ProjectID: "foreign", OwnerID: "other"}); err != nil {
		t.Fatal(err)
	}
	f.write(t, "Crew/source-proj/.agentworks/settings/product.json", `{"schema_version":1,"product":"work","id":"source","title":"Source Project","session_id":"sess-source"}`)
	f.write(t, "Crew/source-proj/.agentworks/settings/workflow.json", `{"id":"source","label":"Source Project","created_by":"owner"}`)
	f.write(t, "_users/other/Crew/foreign-proj/.agentworks/settings/product.json", `{"schema_version":1,"product":"work","id":"foreign","title":"Foreign Project","session_id":"sess-foreign"}`)
	f.write(t, "_users/other/Crew/foreign-proj/.agentworks/settings/workflow.json", `{"id":"foreign","label":"Foreign Project","created_by":"other"}`)

	for _, entry := range []struct {
		dir string
		sql string
	}{
		{filepath.Join(f.docs, externalCommandCrewAutomation, "db"), "CREATE TABLE own_items (label TEXT); INSERT INTO own_items VALUES ('current-project')"},
		{filepath.Join(f.docs, "Crew/source-proj/.agentworks/db"), "CREATE TABLE tax_records (fy TEXT, amount INTEGER); INSERT INTO tax_records VALUES ('2025-26', 9900)"},
		{filepath.Join(f.docs, "_users/other/Crew/foreign-proj/.agentworks/db"), "CREATE TABLE secret_records (note TEXT); INSERT INTO secret_records VALUES ('private-other')"},
	} {
		if err := os.MkdirAll(entry.dir, 0o700); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", filepath.Join(entry.dir, "db.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(entry.sql); err != nil {
			db.Close()
			t.Fatal(err)
		}
		db.Close()
	}

	router := gin.New()
	router.POST("/api/query", workspacehandlers.QueryWorkflowDB)
	parsed, _ := url.Parse(upstream)
	router.NoRoute(gin.WrapH(httputil.NewSingleHostReverseProxy(parsed)))
	service := httptest.NewServer(router)
	defer service.Close()
	t.Setenv("WORKSPACE_API_URL", service.URL)
	t.Setenv("MCP_API_URL", service.URL)
	t.Setenv("MCP_API_TOKEN", "from-project-test-token")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("MCP_BRIDGE_BINARY", "/bin/false")

	config := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(config, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	makeBuilder := func(t *testing.T, session string, claims *UserClaims, readOnly bool) (context.Context, func()) {
		t.Helper()
		common.SetSessionFolderGuard(session, []string{externalCommandCrewRoot}, nil)
		common.SetSessionCrewReader(session, readOnly)
		common.SetSessionCrewAskReadOnly(session, readOnly)
		stepworkflow.ConfigureManagedWorkflowDBSession(session, externalCommandCrewRoot, !readOnly)
		profile := &resolvedAgentProfile{Definition: workproduct.BuiltinAgentProfile(), ToolCallerClaims: claims}
		req := QueryRequest{AgentProfileID: "work", SelectedFolder: externalCommandCrewRoot}
		if err := applyCrewChatMode(profile, &req, readOnly); err != nil {
			t.Fatal(err)
		}
		gate := newProductToolGate(profile)
		if readOnly {
			gate.DenyReaderTools(crewReaderDeniedTools()...)
			gate.DenyWhere(func(name string) bool { return !crewAskAllowedTools[name] })
		}
		bridge := []string{}
		for _, name := range profile.Definition.Runtime.BridgeTools {
			if gate.Admit(name) {
				bridge = append(bridge, name)
			}
		}
		ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, session)
		wrapper, err := agent.NewLLMAgentWrapper(ctx, agent.LLMAgentConfig{
			Name: "crew-db-from-project", Provider: llm.ProviderCodexCLI, ModelID: "gpt-6-luna",
			ServerName: "NO_SERVERS", ConfigPath: config, SessionID: session, MaxTurns: 1,
			UseCodeExecutionMode: true, CodingAgentToolsMode: "mcp_only", ForceStructuredCodingAgent: true,
			CodingAgentWorkingDir: t.TempDir(), AdditionalBridgeTools: bridge, AdmitTool: gate.Admit,
		}, nil, loggerv2.NewNoop())
		if err != nil {
			t.Fatal(err)
		}
		if err := f.api.registerWorkDashboardTools(wrapper, profile, session, claims.UserID, externalCommandCrewRoot, readOnly); err != nil {
			wrapper.Close()
			t.Fatal(err)
		}
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, _ = wrapper.Invoke(callCtx, "Prepare tools.")
		cancel()
		return ctx, func() {
			wrapper.Close()
			common.ClearSessionShellConfig(session)
		}
	}

	// 1. Builder reads own DB when from_project is omitted, and reads source-proj when from_project is set.
	ownerCtx, cleanup := makeBuilder(t, "crew-db-from-owner", &UserClaims{UserID: "owner"}, false)
	defer cleanup()

	ownOut, err := codeexec.CallCustomToolWithSession(ownerCtx, "crew-db-from-owner", "database", map[string]any{
		"action": "query",
		"sql":    "SELECT label FROM own_items",
	})
	if err != nil || !strings.Contains(ownOut, "current-project") {
		t.Fatalf("own DB query failed: %q err=%v", ownOut, err)
	}
	descOut, err := codeexec.CallCustomToolWithSession(ownerCtx, "crew-db-from-owner", "database", map[string]any{
		"action":       "describe",
		"from_project": "Crew/source-proj",
	})
	if err != nil || !strings.Contains(descOut, "tax_records") {
		t.Fatalf("from_project describe failed: %q err=%v", descOut, err)
	}
	rowsOut, err := codeexec.CallCustomToolWithSession(ownerCtx, "crew-db-from-owner", "database", map[string]any{
		"action":       "query",
		"from_project": "Crew/source-proj",
		"sql":          "SELECT fy, amount FROM tax_records",
	})
	if err != nil || !strings.Contains(rowsOut, "9900") {
		t.Fatalf("from_project query failed: %q err=%v", rowsOut, err)
	}

	// 2. Foreign unshared project and non-canonical paths are refused.
	if out, err := codeexec.CallCustomToolWithSession(ownerCtx, "crew-db-from-owner", "database", map[string]any{
		"action":       "describe",
		"from_project": "_users/other/Crew/foreign-proj",
	}); err == nil {
		t.Fatalf("expected foreign unshared project refusal, got: %s", out)
	}
	if out, err := codeexec.CallCustomToolWithSession(ownerCtx, "crew-db-from-owner", "database", map[string]any{
		"action":       "describe",
		"from_project": "Crew/source-proj/db/db.sqlite",
	}); err == nil {
		t.Fatalf("expected non-root path refusal, got: %s", out)
	}
	if out, err := codeexec.CallCustomToolWithSession(ownerCtx, "crew-db-from-owner", "database", map[string]any{
		"action":       "describe",
		"from_project": "../escape",
	}); err == nil {
		t.Fatalf("expected traversal path refusal, got: %s", out)
	}
	if out, err := codeexec.CallCustomToolWithSession(ownerCtx, "crew-db-from-owner", "database", map[string]any{
		"action":       "mutate",
		"from_project": "Crew/source-proj",
		"sql":          "DELETE FROM tax_records",
	}); err == nil {
		t.Fatalf("expected mutate with from_project refusal, got: %s", out)
	}

	// 3. Bearer token limited to "team" cannot read "source".
	scopedClaims := crewWriter("owner")
	scopedClaims.AccessToken.CrewIDs = []string{"team"}
	scopedClaims.AccessToken.AllCrews = false
	scopedCtx, cleanupScoped := makeBuilder(t, "crew-db-from-scoped", scopedClaims, false)
	defer cleanupScoped()
	if out, err := codeexec.CallCustomToolWithSession(scopedCtx, "crew-db-from-scoped", "database", map[string]any{
		"action":       "describe",
		"from_project": "Crew/source-proj",
	}); err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Fatalf("expected scoped token refusal, got out=%q err=%v", out, err)
	}

	// 4. Run (readOnly) chat does not get from_project and cannot read source-proj.
	runCtx, cleanupRun := makeBuilder(t, "crew-db-from-run", &UserClaims{UserID: "other"}, true)
	defer cleanupRun()
	if out, err := codeexec.CallCustomToolWithSession(runCtx, "crew-db-from-run", "database", map[string]any{
		"action":       "describe",
		"from_project": "Crew/source-proj",
	}); err == nil && strings.Contains(out, "tax_records") {
		t.Fatalf("Run chat leaked source-proj database: %s", out)
	}
}
