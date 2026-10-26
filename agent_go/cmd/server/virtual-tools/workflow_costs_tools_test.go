package virtualtools

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func TestWorkflowCostsToolDefinitionExplainsScopeAndPhaseColumn(t *testing.T) {
	raw, err := json.Marshal(workflowCostsQueryToolDefinition())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"costs/costs.sqlite", "cost_events", "phase", "PLAT-184"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("tool definition missing %q: %s", want, raw)
		}
	}
}

// PLAT-825-113: removing the outer source action used to make describe
// unreachable. Exercise both actions through the real registry executor.
func TestWorkflowCostsGroupedQueryAndDescribeReachTheirSource(t *testing.T) {
	session := "grouped-costs"
	common.SetSessionFolderGuard(session, []string{"Workflow/demo"}, nil)
	defer common.ClearSessionShellConfig(session)
	queries := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			SQL    string `json:"sql"`
			DBPath string `json:"db_path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input.DBPath != "Workflow/demo/costs/costs.sqlite" {
			t.Error("cost scope changed")
		}
		queries = append(queries, input.SQL)
		w.Write([]byte(`{"success":true,"data":{"rows":[],"columns":[]}}`))
	}))
	defer upstream.Close()
	registry := CreateWorkflowCostsToolRegistry(upstream.URL, "", session)
	definitions := []mcpagent.ToolDefinition{}
	for _, tool := range registry.Tools {
		raw, _ := json.Marshal(tool.Function.Parameters)
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		definitions = append(definitions, mcpagent.ToolDefinition{Name: tool.Function.Name, InputSchema: schema, Execute: registry.Executors[tool.Function.Name]})
	}
	jobs, err := platformtools.Project(definitions)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.Name != "costs" {
			continue
		}
		for _, action := range []string{"query", "describe"} {
			if _, err := job.Execute(t.Context(), map[string]any{"action": action, "sql": "SELECT phase FROM cost_events"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(queries) != 2 || queries[0] != "SELECT phase FROM cost_events" || queries[1] != workflowCostsDescribeSQL {
		t.Fatalf("cost actions did not reach their source: %v", queries)
	}
}

func TestResolveCurrentWorkflowCostsPathScopesToOwnWorkflowOnly(t *testing.T) {
	sessionID := "workflow-costs-path"
	defer common.ClearSessionShellConfig(sessionID)
	common.SetSessionFolderGuard(sessionID, []string{"/tmp/untrusted.sqlite", "Workflow/demo/builder"}, nil)

	path, err := resolveCurrentWorkflowCostsPath(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if path != "Workflow/demo/costs/costs.sqlite" {
		t.Fatalf("resolved path=%q", path)
	}
}

func TestResolveCurrentWorkflowCostsPathFailsClosedOutsideAnyWorkflowFolder(t *testing.T) {
	sessionID := "workflow-costs-path-none"
	defer common.ClearSessionShellConfig(sessionID)
	common.SetSessionFolderGuard(sessionID, []string{"_users/default/Chats"}, nil)

	if _, err := resolveCurrentWorkflowCostsPath(context.Background(), sessionID); err == nil {
		t.Fatal("expected an error outside any Workflow/<name> folder, got none")
	}
}
