package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
)

// PLAT-871-15: moving timer storage must retain the caller, message sequence,
// selected route and dependency, without interpreting old timer arrays.
func TestCommonProjectScheduleStorePreservesWorkAndCaller(t *testing.T) {
	_, _ = newScheduleRunWorkspaceStub(t)
	ctx := context.Background()
	root := "Workflow/example"
	record := projectactions.Schedule{ID: "daily", Name: "Daily", Project: projectactions.Project{Kind: "goal", ID: "example"},
		When:   projectactions.When{Cron: "0 9 * * *", Timezone: "UTC", After: &projectactions.After{Schedules: []string{"collect"}, Outcome: "any_terminal", DelayMinutes: 5, Deadline: "12:00"}},
		Action: projectactions.Action{Type: "message"}, RunAs: "owner", Enabled: false,
		Input: json.RawMessage(`{"messages":["Collect evidence.","Check it.","Report it."],"groups":[],"routes":{"router":"report"}}`)}
	if err := writeProjectSchedules(ctx, root, []projectactions.Schedule{record}); err != nil {
		t.Fatal(err)
	}
	manifest := &WorkflowManifest{ID: "example", Access: &WorkflowAccess{Owners: []string{"owner"}},
		Schedules: []WorkflowSchedule{{ID: "retired-array", Enabled: true}, {ID: "hook", ScheduleType: "webhook"}}}
	if err := loadWorkflowTimerActions(ctx, root, manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Schedules) != 2 || manifest.Schedules[0].ID != "hook" {
		t.Fatalf("retired timer or missing hook: %+v", manifest.Schedules)
	}
	got := manifest.Schedules[1]
	if got.ID != "daily" || got.Enabled || got.RunAs != "owner" || len(got.Messages) != 3 || got.Messages[2] != "Report it." || got.RouteSelections["router"] != "report" || got.DependencyDeadline != "12:00" || got.AfterDelayMinutes != 5 {
		t.Fatalf("saved work changed: %+v", got)
	}
	if err := saveWorkflowTimerActions(ctx, root, manifest); err != nil {
		t.Fatal(err)
	}
	if err := loadWorkflowTimerActions(ctx, root, manifest); err != nil {
		t.Fatal(err)
	}
	// PLAT-746-265: persistence must not turn "select no work" into the
	// omitted selection that automatically chooses enabled input sets.
	if manifest.Schedules[1].GroupNames == nil || len(manifest.Schedules[1].GroupNames) != 0 {
		t.Fatal("empty selection became automatic selection")
	}
	// Saved-work actions retain a chat too, even with no message queue.
	record.Action = projectactions.Action{Type: "run", Tab: "reporting"}
	record.Input = json.RawMessage(`{"groups":null}`)
	if err := writeProjectSchedules(ctx, root, []projectactions.Schedule{record}); err != nil {
		t.Fatal(err)
	}
	if err := loadWorkflowTimerActions(ctx, root, manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Schedules[1].ChatID != "reporting" {
		t.Fatal("run lost target chat")
	}
	if err := saveWorkflowTimerActions(ctx, root, manifest); err != nil {
		t.Fatal(err)
	}
	saved, err := readProjectSchedules(ctx, root)
	if err != nil || len(saved) != 1 || saved[0].Action.Tab != "reporting" {
		t.Fatal("saved run lost chat", err)
	}
	manifest.Access.Owners = []string{"another-owner"}
	if err := loadWorkflowTimerActions(ctx, root, manifest); err == nil {
		t.Fatal("saved caller gained another owner's authority")
	}
	crewRoot := "Crew/example-example"
	record.Input = json.RawMessage(`{"messages":["Collect evidence.","Check it.","Report it."]}`)
	record.Action.Type = "message"
	record.Project.Kind = "crew"
	record.When.After = nil
	record.Action.Tab, record.Action.Conversation = "example:chat:research", "isolated"
	if err := writeProjectSchedules(ctx, crewRoot, []projectactions.Schedule{record}); err != nil {
		t.Fatal(err)
	}
	crew := productProjectManifest{ID: "example", OwnerID: "owner"}
	if err := loadCrewTimerActions(ctx, crewRoot, &crew); err != nil {
		t.Fatal(err)
	}
	if len(crew.Schedules) != 1 || !crew.Schedules[0].Isolated || crew.Schedules[0].ChatKey != record.Action.Tab || len(crew.Schedules[0].Messages) != 3 {
		t.Fatalf("Crew chat destination changed: %+v", crew.Schedules)
	}
}

// PLAT-871-14/15: all project clocks skip the sleep/offline gap and may start
// only the newest occurrence inside the two-minute allowance.
func TestSharedProjectClockSkipsMissedCrewOccurrences(t *testing.T) {
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	job := productScheduleJob{ProjectID: "example", Profile: agentprofiles.Profile{ID: "work"},
		Schedule: productschedule.Schedule{ID: "daily", Enabled: true, CronExpression: "0 9 * * *", Timezone: "UTC", CreatedAt: day.Format(time.RFC3339)}}
	late := projectTimerDecision(job, day.Add(10*time.Hour))
	if late.Run || !late.ScheduledFor.Equal(day.Add(9*time.Hour)) {
		t.Fatalf("missed occurrence replayed or cursor lost: %+v", late)
	}
	nextDay := day.Add(24 * time.Hour)
	job.State.LastAttemptAt = late.ScheduledFor.Format(time.RFC3339)
	ontime := projectTimerDecision(job, nextDay.Add(9*time.Hour+time.Minute))
	if !ontime.Run || !ontime.ScheduledFor.Equal(nextDay.Add(9*time.Hour)) {
		t.Fatalf("next on-time occurrence failed: %+v", ontime)
	}
	job.State.LastAttemptAt = ontime.ScheduledFor.Format(time.RFC3339)
	if d := projectTimerDecision(job, nextDay.Add(9*time.Hour+90*time.Second)); d.Run {
		t.Fatal("an already accepted occurrence replayed")
	}
}

// PLAT-871-15: schedules.json is not authority to become another account.
// Check an edited file against real owner resolution, including empty manifests.
func TestProjectScheduleCallerCannotBeChangedByEditingFiles(t *testing.T) {
	_, _ = newScheduleRunWorkspaceStub(t)
	registry := newTestRegistry(t)
	previous := defaultProjectOwnersOverride
	defaultProjectOwnersOverride = registry
	t.Cleanup(func() {
		defaultProjectOwnersOverride = previous
		crewOwners.mu.Lock()
		crewOwners.entries = map[string]crewOwnerEntry{}
		crewOwners.mu.Unlock()
	})
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: "example-example", OwnerID: "owner", Shared: true}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, kind, runAs       string
		multi, noOwner, allowed bool
	}{
		{"goal owner", "goal", "owner", true, false, true},
		{"goal foreign account", "goal", "other", true, false, false},
		{"goal no owner", "goal", "other", true, true, false},
		{"relay owner", "relay", "owner", true, false, true},
		{"relay foreign account", "relay", "other", true, false, false},
		{"relay no owner", "relay", "other", true, true, false},
		{"local fallback", "goal", GetDefaultUserID(), false, true, true},
		{"local foreign account", "goal", "other", false, true, false},
		{"crew local fallback", "local-crew", GetDefaultUserID(), false, true, true},
		{"crew local foreign account", "local-crew", "other", false, true, false},
		{"crew registered owner", "crew", "owner", true, true, true},
		{"crew foreign account", "crew", "other", true, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.multi {
				t.Setenv("MULTI_USER_MODE", "true")
			} else {
				t.Setenv("MULTI_USER_MODE", "false")
			}
			root := "Workflow/example"
			if test.kind == "crew" || test.kind == "local-crew" {
				root = "Crew/example-example"
			}
			recordKind := test.kind
			if recordKind == "local-crew" {
				recordKind = "crew"
				root = "Crew/local-local"
			}
			record := projectactions.Schedule{ID: "daily", Name: "Daily", Project: projectactions.Project{Kind: recordKind, ID: "example"}, When: projectactions.When{Cron: "0 9 * * *", Timezone: "UTC"}, Action: projectactions.Action{Type: "run"}, RunAs: test.runAs, Input: json.RawMessage(`{}`)}
			if err := writeProjectSchedules(context.Background(), root, []projectactions.Schedule{record}); err != nil {
				t.Fatal(err)
			}
			var err error
			if recordKind == "crew" {
				err = loadCrewTimerActions(context.Background(), root, &productProjectManifest{ID: "example"})
			} else {
				m := &WorkflowManifest{ID: "example", Kind: test.kind}
				if !test.noOwner {
					m.Access = &WorkflowAccess{Owners: []string{"owner"}}
				}
				err = loadWorkflowTimerActions(context.Background(), root, m)
			}
			if (err == nil) != test.allowed {
				t.Fatalf("caller %s: allowed=%v: %v", test.runAs, test.allowed, err)
			}
		})
	}
}

// PLAT-871-15: the shared file conversion must retain the real saved Goal
// step binding, typed arguments/caller list and encrypted webhook settings.
func TestProjectEntryFilesPreserveContractsAndEncryptedAuthentication(t *testing.T) {
	_, _ = newScheduleRunWorkspaceStub(t)
	ctx := context.Background()
	root := "Workflow/example"
	manifest := &WorkflowManifest{ID: "example", CreatedBy: "owner", Schedules: []WorkflowSchedule{
		{ChatID: "reporting", ID: "typed-entry", Name: "Inspect", Enabled: true, ScheduleType: "webhook", Kind: triggerKindFunction, Function: &WorkflowFunctionSpec{Name: "inspect", Inputs: []WorkflowFunctionInput{{Name: "query", Type: "string", Required: true, Enum: []string{"one", "two"}}}, AllowedCallers: []triggerCaller{{Type: triggerCallerWorkflow, ID: "source"}}}, GroupNames: []string{"default", "other"}, RouteSelections: map[string]string{"router": "report"}, Webhook: &WorkflowWebhookConfig{StepID: "inspect-step"}},
		{ChatID: "main", ID: "event", Name: "Event", Enabled: true, ScheduleType: "webhook", Webhook: &WorkflowWebhookConfig{AuthMode: "github", EncryptedSecret: "opaque-fixture", InputMode: "envelope", AllowedVariables: []string{"query"}}},
	}}
	if err := saveWorkflowEntryActions(ctx, root, manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Schedules = nil
	if err := loadWorkflowEntryActions(ctx, root, manifest); err != nil {
		t.Fatal(err)
	}
	fn, err := findWorkflowFunctionTrigger(manifest, "inspect")
	if err != nil {
		t.Fatal(err)
	}
	if fn.ChatID != "reporting" || fn.ID != "typed-entry" || fn.Webhook.StepID != "inspect-step" || fn.RouteSelections["router"] != "report" || !workflowFunctionCallerAllowed(fn.Function, triggerCaller{Type: triggerCallerWorkflow, ID: "source"}) || workflowFunctionCallerAllowed(fn.Function, triggerCaller{Type: triggerCallerWorkflow, ID: "other"}) {
		t.Fatalf("function contract changed: %+v", fn)
	}
	if _, _, err := workflowFunctionArgs(*fn, map[string]interface{}{}); err == nil {
		t.Fatal("required function argument lost")
	}
	variables, group, err := workflowFunctionArgs(*fn, map[string]interface{}{"query": "two", "group": "other"})
	if err != nil || variables["query"] != "two" || group != "other" {
		t.Fatalf("typed execution input changed: %v %s %v", variables, group, err)
	}
	hooks, err := readProjectWebhookDefinitions(ctx, root)
	if err != nil || len(hooks) != 1 || hooks[0].Path != "/api/hooks/event" || hooks[0].Auth.EncryptedSecret != "opaque-fixture" || hooks[0].Auth.Mode != "github" || hooks[0].InputMapping.Mode != "envelope" {
		t.Fatalf("webhook settings changed: %+v %v", hooks, err)
	}
	manifest.CreatedBy = "other"
	if err := loadWorkflowEntryActions(ctx, root, manifest); err == nil {
		t.Fatal("a webhook file supplied another owner's authority")
	}
}
