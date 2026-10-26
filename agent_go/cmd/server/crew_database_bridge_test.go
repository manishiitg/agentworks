package server

import (
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
	f.write(t, externalCommandCrewRoot+"/db/README.md", "Shared sample data.")
	live, err := sql.Open("sqlite", filepath.Join(f.docs, externalCommandCrewRoot, "db", "db.sqlite"))
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
			for _, tool := range catalog {
				if tool.Name != "database" {
					continue
				}
				props, _ := tool.Schema["properties"].(map[string]any)
				choice, _ := props["action"].(map[string]any)
				actions, _ = choice["enum"].([]any)
			}
			for _, action := range []string{"query", "describe", "integrity_check"} {
				if !slices.Contains(actions, any(action)) {
					t.Fatalf("native catalog lost %s: %v", action, actions)
				}
			}
			for _, action := range []string{"mutate", "migrate", "snapshot"} {
				if slices.Contains(actions, any(action)) == run {
					t.Fatalf("wrong %s authority in native catalog: %v", action, actions)
				}
			}
			result, err := codeexec.CallCustomToolWithSession(ctx, session, "database", map[string]any{"action": "query", "sql": "SELECT amount FROM sample_items"})
			if err != nil || !strings.Contains(result, "42") {
				t.Fatalf("shared stored row unavailable: %s %v", result, err)
			}
			if run {
				if _, err := codeexec.CallCustomToolWithSession(ctx, session, "database", map[string]any{"action": "mutate", "sql": "DELETE FROM sample_items"}); err == nil {
					t.Fatal("Run database admitted mutation")
				}
			}
			t.Logf("CREW_DATABASE_BRIDGE mode=%s actions=%v stored_row=42", name, actions)
		})
	}
}
