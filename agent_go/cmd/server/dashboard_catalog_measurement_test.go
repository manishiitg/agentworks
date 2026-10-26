package server

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/relayproduct"
	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PLAT-825-113: measure real registered schemas, not declaration-name bytes.
// These are affected-family fixture measurements, not universal chat totals.
func logDashboardCatalogFootprints(t *testing.T, api *StreamingAPI) {
	t.Helper()
	crew, err := api.agentProfiles.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	flowBuilder, err := relayproduct.BuilderTools()
	if err != nil {
		t.Fatal(err)
	}
	flowRun, err := relayproduct.RunTools()
	if err != nil {
		t.Fatal(err)
	}
	for _, surface := range []struct {
		name string
		gate *productToolGate
		crew bool
	}{
		{"goal-builder", newProductToolGateForAllowlist("goal-builder", agentworksproduct.ChatTools("builder")), false},
		{"goal-run", newProductToolGateForAllowlist("goal-run", agentworksproduct.ChatTools("run")), false},
		{"flow-builder", newProductToolGateForAllowlist("flow-builder", flowBuilder), false},
		{"flow-run", newProductToolGateForAllowlist("flow-run", flowRun), false},
		{"crew-builder", newProductToolGate(&resolvedAgentProfile{Definition: crew}), true},
		{"crew-run-ask", newProductToolGate(&resolvedAgentProfile{Definition: crew}), true},
	} {
		if surface.name == "crew-run-ask" {
			surface.gate.DenyReaderTools(crewReaderDeniedTools()...)
			surface.gate.DenyWhere(func(name string) bool { return !crewAskAllowedTools[name] })
		}
		reg := &productSurfaceDraft{gate: surface.gate}
		root := "Workflow/catalog-fixture"
		if surface.crew {
			root = externalCommandCrewRoot
		}
		if err := api.registerDashboardTools(reg, "owner", root, &UserClaims{UserID: "owner"}); err != nil {
			t.Fatal(err)
		}
		if surface.crew {
			if err := api.registerWorkShareLinkTool(reg, "owner", root); err != nil {
				t.Fatal(err)
			}
		} else if err := api.registerShareLinkTools(reg, "owner", root); err != nil {
			t.Fatal(err)
		}
		if err := workflow.RegisterHTMLReportTools(reg, root, loggerv2.NewNoop(), func(context.Context, string) (string, error) { return "", nil }, workflow.ReportHTMLValidationHooks{}); err != nil {
			t.Fatal(err)
		}
		if err := api.registerReportPreviewTool(reg, "catalog-fixture", "owner", root); err != nil {
			t.Fatal(err)
		}
		projectRecordedCatalog(t, &reg.recordingRegistrar)
		logDashboardSchemaEnvelope(t, surface.name, reg.tools)
	}
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	external := map[string]recordedTool{}
	for _, tool := range externalListedTools(&UserClaims{UserID: "owner"}, catalog) {
		if tool.Name == "dashboard" || tool.Name == "share" || tool.Name == "get_dashboard_catalog" {
			external[tool.Name] = recordedTool{desc: tool.Description, params: tool.InputSchema}
		}
	}
	logDashboardSchemaEnvelope(t, "external-mcp-owner", external)
}

func projectRecordedCatalog(t *testing.T, reg *recordingRegistrar, selections ...platformtools.Selection) {
	t.Helper()
	definitions := []mcpagent.ToolDefinition{}
	for name, tool := range reg.tools {
		definitions = append(definitions, mcpagent.ToolDefinition{Name: name, Description: tool.desc, InputSchema: tool.params, Execute: tool.exec, DisplayGroup: tool.group})
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
	projected, err := platformtools.Project(definitions, selections...)
	if err != nil {
		t.Fatal(err)
	}
	reg.tools = map[string]recordedTool{}
	for _, tool := range projected {
		reg.tools[tool.Name] = recordedTool{desc: tool.Description, group: tool.DisplayGroup, params: tool.InputSchema, exec: tool.Execute}
	}
}

func logDashboardSchemaEnvelope(t *testing.T, surface string, tools map[string]recordedTool) {
	t.Helper()
	names := []string{}
	for name := range tools {
		if isDashboardTool(name) || name == "validate_report_html" || name == "preview_report" || name == "dashboard" || name == "share" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	envelope := []map[string]any{}
	for _, name := range names {
		tool := tools[name]
		envelope = append(envelope, map[string]any{"name": name, "description": tool.desc, "inputSchema": tool.params})
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("DASHBOARD_SCHEMA surface=%s tools=%d bytes=%d", surface, len(names), len(encoded))
}
