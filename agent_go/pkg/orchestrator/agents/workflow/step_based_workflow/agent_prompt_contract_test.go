package step_based_workflow

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/spf13/viper"
)

type descriptionContractAgent struct {
	agents.OrchestratorAgent
	turn func(map[string]string, []llmtypes.MessageContent) (string, []llmtypes.MessageContent, error)
}

func (a *descriptionContractAgent) GetType() string { return "execution_only" }
func (a *descriptionContractAgent) GetConfig() *agents.OrchestratorAgentConfig {
	return &agents.OrchestratorAgentConfig{}
}
func (a *descriptionContractAgent) GetBaseAgent() *agents.BaseAgent { return nil }
func (a *descriptionContractAgent) Close() error                    { return nil }
func (a *descriptionContractAgent) Execute(_ context.Context, vars map[string]string, history []llmtypes.MessageContent) (string, []llmtypes.MessageContent, error) {
	return a.turn(vars, history)
}

// Exercise the actual queue, workspace HTTP persistence, automatic validation
// and same-conversation repair. Only the model decision is deterministic; no
// external provider or business action is contacted.
func TestDescriptionFirstExecutionAndRepair(t *testing.T) {
	gin.SetMode(gin.TestMode)
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("MULTI_USER_MODE", "false")
	oldDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", oldDocs) })
	router := gin.New()
	router.Any("/api/documents/*filepath", workspacehandlers.HandleDocumentRequest)
	router.POST("/api/folders", workspacehandlers.CreateFolder)
	server := httptest.NewServer(router)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	for _, contract := range []string{AgentDescriptionFirstPromptContract, ""} {
		for _, scenario := range []string{"no-followup", "followup", "pending-input"} {
			if contract == "" && scenario == "pending-input" {
				continue
			}
			followup, pending := scenario == "followup", scenario == "pending-input"
			t.Run(contract+"/"+scenario, func(t *testing.T) {
				c := newAgentClosingTestOrchestrator(t)
				c.selectedRunFolder = "iteration-0/default"
				c.WorkspaceClient = workspace.NewClient(server.URL)
				step := &AgentPlanStep{Type: StepTypeAgent, PromptContract: contract, SystemRules: "Do not repeat the external action.", PredefinedRoutes: []PlanOrchestrationRoute{{RouteID: "specialist", RouteName: "Specialist", SubAgentStep: &ScriptedPlanStep{Type: StepTypeScripted, CommonStepFields: CommonStepFields{ID: "specialist", Title: "Specialist", Description: "Bounded helper."}}}}, CommonStepFields: CommonStepFields{ID: "proof", Title: "Proof", Description: "Produce exactly one current proof.", ValidationSchema: &ValidationSchema{Files: []FileValidationRule{{FileName: "proof.json", MustExist: true, JSONChecks: []JSONValidationCheck{{Path: "$.ready", MustExist: true, ValueType: "boolean"}}}}}}, AgentConfigs: &AgentConfigs{KnowledgebaseAccess: KBAccessNone, LearningsAccess: LearningsAccessNone}}
				if contract == "" {
					step.Items = []AgentItem{{ID: "execute", Type: "user_message", Message: "Execute the legacy task."}}
				}
				if followup {
					step.Items = append(step.Items, AgentItem{ID: "check-evidence", Type: "user_message", Message: "Check the evidence without repeating the action."})
				}
				if err := validateAgentStepFieldsTyped(step); err != nil {
					t.Fatal(err)
				}
				output := filepath.Join(docs, "Workflow/test-flow/runs/iteration-0/default/execution/proof/proof.json")
				if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
					t.Fatal(err)
				}
				_ = os.Remove(output)
				calls, effects, repairs := 0, 0, 0
				model := &descriptionContractAgent{}
				model.turn = func(vars map[string]string, history []llmtypes.MessageContent) (string, []llmtypes.MessageContent, error) {
					renderer := &WorkflowExecutionOnlyAgent{}
					user := renderer.executionOnlyUserMessageProcessor(vars)
					system := renderer.executionOnlySystemPromptProcessor(vars)
					if strings.Contains(system, step.Description) || strings.Contains(system, step.SystemRules) {
						t.Fatal("task or rules have the wrong prompt authority")
					}
					if effects == 0 {
						want := agentTaskDescription(step)
						if contract == "" {
							want += "\n\n## Current instruction\n" + step.Items[0].Message
						}
						wantHistory := 0
						if pending {
							// Caller instructions arrive in the same first message as the task.
							want = agentTaskDescription(step) + "\n\n## Instructions for this run\nApply the task to the supplied scope once."
						}
						if user != want || len(history) != wantHistory || vars["PendingInvocationInput"] != "" {
							t.Fatalf("opening message=%q, want alone %q", user, want)
						}
						effects++
						if err := os.WriteFile(output, []byte(`{}`), 0600); err != nil {
							t.Fatal(err)
						}
					} else {
						if len(history) < 2 {
							t.Fatal("follow-up lost the original conversation")
						}
						if strings.Contains(user, "Do NOT just reply") {
							repairs++
							if err := os.WriteFile(output, []byte(`{"ready":true}`), 0600); err != nil {
								t.Fatal(err)
							}
						} else if user != "Check the evidence without repeating the action." && user != "## Retained task context\n## Rules\n"+step.SystemRules+"\n\n## Current instruction\nCheck the evidence without repeating the action." {
							t.Fatalf("unexpected follow-up %q", user)
						}
					}
					calls++
					history = append(history, llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, user), llmtypes.TextParts(llmtypes.ChatMessageTypeAI, "Proof recorded."))
					return "Proof recorded.", history, nil
				}
				delegation := &agentSequenceDelegation{CreateAgent: func(context.Context) (agents.OrchestratorAgent, error) { return model, nil }, TurnVars: func(item AgentItem, message string, _ bool) map[string]string {
					return c.buildAgentTemplateVars(t.Context(), step, item, 0, "proof", message, nil, nil, AgentWriteAccess{})
				}}
				var execCtx *ExecutionContext
				if pending {
					execCtx = &ExecutionContext{WorkshopHumanInput: "Apply the task to the supplied scope once."}
				}
				_, history, err := c.executeAgentStep(t.Context(), agentSequenceForDelegation(step, step.Items), 0, "proof", nil, execCtx, nil, agentSequenceCallOptions{Delegation: delegation})
				if err != nil {
					t.Fatal(err)
				}
				if effects != 1 || repairs != 1 || len(history) != calls*2 {
					t.Fatalf("effects=%d repairs=%d history=%d", effects, repairs, len(history))
				}
				wantCalls := 2
				if followup {
					wantCalls++
				}
				if calls != wantCalls {
					t.Fatalf("calls=%d want=%d", calls, wantCalls)
				}
				// Resume through the route executor with the starting task snapshot.
				// A later plan edit must neither replay the opener nor change the
				// binding policies of this already-running conversation.
				if contract == AgentDescriptionFirstPromptContract && scenario == "no-followup" {
					// Simulate an older saved conversation: rules were only in its
					// system, while the opening user turn held the bare description.
					history = append([]llmtypes.MessageContent(nil), history...)
					history[0] = llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, step.Description)
				}
				pinned := *step
				session := &agentSequenceSession{
					PromptContract: contract, SystemRules: step.SystemRules,
					initialStep: &pinned, StepID: step.ID, SessionID: step.ID,
					ConversationHistory: history, ExecutionTurnCount: calls,
					runtime: &agentSequenceRuntime{Agent: model},
				}
				key := c.msgSeqRouteKey("proof", step.ID)
				c.storeMsgSeqRouteSession(key, session)
				t.Cleanup(func() {
					c.clearMsgSeqRouteSession(key)
					c.closeAgentRuntime(session, "test completed")
				})
				edited := *step
				edited.Description = "Repeat the action under a changed task."
				edited.SystemRules = "Permit repeated actions."
				_, resumed, err := c.executeAgentStep(t.Context(), &edited, 0, "proof", nil, nil, nil, agentSequenceCallOptions{
					Source: "orchestrator_reentry", ContinuationMessage: "Check the evidence without repeating the action.",
				})
				if err != nil {
					t.Fatal(err)
				}
				if effects != 1 || repairs != 1 || calls != wantCalls+1 || len(resumed) != calls*2 {
					t.Fatalf("reentry replayed or lost history: effects=%d repairs=%d calls=%d history=%d", effects, repairs, calls, len(resumed))
				}
				if contract == AgentDescriptionFirstPromptContract && scenario == "no-followup" {
					// Follow the actual routed executor rather than calling the item
					// queue directly. Its turns must remain beneath the registered
					// call, including continuation and final-validation notifications.
					notifier := &recordingExecutionNotifier{}
					c.workshopExecutionNotifier = notifier
					routed := edited
					routed.PredefinedRoutes = nil
					ctx := virtualtools.WithBackgroundAgentID(t.Context(), "registered-route-call")
					ctx = context.WithValue(ctx, orchEvents.ParentExecutionIDKey, "registered-route-call")
					_, _, err := c.executeRoutedSubAgentStep(ctx, &routed, "Check the evidence without repeating the action.", 0, "proof", nil, nil, nil, nil)
					if err != nil {
						t.Fatal(err)
					}
					if len(notifier.starts) != 2 || effects != 1 {
						t.Fatalf("routed capture or action changed: starts=%d effects=%d", len(notifier.starts), effects)
					}
					for _, start := range notifier.starts {
						if start.ParentExecutionID != "registered-route-call" {
							t.Fatalf("captured routed turn points to an unregistered parent: %q", start.ParentExecutionID)
						}
					}
				}
			})
		}
	}
}

func TestDescriptionFirstContractStampAndAuthoring(t *testing.T) {
	legacy := &AgentPlanStep{CommonStepFields: CommonStepFields{ID: "task", Title: "Task", Description: "Do task."}, Items: []AgentItem{{ID: "work", Type: "user_message", Message: "Do task."}}}
	plan := &PlanningResponse{Steps: []PlanStepInterface{legacy}}
	raw, _ := json.Marshal(plan)
	if ValidateAgentDescriptionFirstContractStamp(string(raw)) == nil {
		t.Fatal("stamp accepted an unreviewed agent")
	}
	updated := mergePartialStepUpdate(legacy, PartialPlanStep{Items: []AgentItem{}, PromptContract: ptrDescriptionContract(AgentDescriptionFirstPromptContract), SystemRules: ptrDescriptionContract("No second send.")}).(*AgentPlanStep)
	plan.Steps[0] = updated
	raw, _ = json.Marshal(plan)
	if err := ValidateAgentDescriptionFirstContractStamp(string(raw)); err != nil {
		t.Fatal(err)
	}
	updated.Items = []AgentItem{{ID: "verify", Type: "user_message", Message: "Re-open the authoritative evidence and repair any gap."}}
	raw, _ = json.Marshal(plan)
	if err := ValidateAgentDescriptionFirstContractStamp(string(raw)); err == nil || !strings.Contains(err.Error(), "retained_reason") {
		t.Fatalf("expected retained_reason review error, got %v", err)
	}
	updated.Items[0].RetainedReason = "Evidence-only confirmation; report ambiguity, never repeat the greeting."
	updated.Items[0].Message = "Read the saved greeting receipt and report gaps without greeting again."
	raw, _ = json.Marshal(plan)
	if err := ValidateAgentDescriptionFirstContractStamp(string(raw)); err != nil {
		t.Fatal(err)
	}
	updated.Items = nil
	raw, _ = json.Marshal(plan)
	var decoded PlanningResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded.Steps[0].(*AgentPlanStep); got.PromptContract != AgentDescriptionFirstPromptContract || got.SystemRules != updated.SystemRules || len(got.Items) != 0 {
		t.Fatal("saved contract lost")
	}
	// Routes now contain scripted helpers, not conversational agents (PLAT-861-262).
	for _, schema := range []string{getAddAgentStepSchema(), getUpdateAgentStepSchema()} {
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(schema), &parsed); err != nil {
			t.Fatal(err)
		}
		properties := parsed["properties"].(map[string]interface{})
		for _, name := range []string{"prompt_contract", "system_rules", "initial_write_access"} {
			if _, ok := properties[name]; !ok {
				t.Fatalf("authoring schema omitted top-level %s", name)
			}
		}
	}
}
func ptrDescriptionContract(s string) *string { return &s }

// Preserve saved policy exactly once, including markdown examples, without
// changing optional items or granting access through prose.
func TestTaskRulesPreservedOnceInDescriptionAndContinuation(t *testing.T) {
	description := "## Goal\nProduce the report.\n\n## Rules\nNo paid calls.\n\n## Done when\nEvidence is saved."
	step := &AgentPlanStep{CommonStepFields: CommonStepFields{Description: description}, SystemRules: "No paid calls.\n\nUse verified sources."}
	task := agentTaskDescription(step)
	if strings.Count(task, "No paid calls.") != 1 || strings.Count(task, "Use verified sources.") != 1 || strings.Index(task, "Use verified sources.") > strings.Index(task, "## Done when") {
		t.Fatal(task)
	}
	normalizeAgentTaskRules(step)
	if step.SystemRules != "" || step.Description != task || agentTaskDescription(step) != task {
		t.Fatal("normalization lost or repeated rules")
	}
	legacy := &AgentPlanStep{CommonStepFields: CommonStepFields{Description: description}, SystemRules: "Use verified sources."}
	history := []llmtypes.MessageContent{llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, description)}
	missing := missingAgentTaskContext(legacy, history)
	if missing != "## Rules\nUse verified sources." {
		t.Fatalf("old continuation context = %q", missing)
	}
	history = append(history, llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, missing))
	if got := missingAgentTaskContext(legacy, history); got != "" {
		t.Fatalf("continuation repeated context: %q", got)
	}
	// An example's heading is not the actual Rules section.
	fenced := "## Goal\nExample:\n````md\n## Rules\n```\n````\n\n## Rules\nNo paid calls."
	if got := mergeTaskRules(fenced, "Use verified sources."); !strings.HasSuffix(got, "No paid calls.\n\nUse verified sources.\n") {
		t.Fatal(got)
	}
	renderer := &WorkflowExecutionOnlyAgent{}
	delegated := renderer.executionOnlyUserMessageProcessor(map[string]string{"StepDescription": task + "\n\n## Orchestrator Instructions\n\nCheck the selected record."})
	if !strings.Contains(delegated, "Use verified sources.") || !strings.Contains(delegated, "Check the selected record.") {
		t.Fatal("child lost its own task or caller input")
	}
}

// A later turn of a live runtime keeps the prompt it started with; a first turn
// or a rebuilt runtime (no instructions yet) gets the rendered prompt.
func TestSystemPromptForTurnKeepsTheStartedPrompt(t *testing.T) {
	history := []llmtypes.MessageContent{llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "hi")}
	if got := systemPromptForTurn("started", history, "rendered"); got != "" {
		t.Fatalf("turn 2 of a live runtime re-applied %q", got)
	}
	if got := systemPromptForTurn("", history, "rendered"); got != "rendered" {
		t.Fatalf("a rebuilt runtime must get the full prompt, got %q", got)
	}
	if got := systemPromptForTurn("started", nil, "rendered"); got != "rendered" {
		t.Fatalf("the first turn must apply the prompt, got %q", got)
	}
}
