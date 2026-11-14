package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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

func TestWorkflowProjectCallOwnersLegacyLocalOnly(t *testing.T) {
	for _, tc := range []struct {
		name     string
		multi    bool
		manifest *WorkflowManifest
		want     string
	}{
		{"local legacy", false, &WorkflowManifest{}, "local-account"},
		{"server legacy", true, &WorkflowManifest{}, ""},
		{"local explicit owner", false, &WorkflowManifest{Access: &WorkflowAccess{Owners: []string{"other"}}}, "other"},
		{"local creator", false, &WorkflowManifest{CreatedBy: "creator"}, "creator"},
		{"local explicit empty access", false, &WorkflowManifest{Access: &WorkflowAccess{}}, ""},
		{"missing manifest", false, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MULTI_USER_MODE", "false")
			if tc.multi {
				t.Setenv("MULTI_USER_MODE", "true")
			}
			t.Setenv("DEFAULT_USER_ID", "local-account")
			owners := workflowProjectCallOwners(tc.manifest)
			if tc.want == "" && len(owners) != 0 || tc.want != "" && (len(owners) != 1 || owners[0] != tc.want) {
				t.Fatalf("owners = %v, want %q", owners, tc.want)
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

// Declared functions keep their own tab and owner for every caller; they never
// enter a person's main chat by inheriting the old implicit-binding routing.
func TestDeclaredCrewCallsKeepFunctionTabAcrossOwners(t *testing.T) {
	for _, tc := range []struct {
		name, source, target string
	}{
		{"same owner", linkAlphaPath, "Beta"},
		{"different source owner even when actor owns target", linkGammaPath, "Beta"},
		{"cross owner", linkAlphaPath, "Gamma"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
			env := newCrewFunctionEnv(t)
			ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
			// Links belong to the actual caller, including another person's source.
			env.mock.mu.Lock()
			env.mock.files[tc.source+"/workflow.json"] = `{"workflow_context_paths":["` + linkBetaPath + `"]}`
			env.mock.mu.Unlock()
			caller, err := crewTriggerLinkCaller(tc.source)(ctx)
			if err != nil {
				t.Fatal(err)
			}
			target, err := resolveTriggerTarget(ctx, &UserClaims{UserID: "owner"}, tc.target)
			if err != nil {
				t.Fatal(err)
			}
			targetCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: target.CrewOwner})
			fn := savedFunctionForTest(t, targetCtx, target, crewFunction{Name: "routing_task", Description: "Routing task", Instructions: "Complete the task."})
			call, err := env.api.startCrewFunctionCall(ctx, "owner", caller, target, fn, nil, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			defer call.finish("completed", "fixture", "")
			env.svc.mu.Lock()
			defer env.svc.mu.Unlock()
			for _, queue := range env.svc.queued {
				for _, item := range queue {
					if item.options.RunID == call.RunID {
						if item.job.UserID != target.CrewOwner || !item.job.RunAsOwner || item.job.ProjectCaller != nil {
							t.Fatalf("function lost target owner authority: user=%q owner=%q run_as_owner=%v project_caller=%+v", item.job.UserID, target.CrewOwner, item.job.RunAsOwner, item.job.ProjectCaller)
						}
						if item.job.Schedule.Isolated || item.job.Schedule.ChatKey != target.CrewID+codeChatSideMarker+crewFunctionTabPrefix+"routing-task" {
							t.Fatalf("function entered the wrong conversation: isolated=%v chat=%q", item.job.Schedule.Isolated, item.job.Schedule.ChatKey)
						}
						return
					}
				}
			}
			t.Fatal("declared call did not queue a turn")
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

// A workflow Crew step that names an agent talks to that agent's own chat in
// the same owner's Crew; the same name always reaches the same chat (PLAT-861-261).
