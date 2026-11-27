package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

func TestProductScheduleJobIDRoundTrip(t *testing.T) {
	id := productScheduleJobID("example-product", "daily-checkin")
	if id != "product:example-product:daily-checkin" {
		t.Fatalf("id = %q", id)
	}
	profile, sched, ok := parseProductScheduleJobID(id)
	if !ok || profile != "example-product" || sched != "daily-checkin" {
		t.Fatalf("parse = %q %q %v", profile, sched, ok)
	}
	for _, bad := range []string{"daily-checkin", "product:", "product:x", "product:x:"} {
		if _, _, ok := parseProductScheduleJobID(bad); ok {
			t.Fatalf("%q should not parse", bad)
		}
	}
}

func TestProjectScheduleJobIDRoundTrip(t *testing.T) {
	id := projectScheduleJobID("work", "project-1", "daily-checkin")
	if id != "product-project:work:project-1:daily-checkin" {
		t.Fatalf("id = %q", id)
	}
	profile, project, sched, ok := parseProjectScheduleJobID(id)
	if !ok || profile != "work" || project != "project-1" || sched != "daily-checkin" {
		t.Fatalf("parse = %q %q %q %v", profile, project, sched, ok)
	}
	for _, bad := range []string{"daily-checkin", "product-project:", "product-project:work:project-1", "product-project:work::daily"} {
		if _, _, _, ok := parseProjectScheduleJobID(bad); ok {
			t.Fatalf("%q should not parse", bad)
		}
	}
}

// A new Crew schedule that names no place gets its own chat (owner, 9 Nov; workflow timers too, 25 Nov #4046); a
// chosen chat or a tab keeps what was asked, and "main" is no stand-in for Main. The parser still reads an empty value
// as the chat.
func TestRunDestinationParsingAndNewCrewScheduleGetsItsOwnChat(t *testing.T) {
	for _, test := range []struct {
		destination, chatID string
		own                 bool
	}{{"", "", true}, {runDestinationChat, "", false}, {"", "tab1", false}} {
		if got := crewScheduleDefaultsToOwnChat(crewProfileID, "team", test.destination, test.chatID); got != test.own {
			t.Fatalf("destination=%q chat=%q own chat = %v, want %v", test.destination, test.chatID, got, test.own)
		}
	}
	if _, err := projectScheduleChatKey(context.Background(), "owner", crewProfileID, "team", "main"); err == nil || err.Error() != scheduleChooseAgentMessage {
		t.Fatalf(`chat_id "main" = %v, want the choose-an-agent refusal`, err)
	}
	// The one-off move: a stored schedule with no agent (empty, or the project key that meant Main) gets its own chat;
	// one on an agent, one with its own chat and a Watcher stay as they are.
	for _, test := range []struct {
		action projectactions.Action
		input  string
		move   bool
	}{
		{projectactions.Action{Type: "message"}, `{"messages":["hi"]}`, true},
		{projectactions.Action{Type: "run", WorkflowID: "wf", Tab: "main"}, `{}`, true},
		// A short tab id (Main's own id) names an agent: it stays (a server listed every such timer as one to move).
		{projectactions.Action{Type: "run", WorkflowID: "wf", Tab: "imported-abc123"}, `{}`, false},
		{projectactions.Action{Type: "message", Tab: "team:chat:abc"}, `{}`, false},
		{projectactions.Action{Type: "message", Conversation: runDestinationIsolated}, `{}`, false},
		{projectactions.Action{Type: "message"}, `{"condition":{"check_mode":"schedule"}}`, false},
	} {
		if got := crewScheduleNamesNoAgent(projectactions.Schedule{Action: test.action, Input: json.RawMessage(test.input)}); got != test.move {
			t.Fatalf("%+v %s: move = %v, want %v", test.action, test.input, got, test.move)
		}
	}
	for _, test := range []struct {
		value    string
		isolated bool
		fresh    bool
	}{{"", false, false}, {runDestinationChat, false, false}, {runDestinationIsolated, true, false}, {runDestinationNew, true, true}} {
		got, err := parseRunDestination(test.value)
		if err != nil || got.Isolated != test.isolated || got.New != test.fresh {
			t.Fatalf("destination %q = %+v, %v", test.value, got, err)
		}
		if got.value() != firstNonEmptyTrimmed(test.value, runDestinationChat) {
			t.Fatalf("destination %q is shown as %q", test.value, got.value())
		}
	}
	if _, err := parseRunDestination("new_chat"); err == nil {
		t.Fatal("invalid destination accepted")
	}
	for _, id := range runDestinationEnum() {
		if _, err := parseRunDestination(id); err != nil {
			t.Fatalf("the shared list offers %q but the parser refuses it: %v", id, err)
		}
	}
}

func TestUsersWithProductFollowsDirectoryAccess(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_ADMIN_ONLY_PRODUCT_SURFACES", "work")
	withMemoryUserDirectory(t, `{"users":[
		{"id":"admin1","username":"admin","admin":true,"can_create":true,"products":[]},
		{"id":"member1","username":"m","can_create":true,"products":[]},
		{"id":"scoped1","username":"s","can_create":true,"products":["finance"]},
		{"id":"reader1","username":"r","can_create":false,"products":[]},
		{"id":"off1","username":"o","admin":true,"disabled":true}
	]}`)
	got := usersWithProduct("example-product")
	if strings.Join(got, ",") != "admin1,member1" {
		t.Fatalf("example-product users = %v", got)
	}
	if got := usersWithProduct("finance"); strings.Join(got, ",") != "admin1,member1,scoped1" {
		t.Fatalf("finance users = %v", got)
	}
	if got := usersWithProduct("work"); strings.Join(got, ",") != "admin1" {
		t.Fatalf("admin-only Work users = %v", got)
	}
}

func newProductScheduleTestService(t *testing.T, users []string) (*ProjectActionDispatcher, map[string]string) {
	t.Helper()
	registry := agentprofiles.NewRegistry()
	profile := agentprofiles.Profile{
		ID: "family", Name: "Family", Version: 1, SystemPromptTemplate: "hi", BuiltIn: true, Product: "family",
		Runtime: agentprofiles.RuntimePolicy{
			Transport:    "auto",
			Conversation: agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeSingleton},
			Workspace:    agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeFixed, Root: "Chats"},
		},
		Schedules: []productschedule.Schedule{
			{ID: "checkin", Name: "Check-in", Enabled: true, CronExpression: "0 8 * * *", Timezone: "UTC", Messages: []string{"review", "summarize"}},
			{ID: "weekly", Name: "Weekly", Enabled: false, CronExpression: "0 9 * * 1", Timezone: "UTC", Messages: []string{"weekly"}},
		},
	}
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	svc := NewProjectActionDispatcher(nil, registry)
	svc.users = func(string) []string { return users }
	svc.readFile = func(_ context.Context, path string) (string, bool, error) {
		c, ok := files[path]
		return c, ok, nil
	}
	svc.writeFile = func(_ context.Context, path, content string) error {
		files[path] = content
		return nil
	}
	return svc, files
}

func TestProductScheduleJobsForUserAndEnableOverride(t *testing.T) {
	svc, files := newProductScheduleTestService(t, []string{"u1"})
	ctx := context.Background()
	jobs, err := svc.JobsForUser(ctx, "u1")
	if err != nil || len(jobs) != 2 {
		t.Fatalf("jobs = %d err = %v", len(jobs), err)
	}
	if _, err := svc.JobsForUser(ctx, "u2"); err != nil {
		t.Fatal(err)
	} else if j, _ := svc.JobsForUser(ctx, "u2"); len(j) != 0 {
		t.Fatalf("a user without the product must see no jobs: %d", len(j))
	}

	job, err := svc.SetEnabled(ctx, "u1", "product:family:checkin", false)
	if err != nil {
		t.Fatal(err)
	}
	if job.Effective().Enabled {
		t.Fatal("override should disable the schedule")
	}
	if !strings.Contains(files[productScheduleStatePath("u1")], `"enabled": false`) {
		t.Fatalf("override not persisted: %s", files[productScheduleStatePath("u1")])
	}
	job, err = svc.SetEnabled(ctx, "u1", "product:family:weekly", true)
	if err != nil || !job.Effective().Enabled {
		t.Fatalf("override should enable the product-disabled schedule: %+v %v", job, err)
	}
	if _, err := svc.Job(ctx, "u1", "product:family:nope"); err == nil {
		t.Fatal("unknown schedule must not resolve")
	}
}

func TestProductScheduleJobResponseShape(t *testing.T) {
	svc, _ := newProductScheduleTestService(t, []string{"u1"})
	job, err := svc.Job(context.Background(), "u1", "product:family:checkin")
	if err != nil {
		t.Fatal(err)
	}
	job.State.LastRunAt = time.Now().Add(-30 * time.Hour).UTC().Format(time.RFC3339)
	job.State.LastStatus = "success"
	resp := svc.jobResponse(job, "_users/u1/Chats")
	if resp.EntityType != "product" || resp.ID != "product:family:checkin" || resp.WorkflowLabel != "Family" || len(resp.Messages) != 2 || !resp.Enabled {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.LastRunAt == nil || resp.NextRunAt == nil || !resp.NextRunAt.After(*resp.LastRunAt) {
		t.Fatalf("last/next run not populated: %+v", resp)
	}
	if resp.LastStatus != "success" {
		t.Fatalf("last status = %q", resp.LastStatus)
	}

	// A one-time schedule is shown as a calendar entry with its next run, and has none once it ran.
	at := time.Now().Add(3 * time.Hour).UTC()
	once := job
	once.Schedule.CronExpression, once.Schedule.CadenceHours = "", 0
	once.Schedule.RunAt, once.Schedule.Timezone = at.Format(time.RFC3339), "Asia/Kolkata"
	once.State.LastRunAt = ""
	resp = svc.jobResponse(once, "_users/u1/Chats")
	local := at.In(time.FixedZone("IST", 5*3600+1800))
	if resp.ScheduleType != "calendar" || len(resp.CalendarItems) != 1 || resp.CalendarItems[0].Date != local.Format("2006-01-02") ||
		resp.CalendarItems[0].Time != local.Format("15:04") || resp.NextRunAt == nil || !resp.NextRunAt.Equal(at.Truncate(time.Second)) {
		t.Fatalf("one-time response = %+v", resp)
	}
	once.State.LastRunAt = time.Now().UTC().Format(time.RFC3339)
	if resp = svc.jobResponse(once, "_users/u1/Chats"); resp.NextRunAt != nil {
		t.Fatalf("a one-time schedule that ran still shows a next run: %+v", resp)
	}
}

func TestAgentProfileSchedulesRequireSingletonConversation(t *testing.T) {
	profile := agentprofiles.Profile{
		ID: "p", Name: "P", Version: 1, SystemPromptTemplate: "hi", BuiltIn: true,
		Runtime:   agentprofiles.RuntimePolicy{Transport: "auto", Conversation: agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject}},
		Schedules: []productschedule.Schedule{{ID: "a", Name: "A", CronExpression: "0 8 * * *", Messages: []string{"m"}}},
	}
	if err := agentprofiles.Validate(profile); err == nil || !strings.Contains(err.Error(), "singleton") {
		t.Fatalf("keyed profiles must reject schedules, got %v", err)
	}
}

// PLAT-871-33: actual Crew binding/workspace HTTP and SQLite occurrence evidence.
// Only the provider turn is replaced. Stop/setup errors cannot strand a run,
// and a second independent Crew chat must not acquire a new scope-wide lease.
func TestCrewTimerLedgerRecordsTerminalOutcomesWithoutSecondScopeLease(t *testing.T) {
	for _, outcome := range []string{"completed", "failed", "stopped", "setup_failed", "ledger_unavailable", "manual_clock_disabled"} {
		t.Run(outcome, func(t *testing.T) {
			docs := t.TempDir()
			t.Setenv("WORKSPACE_DOCS_PATH", docs)
			env := newTriggerLinkEnv(t)
			env.svc.heldConversation = nil
			triggerSource := "cron"
			if outcome == "manual_clock_disabled" {
				outcome, triggerSource = "completed", "manual"
				t.Setenv("SCHEDULER_ENABLED", "false")
				// Existing work belongs to the primary machine. Opening the
				// shared ledger on a secondary must not mark it interrupted.
				primary, err := schedulerstate.Open(filepath.Join(docs, "_system/schedule-state.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = primary.Close() })
				if err := primary.RecordAdmittedRun(t.Context(), schedulerstate.Run{RunID: "primary-active", ScopeType: "crew", ScopeID: "primary", ScheduleID: "primary-timer", TriggerSource: "cron"}); err != nil {
					t.Fatal(err)
				}
			}
			// Production startup opens evidence even when it does not Start
			// the automatic clock. No test-injected store is used here.
			if err := env.api.scheduler.initializeRunLedger(); err != nil {
				t.Fatal(err)
			}
			store := env.api.scheduler.stateStore
			t.Cleanup(func() { _ = store.Close() })
			if triggerSource == "manual" {
				primary, err := store.GetRun(t.Context(), "primary-active")
				if err != nil || primary.State != schedulerstate.StateStarting {
					t.Fatalf("opening ledger interrupted primary: %+v, %v", primary, err)
				}
			}
			if outcome == "ledger_unavailable" {
				env.api.scheduler.stateStore = nil
			}
			profile, err := env.svc.registry.Resolve("work", 0, "owner")
			if err != nil {
				t.Fatal(err)
			}
			job := productScheduleJob{UserID: "owner", Profile: profile, ProjectID: "beta", ProjectTitle: "Beta", WorkspacePath: linkBetaPath,
				Schedule: productschedule.Schedule{ID: "first", Name: "First", Enabled: true, CronExpression: "* * * * *", Timezone: "UTC", Messages: []string{"do work"}, Isolated: true}}
			if outcome == "setup_failed" {
				job.Profile.Runtime.Conversation.Mode = ""
			}
			started := make(chan struct{}, 2)
			release := make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			var calls atomic.Int32
			env.svc.automationTurnRunner = func(ctx context.Context, _ map[string]interface{}, _, _ string) (internalSessionTurnResult, error) {
				if calls.Add(1) == 1 {
					started <- struct{}{}
					select {
					case <-release:
					case <-ctx.Done():
						return internalSessionTurnResult{}, ctx.Err()
					}
					if err := ctx.Err(); err != nil {
						return internalSessionTurnResult{}, err
					}
					if outcome == "failed" {
						return internalSessionTurnResult{}, fmt.Errorf("provider failed")
					}
				}
				return internalSessionTurnResult{FinalResponse: "Done"}, nil
			}
			ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner"})
			occurrence := time.Now().UTC().Truncate(time.Minute)
			done := make(chan error, 1)
			go func() { _, err := env.svc.Run(ctx, job, triggerSource, occurrence); done <- err }()
			if outcome != "setup_failed" && outcome != "ledger_unavailable" {
				// An upper bound only: on a busy build host the setup took over 3 s (#3324). A run that ends
				// without starting reports its own error instead of a timeout.
				select {
				case <-started:
				case err := <-done:
					t.Fatalf("timer ended before reaching the provider boundary: %v", err)
				case <-time.After(15 * time.Second):
					t.Fatal("timer did not reach provider boundary")
				}
				when := projectactions.When{Timezone: "UTC", After: &projectactions.After{Schedules: []string{"first"}}}
				if state, _ := productschedule.EvaluateDependencies(ctx, store, "crew", "beta", when, occurrence, time.Now()); state != productschedule.DependencyWaiting {
					t.Fatalf("active prerequisite released dependent: %v", state)
				}
				if outcome == "completed" {
					second := job
					second.Schedule.ID, second.Schedule.Name = "second", "Second"
					if _, err := env.svc.Run(ctx, second, triggerSource, occurrence); err != nil {
						t.Fatalf("second independent Crew chat took a scope lease: %v", err)
					}
				}
				if outcome == "stopped" && !env.svc.Stop("owner", job.ID()) {
					t.Fatal("Stop did not find timer")
				}
				once.Do(func() { close(release) })
			}
			select {
			case err := <-done:
				if (err == nil) != (outcome == "completed") {
					t.Fatalf("outcome %s returned %v", outcome, err)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("timer did not settle")
			}
			decisions, err := store.ListFireDecisions(ctx, "crew", "beta", "first", 10)
			if outcome == "ledger_unavailable" {
				if err != nil || len(decisions) != 0 || calls.Load() != 0 {
					t.Fatalf("unavailable ledger started work/evidence: calls=%d decisions=%v err=%v", calls.Load(), decisions, err)
				}
				if _, running := env.svc.Running("owner", job.ID()); running {
					t.Fatal("unavailable ledger stranded dispatcher claim")
				}
				return
			}
			if err != nil || len(decisions) != 1 || decisions[0].Decision != "started" {
				t.Fatalf("admission evidence = %+v, %v", decisions, err)
			}
			saved, err := store.GetRun(ctx, decisions[0].RunID)
			want := schedulerstate.State(outcome)
			if outcome == "setup_failed" {
				want = schedulerstate.StateFailed
			}
			if err != nil || saved.State != want || saved.CompletedAt == nil {
				t.Fatalf("terminal evidence = %+v, %v", saved, err)
			}
			if err := store.RecordAdmittedRun(ctx, saved); !errors.Is(err, schedulerstate.ErrRunAlreadyActive) {
				t.Fatalf("same run ID re-admitted: %v", err)
			}
			receipts, err := store.ListFireDecisions(ctx, "crew", "beta", "first", 10)
			if err != nil || len(receipts) != 1 || receipts[0].RunID != saved.RunID {
				t.Fatalf("duplicate admission changed occurrence evidence: %v, %v", receipts, err)
			}

			if _, running := env.svc.Running("owner", job.ID()); running {
				t.Fatal("terminal timer retained dispatcher claim")
			}
			when := projectactions.When{Timezone: "UTC", After: &projectactions.After{Schedules: []string{"first"}}}
			state, reason := productschedule.EvaluateDependencies(ctx, store, "crew", "beta", when, occurrence, time.Now())
			wantDependency := productschedule.DependencyBlocked
			if outcome == "completed" {
				wantDependency = productschedule.DependencyReady
			}
			if state != wantDependency {
				t.Fatalf("dependent decision = %v (%s), want %v", state, reason, wantDependency)
			}
			if outcome != "setup_failed" {
				runs, err := ReadScheduleRuns(ctx, agentProfileRuntimeWorkspace("owner", linkBetaPath))
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, projected := range runs {
					if projected.ID == saved.RunID {
						found = true
						status := "error"
						if outcome == "completed" {
							status = "success"
						} else if outcome == "stopped" {
							status = "stopped"
						}
						if projected.Status != status || projected.CompletedAt == nil {
							t.Fatalf("projection disagrees with ledger: %+v", projected)
						}
					}
				}
				if !found {
					t.Fatal("terminal ledger run absent from Crew run projection")
				}
			}
		})
	}
}

func TestCrewScheduleRejectsDependencyCycleWithoutSaving(t *testing.T) {
	// PLAT-871-43: saving B after A must refuse A -> B -> A and preserve B.
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env := newTriggerLinkEnv(t)
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	makeSchedule := func(id string) productschedule.Schedule {
		return productschedule.Schedule{ID: id, Name: id, CronExpression: "0 8 * * *", Timezone: "UTC", Messages: []string{"Check the saved report."}}
	}
	b, err := env.svc.CreateProjectSchedule(ctx, "owner", "work", "beta", makeSchedule("b"))
	if err != nil {
		t.Fatal(err)
	}
	a := makeSchedule("a")
	a.When = &projectactions.When{After: &projectactions.After{Schedules: []string{b.ID()}}}
	if _, err := env.svc.CreateProjectSchedule(ctx, "owner", "work", "beta", a); err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.UpdateProjectSchedule(ctx, "owner", b.ID(), func(schedule *productschedule.Schedule) {
		schedule.When = &projectactions.When{After: &projectactions.After{Schedules: []string{projectScheduleJobID("work", "beta", "a")}}}
	}); err == nil || !strings.Contains(err.Error(), "dependency cycle") {
		t.Fatalf("cyclic edit was not refused: %v", err)
	}
	saved, err := env.svc.Job(ctx, "owner", b.ID())
	if err != nil {
		t.Fatal(err)
	}
	if saved.Schedule.When != nil && saved.Schedule.When.After != nil && len(saved.Schedule.When.After.Schedules) != 0 {
		t.Fatal("refused dependency edit was persisted")
	}
	if _, err := env.svc.UpdateProjectSchedule(ctx, "owner", b.ID(), func(schedule *productschedule.Schedule) { schedule.Name = "Updated B" }); err != nil {
		t.Fatalf("acyclic edit was refused: %v", err)
	}
}

// PLAT-746-786: pausing after a real queue admission must not consume the
// calendar occurrence when the busy conversation releases or admission races.
// Native binding/workspace/ledger paths run; only the provider turn is replaced.
func TestPausedCalendarQueueRetainsOccurrenceUntilResume(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env := newTriggerLinkEnv(t)
	if err := env.api.scheduler.initializeRunLedger(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.api.scheduler.stateStore.Close() })
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	occurrence := time.Now().UTC().Add(time.Hour).Truncate(time.Minute)
	job, err := env.svc.CreateProjectSchedule(ctx, "owner", "work", "beta", productschedule.Schedule{
		ID: "one-time", Name: "One time", Enabled: true, RunAt: occurrence.Format(time.RFC3339), Timezone: "UTC", Messages: []string{"Return the saved result."},
	})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	called := make(chan struct{}, 1)
	env.svc.automationTurnRunner = func(context.Context, map[string]interface{}, string, string) (internalSessionTurnResult, error) {
		calls.Add(1)
		called <- struct{}{}
		return internalSessionTurnResult{FinalResponse: "calendar completed"}, nil
	}
	if _, err := env.svc.Run(ctx, job, "cron", occurrence); !errors.Is(err, ErrProductRunQueued) {
		t.Fatalf("busy timer was not accepted into the conversation queue: %v", err)
	}
	if err := SaveSchedulerConfig(ctx, &SchedulerConfig{GloballyPaused: true}); err != nil {
		t.Fatal(err)
	}
	env.svc.heldConversation = nil
	convKey := job.UserID + "\x1f" + conversationKeyForJob(job)
	env.svc.finishAutomationRun("finished-turn", convKey)
	assertRetained := func() {
		t.Helper()
		env.svc.mu.Lock()
		defer env.svc.mu.Unlock()
		pending := env.svc.queued[convKey]
		if len(pending) != 1 || !pending[0].scheduledFor.Equal(occurrence) || env.svc.conversations[convKey] || calls.Load() != 0 {
			t.Fatalf("pause consumed or started the calendar occurrence: pending=%+v calls=%d", pending, calls.Load())
		}
	}
	assertRetained()
	// Exercise the second boundary: pause begins after queue selection but
	// before actual admission. The popped occurrence must return to the queue.
	env.svc.mu.Lock()
	pending, ok := popProductQueueLocked(env.svc.queued, convKey, false)
	env.svc.mu.Unlock()
	if !ok {
		t.Fatal("accepted occurrence disappeared")
	}
	claim, err := env.svc.claimAutomationRun(ctx, pending.job, pending.triggerSource, pending.scheduledFor, pending.options, pending.onStarted)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.executeAutomationRun(claim.runCtx, claim.cancel, pending.job, pending.triggerSource, pending.scheduledFor, pending.options, claim.run, claim.jobKey, claim.convKey, pending.onStarted); !errors.Is(err, ErrProductRunQueued) {
		t.Fatalf("racing pause did not retain admitted queue item: %v", err)
	}
	assertRetained()
	if err := SaveSchedulerConfig(ctx, &SchedulerConfig{}); err != nil {
		t.Fatal(err)
	}
	env.svc.resumeQueuedRuns()
	env.svc.resumeQueuedRuns()
	select {
	case <-called:
	case <-time.After(15 * time.Second): // an upper bound for a busy build host (#3324)
		t.Fatal("resume did not execute the retained occurrence")
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, running := env.svc.Running(job.UserID, job.ID()); !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retained occurrence did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	store := env.api.scheduler.stateStore
	decisions, err := store.ListFireDecisions(ctx, "crew", "beta", job.Schedule.ID, 10)
	if err != nil || len(decisions) != 1 || !decisions[0].ScheduledFor.Equal(occurrence) || calls.Load() != 1 {
		t.Fatalf("resume lost or duplicated the accepted occurrence: %+v calls=%d error=%v", decisions, calls.Load(), err)
	}
	run, err := store.GetRun(ctx, decisions[0].RunID)
	if err != nil || run.State != schedulerstate.StateCompleted {
		t.Fatalf("retained occurrence lacks its terminal receipt: %+v %v", run, err)
	}
}

// A schedule made from a chat with no place named runs in that chat (owner, 22 Nov): the chat tools no longer turn
// an empty place into its own chat, and their help names the chat as the default.
func TestChatScheduleToolDefaultsToTheChatItWasMadeFrom(t *testing.T) {
	if !strings.Contains(messageScheduleDestinationHelp, `"chat" (the default)`) || strings.Contains(messageScheduleDestinationHelp, `"isolated" (the default)`) {
		t.Fatalf("help does not name the chat as the default: %s", messageScheduleDestinationHelp)
	}
	source, err := os.ReadFile("work_schedule_tools.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "crewScheduleDefaultsToOwnChat(") {
		t.Fatal("the chat schedule tool still gives a schedule with no place its own chat")
	}
}

// A schedule's own chat takes replies from its owner (owner, 22 Nov; replaces 10 Nov's read-only schedule chat).
func TestScheduleOwnChatRowIsWritableForItsOwner(t *testing.T) {
	source, err := os.ReadFile("agent_profile_side_chats.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	start := strings.Index(text, "func scheduleOwnChatRows(")
	if start < 0 {
		t.Fatal("scheduleOwnChatRows not found")
	}
	body := text[start:]
	if end := strings.Index(body, "\n}\n"); end > 0 {
		body = body[:end]
	}
	if !strings.Contains(body, "TabOwnerID: userID, CanWrite: true") || strings.Contains(body, "CanWrite: false") {
		t.Fatal("a schedule's own chat is not writable for its owner")
	}
}

func TestProjectScheduleUpdatePreservesGitHubConnectionAndUnmodelledManifestFields(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env := newTriggerLinkEnv(t)
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})

	manifestFile := linkBetaPath + "/workflow.json"
	env.mock.mu.Lock()
	env.mock.files[manifestFile] = `{
  "schema_version": 1,
  "id": "beta",
  "label": "Beta",
  "created_by": "owner",
  "remote_project_links": [{"id": "link-1"}],
  "capabilities": {
    "github_connection_id": "11111111111111111111111111111111",
    "slack_connection_id": "slack-bot-1",
    "gmail_connection_id": "gmail_001",
    "gmail_blocked_recipients": ["blocked@example.com"],
    "browser_cdp_port": 9222,
    "notifications": {"wake_tab": {"crew_id": "crew-1", "chat_id": "chat-1"}},
    "selected_secrets": ["CUSTOM_SECRET"],
    "workflow_context_paths": ["Workflow/legacy"]
  }
}`
	env.mock.mu.Unlock()

	created, err := env.svc.CreateProjectSchedule(ctx, "owner", "work", "beta", productschedule.Schedule{
		ID: "daily", Name: "Daily check", Enabled: true, CronExpression: "0 8 * * *", Timezone: "UTC", Messages: []string{"Run daily check."},
	})
	if err != nil {
		t.Fatalf("CreateProjectSchedule failed: %v", err)
	}
	if _, err := env.svc.UpdateProjectSchedule(ctx, "owner", created.ID(), func(s *productschedule.Schedule) {
		s.Enabled = false
	}); err != nil {
		t.Fatalf("UpdateProjectSchedule failed: %v", err)
	}
	if err := env.svc.DeleteProjectSchedule(ctx, "owner", created.ID()); err != nil {
		t.Fatalf("DeleteProjectSchedule failed: %v", err)
	}

	env.mock.mu.Lock()
	raw := env.mock.files[manifestFile]
	env.mock.mu.Unlock()

	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("unmarshal rewritten workflow.json: %v\n%s", err, raw)
	}
	if doc["created_by"] != "owner" {
		t.Fatalf("created_by lost after schedule update: %v", doc["created_by"])
	}
	if _, ok := doc["remote_project_links"]; !ok {
		t.Fatalf("unmodelled top-level remote_project_links lost: %s", raw)
	}
	if _, hasSchedules := doc["schedules"]; hasSchedules {
		t.Fatalf("schedules should remain out of workflow.json: %s", raw)
	}
	caps, _ := doc["capabilities"].(map[string]any)
	if caps == nil {
		t.Fatalf("capabilities missing after schedule update: %s", raw)
	}
	if caps["github_connection_id"] != "11111111111111111111111111111111" {
		t.Fatalf("github_connection_id = %v, want preserved", caps["github_connection_id"])
	}
	if caps["slack_connection_id"] != "slack-bot-1" {
		t.Fatalf("slack_connection_id = %v, want preserved", caps["slack_connection_id"])
	}
	if caps["gmail_connection_id"] != "gmail_001" {
		t.Fatalf("gmail_connection_id = %v, want preserved", caps["gmail_connection_id"])
	}
	if _, ok := caps["gmail_blocked_recipients"]; !ok {
		t.Fatalf("gmail_blocked_recipients lost: %s", raw)
	}
	if caps["browser_cdp_port"] != float64(9222) {
		t.Fatalf("browser_cdp_port = %v, want 9222", caps["browser_cdp_port"])
	}
	if _, ok := caps["notifications"]; !ok {
		t.Fatalf("notifications lost: %s", raw)
	}
	if _, ok := caps["selected_secrets"]; !ok {
		t.Fatalf("unmodelled capabilities.selected_secrets lost: %s", raw)
	}
	if _, hasLegacy := caps["workflow_context_paths"]; hasLegacy {
		t.Fatalf("legacy capabilities.workflow_context_paths should stay cleared: %s", raw)
	}
}
