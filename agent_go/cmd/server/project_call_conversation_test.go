package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	productschedule "github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

func TestProjectsShareAnyOwner(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b []string
		want bool
	}{
		{"same", []string{"alice"}, []string{"alice"}, true},
		{"second owner", []string{"alice", "bob"}, []string{"carol", "bob"}, true},
		{"different", []string{"alice"}, []string{"bob"}, false},
		{"missing", nil, []string{"alice"}, false},
		{"empty IDs", []string{" "}, []string{""}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := projectsShareOwner(tc.a, tc.b); got != tc.want {
				t.Fatalf("share owner = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestQueuedProjectCallRejectsChangedOwners(t *testing.T) {
	env := newTriggerLinkEnv(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	caller, _ := workflowTriggerLinkCaller("Workflow/reports")(ctx)
	job := productScheduleJob{UserID: "owner", WorkspacePath: linkBetaPath,
		ProjectCaller: &caller.Stamp, ProjectCallerPath: caller.Path,
		Schedule: productschedule.Schedule{Isolated: false}}
	if err := env.svc.validateProjectCallConversation(ctx, job); err != nil {
		t.Fatal(err)
	}
	manifest, _, _ := ReadWorkflowManifest(ctx, caller.Path)
	manifest.Access.Owners = []string{"other"}
	raw, _ := json.Marshal(manifest)
	env.mock.mu.Lock()
	env.mock.files[manifestPath(caller.Path)] = string(raw)
	env.mock.mu.Unlock()
	if err := env.svc.validateProjectCallConversation(ctx, job); err == nil {
		t.Fatal("queued main-chat call retained revoked shared ownership")
	}
}

func TestWorkflowAskSharedOwnerUsesMainChat(t *testing.T) {
	env := newCrewFunctionEnv(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	manifest, _, _ := ReadWorkflowManifest(ctx, "Workflow/reports")
	manifest.Access.Owners = []string{"other", "owner"}
	raw, _ := json.Marshal(manifest)
	env.mock.mu.Lock()
	env.mock.files[manifestPath("Workflow/reports")] = string(raw)
	for _, saved := range []struct{ id, user, date string }{
		{"main-reports", "owner", "2026-10-01T00:00:00Z"},
		{"other-owners-private-chat", "other", "2026-10-05T00:00:00Z"},
		{"wfask-previous-isolated", "owner", "2026-10-05T01:00:00Z"},
		{"schedule-cron--briefing_1", "owner", "2026-10-05T02:00:00Z"},
	} {
		content, _ := json.Marshal(builderConversationLog{SessionID: saved.id, UserID: saved.user, PhaseID: "workflow-builder", UpdatedAt: saved.date,
			ConversationHistory: []builderConversationMessage{{Role: "human", Parts: []builderConversationPart{{Text: "Previous main chat"}}}}})
		env.mock.files[workflowBuilderConversationLogPath("Workflow/reports", saved.id, time.Now())] = string(content)
	}
	env.mock.mu.Unlock()
	caller, err := crewTriggerLinkCaller(linkAlphaPath)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	previous := workflowAskTurn
	workflowAskTurn = func(_ *StreamingAPI, _ context.Context, req map[string]interface{}, sessionID, userID string) (internalSessionTurnResult, error) {
		if sessionID != "main-reports" || userID != "owner" || req["restored_conversation_session_id"] != "main-reports" || req["pin_run_mode"] != true {
			t.Fatalf("wrong main chat/principal/capabilities: session=%s user=%s req=%v", sessionID, userID, req)
		}
		return internalSessionTurnResult{FinalResponse: "Answer"}, nil
	}
	t.Cleanup(func() { workflowAskTurn = previous })
	call := &crewFunctionCall{ID: "fn-test-main", UserID: "owner", poll: time.Second, done: make(chan struct{})}
	env.api.runWorkflowAsk(call, triggerTarget{Kind: triggerCallerWorkflow, Path: "Workflow/reports", Manifest: manifest}, caller, "Question", time.Minute)
	if call.Status != "completed" || call.RunID != "main-reports" {
		t.Fatalf("call=%+v", call)
	}
}

func TestProjectCallsRouteByProjectOwners(t *testing.T) {
	for _, tc := range []struct {
		name, source, target string
		workflow             bool
		owners               []string
		isolated             bool
	}{
		{"Crew same owner", linkAlphaPath, "Beta", false, nil, false},
		{"Crew different owner even when actor owns target", linkGammaPath, "Beta", false, nil, true},
		{"Workflow same owner", "Workflow/reports", "Beta", true, []string{"owner"}, false},
		{"Workflow shared second owner", "Workflow/reports", "Beta", true, []string{"other", "owner"}, false},
		{"Workflow different owner even when actor owns target", "Workflow/reports", "Beta", true, []string{"other"}, true},
		{"Crew cross owner", linkAlphaPath, "Gamma", false, nil, true},
		{"Code same owner", "_users/owner/Chats/Code/projects/source", "Beta", false, nil, false},
		{"Code cross owner", "_users/owner/Chats/Code/projects/source", "Gamma", false, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newTriggerLinkEnv(t)
			ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
			if tc.workflow {
				manifest, _, _ := ReadWorkflowManifest(ctx, tc.source)
				manifest.Access = &WorkflowAccess{Owners: tc.owners, Editors: []string{"owner"}}
				raw, _ := json.Marshal(manifest)
				env.mock.mu.Lock()
				env.mock.files[manifestPath(tc.source)] = string(raw)
				env.mock.mu.Unlock()
			} else if tc.source == "_users/owner/Chats/Code/projects/source" {
				if err := env.svc.registry.RegisterProfile(agentprofiles.Profile{ID: "code", Name: "Code", Version: 1, BuiltIn: true, Product: "code", SystemPromptTemplate: "hi",
					Runtime: agentprofiles.RuntimePolicy{Conversation: agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject},
						Workspace: agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, Root: "Chats", ProjectsRoot: "Chats/Code/projects"}},
					Features: []agentprofiles.FeatureBinding{{ID: "triggers"}}, UIPanels: agentprofiles.UIPanels{Schedules: true}}); err != nil {
					t.Fatal(err)
				}
				env.mock.mu.Lock()
				env.mock.files[tc.source+"/product.json"] = `{"schema_version":1,"product":"code","id":"source","title":"Private source","session_id":"code-source"}`
				env.mock.mu.Unlock()
			}
			resolve := crewTriggerLinkCaller(tc.source)
			if tc.workflow {
				resolve = workflowTriggerLinkCaller(tc.source)
			}
			caller, err := resolve(ctx)
			if err != nil {
				t.Fatal(err)
			}
			target, err := resolveTriggerTarget(ctx, &UserClaims{UserID: "owner"}, tc.target)
			if err != nil {
				t.Fatal(err)
			}
			triggerID, _, err := env.api.connectTriggerTarget(ctx, "owner", caller, target)
			if err != nil {
				t.Fatal(err)
			}
			delivery, err := env.api.dispatchTargetTrigger(ctx, "owner", caller, target, triggerID, "test-routing", "test", map[string]interface{}{"task": "test"})
			if err != nil {
				t.Fatal(err)
			}
			env.svc.mu.Lock()
			defer env.svc.mu.Unlock()
			for _, queue := range env.svc.queued {
				for _, item := range queue {
					if item.options.RunID == delivery.RunID {
						if item.job.Schedule.Isolated != tc.isolated {
							t.Fatalf("isolated = %v, want %v", item.job.Schedule.Isolated, tc.isolated)
						}
						return
					}
				}
			}
			t.Fatal("delivery did not queue a turn")
		})
	}
}

func TestProjectCallerOwnersRejectMismatchedPath(t *testing.T) {
	env := newTriggerLinkEnv(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	for _, caller := range []triggerLinkCaller{
		{Stamp: triggerCaller{Type: triggerCallerCrew, ID: "gamma", ProfileID: "work"}, Path: linkAlphaPath},
		{Stamp: triggerCaller{Type: triggerCallerWorkflow, ID: "not-reports"}, Path: "Workflow/reports"},
	} {
		if owners := env.svc.projectCallerOwners(ctx, "owner", caller); len(owners) != 0 {
			t.Fatalf("mismatched source granted main chat: %v", owners)
		}
	}
}
