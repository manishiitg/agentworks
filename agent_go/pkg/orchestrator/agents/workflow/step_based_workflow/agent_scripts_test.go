package step_based_workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	workspacepkg "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func sequenceScriptFixture() (*PlanningResponse, AgentItem) {
	child := &RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{
		ID: "fetch", Title: "Fetch", Description: "Fetch evidence",
		ValidationSchema: &ValidationSchema{Files: []FileValidationRule{{FileName: "result.json", MustExist: true}}},
		ScriptParameters: map[string]ScriptParameterDefinition{
			"market": {Type: "string", Description: "Market", Required: true},
		},
	}}
	item := AgentItem{ID: "collect", Type: "scripted", ScriptedSteps: []AgentScriptCall{
		{ID: "one", StepID: "fetch", Parameters: map[string]interface{}{"market": "NSE"}},
	}}
	parent := &AgentPlanStep{Type: StepTypeAgent, CommonStepFields: CommonStepFields{
		ID: "analyze", Title: "Analyze", Description: "Analyze the collected evidence",
	}, Items: []AgentItem{item, {ID: "report", Type: "user_message", Message: "Analyze results and report"}}}
	return &PlanningResponse{Steps: []PlanStepInterface{parent}, OrphanSteps: []PlanStepInterface{child}}, item
}

func TestSequenceScriptsPlanRoundTripAndInvalidReferences(t *testing.T) {
	plan, _ := sequenceScriptFixture()
	if err := ValidatePlanStructure(plan); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var decoded PlanningResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	call := decoded.Steps[0].(*AgentPlanStep).Items[0].ScriptedSteps[0]
	if call.StepID != "fetch" || call.Parameters["market"] != "NSE" {
		t.Fatalf("script reference lost: %+v", call)
	}
	decoded.OrphanSteps = nil // deleting a referenced definition must fail atomically
	if err := ValidatePlanStructure(&decoded); err == nil {
		t.Fatal("dangling script reference accepted")
	}
	for _, tc := range []struct {
		name string
		edit func(*PlanningResponse, *AgentItem)
	}{
		{"agentic target", func(p *PlanningResponse, _ *AgentItem) {
			p.OrphanSteps[0] = &AgentPlanStep{CommonStepFields: CommonStepFields{ID: "fetch"}}
		}},
		{"legacy agentic regular", func(p *PlanningResponse, _ *AgentItem) {
			p.OrphanSteps[0].(*RegularPlanStep).AgentConfigs = &AgentConfigs{LegacyDeclaredExecutionMode: StepModeAgentic}
		}},
		{"main-flow target", func(p *PlanningResponse, _ *AgentItem) {
			p.Steps = append(p.Steps, p.OrphanSteps...)
			p.OrphanSteps = nil
		}},
		{"missing parameter", func(_ *PlanningResponse, i *AgentItem) { i.ScriptedSteps[0].Parameters = nil }},
		{"wrong parameter type", func(_ *PlanningResponse, i *AgentItem) { i.ScriptedSteps[0].Parameters["market"] = 1 }},
		{"unknown parameter", func(_ *PlanningResponse, i *AgentItem) { i.ScriptedSteps[0].Parameters["invented"] = true }},
		{"no validation", func(p *PlanningResponse, _ *AgentItem) {
			p.OrphanSteps[0].(*RegularPlanStep).ValidationSchema = nil
		}},
		{"path traversal", func(_ *PlanningResponse, i *AgentItem) { i.ScriptedSteps[0].ID = "../outside" }},
		{"duplicate ID", func(_ *PlanningResponse, i *AgentItem) {
			i.ScriptedSteps = append(i.ScriptedSteps, i.ScriptedSteps[0])
		}},
		{"permission override", func(_ *PlanningResponse, i *AgentItem) { i.WriteAccess.DB = true }},
		{"over limit", func(_ *PlanningResponse, i *AgentItem) { i.MaxParallel = 9 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, item := sequenceScriptFixture()
			tc.edit(plan, &item)
			if _, err := resolveSequenceScripts(item, plan); err == nil {
				t.Fatal("invalid script call accepted")
			}
		})
	}
}

func sequenceBatchCalls(n int) []resolvedSequenceScript {
	calls := make([]resolvedSequenceScript, n)
	for i := range calls {
		calls[i].Call = AgentScriptCall{ID: fmt.Sprintf("task-%d", i), StepID: fmt.Sprintf("script-%d", i)}
	}
	return calls
}

func TestSequenceScriptsP0ParallelBarrierAndFailureCoverage(t *testing.T) {
	started := make(chan string, 10)
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	finished := make(chan []sequenceScriptResult, 1)
	go func() {
		results, err := runSequenceScriptBatch(t.Context(), sequenceBatchCalls(10), 2, func(ctx context.Context, call resolvedSequenceScript) (string, error) {
			started <- call.Call.ID
			<-release
			if call.Call.ID == "task-0" {
				return "output", errors.New("validation failed")
			}
			return "output", nil
		})
		if err == nil {
			t.Error("failed validation promoted to success")
		}
		finished <- results
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("parallel workers did not start")
		}
	}
	select {
	case <-finished:
		t.Fatal("batch returned before workers settled")
	default:
	}
	select {
	case <-started:
		t.Fatal("concurrency bound exceeded")
	default:
	}
	close(release)
	select {
	case results := <-finished:
		if len(results) != 10 || results[0].Status != "failed" {
			t.Fatalf("incomplete outcomes: %+v", results)
		}
		for _, result := range results[1:] {
			if result.Status != "completed" {
				t.Fatalf("ordinary failure hid remaining work: %+v", result)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("batch did not settle")
	}
}

func TestSequenceScriptsP0StopCancelsActiveAndSkipsPending(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var ran atomic.Int32
	results, err := runSequenceScriptBatch(ctx, sequenceBatchCalls(10), 1, func(ctx context.Context, _ resolvedSequenceScript) (string, error) {
		ran.Add(1)
		cancel()
		<-ctx.Done()
		return "partial-output", ctx.Err()
	})
	if !errors.Is(err, context.Canceled) || ran.Load() != 1 || results[0].Status != "cancelled" {
		t.Fatalf("Stop failed: ran=%d err=%v results=%+v", ran.Load(), err, results)
	}
	for _, result := range results[1:] {
		if result.Status != "not_started" {
			t.Fatalf("pending call ran after Stop: %+v", result)
		}
	}
}

func TestSequenceScriptsP0SavedRunnerAndValidation(t *testing.T) {
	for _, layout := range []int32{0, 1} {
		for _, validOutput := range []bool{true, false} {
			t.Run(fmt.Sprintf("layout-%d-valid-output-%v", layout, validOutput), func(t *testing.T) {
				t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
				var mu sync.Mutex
				var received []workspacepkg.ExecuteShellCommandParams
				var savedReceipt string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch {
					case r.URL.Path == "/api/execute":
						var req workspacepkg.ExecuteShellCommandParams
						if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
							t.Error(err)
							http.Error(w, "bad request", 400)
							return
						}
						mu.Lock()
						received = append(received, req)
						mu.Unlock()
						fmt.Fprint(w, `{"success":true,"data":{"stdout":"script executed","exit_code":0}}`)
					case r.Method == http.MethodPost && r.URL.Path == "/api/folders":
						w.WriteHeader(http.StatusCreated)
						fmt.Fprint(w, `{"success":true}`)
					case r.Method == http.MethodGet && r.URL.Path == "/api/documents":
						fmt.Fprint(w, `{"success":true,"data":[]}`)
					case r.Method == http.MethodGet && (strings.HasSuffix(r.URL.Path, "/code/fetch/main.py") || strings.HasSuffix(r.URL.Path, "/learnings/fetch/main.py")):
						fmt.Fprint(w, `{"success":true,"data":{"content":"import json, os\nprint(json.loads(os.environ['STEP_PARAMS_JSON'])['market'])\n"}}`)
					case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/result.json") && validOutput:
						fmt.Fprint(w, `{"success":true,"data":{"content":"{\"status\":\"ok\"}"}}`)
					case r.Method == http.MethodGet:
						http.Error(w, "not found", http.StatusNotFound)
					case r.Method == http.MethodPut:
						if strings.HasSuffix(r.URL.Path, "/results.json") {
							var body struct {
								Content string `json:"content"`
							}
							if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
								t.Error(err)
							}
							mu.Lock()
							savedReceipt = body.Content
							mu.Unlock()
						}
						fmt.Fprint(w, `{"success":true}`)
					default:
						fmt.Fprint(w, `{"success":true}`)
					}
				}))
				defer server.Close()
				t.Setenv("WORKSPACE_API_URL", server.URL)
				base, err := orchestrator.NewBaseOrchestrator(loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				base.WorkspaceClient = workspacepkg.NewClient(server.URL)
				base.SetWorkspacePath("Workflow/scripts-test")
				hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base, selectedRunFolder: "iteration-1/default"}
				hcpo.codeLayoutVersion.Store(layout)
				plan, item := sequenceScriptFixture()
				session := &agentSequenceSession{scriptedPlan: plan, LastRuntimeContext: "Opening instruction"}
				_, err = hcpo.executeAgentItem(t.Context(), plan.Steps[0].(*AgentPlanStep), item, 0, "step-1", session, false)
				if (err == nil) != validOutput {
					t.Fatalf("validation result=%v, validOutput=%v", err, validOutput)
				}
				mu.Lock()
				defer mu.Unlock()
				if len(received) != 1 {
					t.Fatalf("expected one saved-script execution, got %d", len(received))
				}
				if received[0].ExtraEnv[ScriptedParametersEnv] != `{"market":"NSE"}` {
					t.Fatalf("parameters lost: %+v", received[0].ExtraEnv)
				}
				want := "Workflow/scripts-test/runs/iteration-1/default/execution/analyze/scripts/items/collect/calls/one"
				if received[0].FolderGuard.WritePaths[0] != want || !strings.HasSuffix(received[0].ExtraEnv["STEP_OUTPUT_DIR"], want) {
					t.Fatalf("invocation output/guard mismatch: %+v", received[0])
				}
				if layout == 0 && !strings.Contains(received[0].Command, want+"/code/main.py") {
					t.Fatalf("legacy script did not use isolated execution copy: %s", received[0].Command)
				}
				if layout == 1 && !strings.Contains(received[0].Command, "/code/fetch/main.py") {
					t.Fatalf("version 1 did not use canonical source: %s", received[0].Command)
				}
				if session.runtime != nil || session.delegation != nil {
					t.Fatal("script created an agent runtime")
				}
				var receipt []sequenceScriptResult
				if err := json.Unmarshal([]byte(savedReceipt), &receipt); err != nil {
					t.Fatalf("missing durable receipt: %q: %v", savedReceipt, err)
				}
				wantStatus := "failed"
				if validOutput {
					wantStatus = "completed"
				}
				if len(receipt) != 1 || receipt[0].Status != wantStatus {
					t.Fatalf("invalid receipt: %+v", receipt)
				}
				if validOutput && (!strings.Contains(session.LastRuntimeContext, "Opening instruction") || !strings.Contains(session.LastRuntimeContext, "results.json")) {
					t.Fatal("next turn lost opening instruction or batch results")
				}
			})
		}
	}
}
