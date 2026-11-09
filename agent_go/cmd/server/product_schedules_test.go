package server

import (
	"context"
	"errors"
	"fmt"
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

// A new Crew message schedule that names no place gets its own chat (owner, 9 Nov); a chosen chat, a tab or a
// workflow timer keeps what was asked. The parser itself still reads an empty value as the chat.
func TestRunDestinationParsingAndNewCrewMessageScheduleGetsItsOwnChat(t *testing.T) {
	for _, test := range []struct {
		workflow, destination, chatID string
		own                           bool
	}{{"", "", "", true}, {"", runDestinationChat, "", false}, {"", "", "tab1", false}, {"report", "", "", false}} {
		if got := crewMessageScheduleDefaultsToOwnChat(crewProfileID, "team", test.workflow, test.destination, test.chatID); got != test.own {
			t.Fatalf("workflow=%q destination=%q chat=%q own chat = %v, want %v", test.workflow, test.destination, test.chatID, got, test.own)
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
				select {
				case <-started:
				case <-time.After(3 * time.Second):
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
			case <-time.After(3 * time.Second):
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
		return internalSessionTurnResult{Response: "calendar completed"}, nil
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
	case <-time.After(3 * time.Second):
		t.Fatal("resume did not execute the retained occurrence")
	}
	deadline := time.Now().Add(3 * time.Second)
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
