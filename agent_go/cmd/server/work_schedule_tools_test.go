package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestRegisterWorkScheduleToolsReadsPairedUsersProjectManifest(t *testing.T) {
	workspace, docs := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	const userID = "paired-owner"
	const publicPath = "Chats/Work/projects/agentworks-6068cfc3"
	docs.files["_users/"+userID+"/"+publicPath+"/product.json"] = `{"schema_version":1,"product":"work","id":"6068cfc3-039a-4b85-8ade-f8c655000701","title":"Agentworks"}`

	api := &StreamingAPI{projectActions: &ProjectActionDispatcher{}}
	registrar := &recordingRegistrar{}
	if err := api.registerWorkScheduleTools(registrar, "work", userID, "", publicPath, false); err != nil {
		t.Fatalf("register Work tools from public conversation path: %v", err)
	}
	if _, ok := registrar.tools["list_project_schedules"]; !ok {
		t.Fatal("project schedule tools were not registered")
	}

	other := &recordingRegistrar{}
	if err := api.registerWorkScheduleTools(other, "work", "another-user", "", publicPath, false); err == nil {
		t.Fatal("another user's project manifest must not authorize schedule tools")
	}
}

// A Code registers the same project schedule and trigger tools from its own
// manifest (product "code"): a Code turn no longer fails on the Crew-only
// check. A Code's manifest never authorizes the Crew profile's tools, and
// another person's tree does not authorize Code tools.
func TestRegisterScheduleToolsForACode(t *testing.T) {
	workspace, docs := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	const owner = "code-owner"
	const publicPath = "Chats/Code/projects/app-c0de0001"
	docs.files["_users/"+owner+"/"+publicPath+"/product.json"] = `{"schema_version":1,"product":"code","id":"c0de0001-0000-4000-8000-000000000001","title":"App"}`

	api := &StreamingAPI{projectActions: &ProjectActionDispatcher{}}
	registrar := &recordingRegistrar{}
	if err := api.registerWorkScheduleTools(registrar, "code", owner, "", publicPath, false); err != nil {
		t.Fatalf("register Code schedule tools: %v", err)
	}
	for _, name := range []string{"list_project_schedules", "create_project_schedule", "create_project_trigger"} {
		if _, ok := registrar.tools[name]; !ok {
			t.Fatalf("%s was not registered for a Code", name)
		}
	}
	if err := api.registerWorkScheduleTools(&recordingRegistrar{}, "work", owner, "", publicPath, false); err == nil {
		t.Fatal("a Code manifest authorized the Crew profile's tools")
	}
	if err := api.registerWorkScheduleTools(&recordingRegistrar{}, "code", "editor", "", publicPath, false); err == nil {
		t.Fatal("another person's Code manifest authorized schedule tools")
	}
}

func TestOneTimeRunAtFromToolArguments(t *testing.T) {
	now := time.Date(2026, 10, 5, 18, 0, 0, 0, time.UTC)
	got, err := oneTimeRunAt(map[string]interface{}{"in_minutes": float64(180)}, now)
	if err != nil || got != "2026-10-05T21:00:00Z" {
		t.Fatalf("in_minutes 180 = %q, %v", got, err)
	}
	// An offset is honoured and normalised to UTC: 23:30 +05:30 on the next day is 18:00 UTC then.
	if got, err := oneTimeRunAt(map[string]interface{}{"run_at": "2026-10-06T23:30:00+05:30"}, now); err != nil || got != "2026-10-06T18:00:00Z" {
		t.Fatalf("run_at with an offset = %q, %v", got, err)
	}
	for _, bad := range []map[string]interface{}{
		{"run_at": "2026-10-05T17:00:00Z"},                           // past
		{"run_at": "tomorrow"},                                       // not RFC3339
		{"in_minutes": float64(0)},                                   // too small
		{"in_minutes": float64(5), "run_at": "2026-12-01T00:00:00Z"}, // both
	} {
		if _, err := oneTimeRunAt(bad, now); err == nil {
			t.Fatalf("should be refused: %v", bad)
		}
	}
	if got, err := oneTimeRunAt(map[string]interface{}{}, now); err != nil || got != "" {
		t.Fatalf("no one-time argument must mean recurring, got %q, %v", got, err)
	}
}

// Owner, 2026-10-07: a reminder set from a Code side chat (tab) runs in that
// chat and queues with it; anything else runs in the main chat.
func TestProjectScheduleRunsInItsSideChat(t *testing.T) {
	job := productScheduleJob{ProjectID: "proj1", Profile: agentprofiles.Profile{ID: "code"}}
	job.Schedule.ChatKey = "proj1:chat:abc"
	if got := scheduleChatKey(job); got != "proj1:chat:abc" {
		t.Fatalf("side chat key = %q", got)
	}
	if got := conversationKeyForJob(job); got != "conversation:code:proj1:chat:abc" {
		t.Fatalf("queue key = %q, want the side chat's own queue", got)
	}
	for _, foreign := range []string{"proj2:chat:abc", "proj1", "proj1:chat:", "proj1:chat:a:b", "other"} {
		job.Schedule.ChatKey = foreign
		if got := scheduleChatKey(job); got != "" {
			t.Errorf("chat key %q accepted as %q; only this project's side chats count", foreign, got)
		}
	}
	job.Schedule.ChatKey = ""
	if got := conversationKeyForJob(job); got != "conversation:code:proj1" {
		t.Fatalf("main chat queue key = %q", got)
	}
}

// Main is a role, so a schedule created there follows a later main change;
// a side-created schedule remains pinned to that physical chat.
func TestScheduleCreatedFromMigratedMainUsesTheMainAddress(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	ctx := internalBotRequestContext(t.Context(), "owner")
	profile, err := env.svc.registry.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	binding, owned, err := resolveConversationBindingForUser(ctx, "owner", profile, "beta")
	if err != nil || !owned {
		t.Fatalf("binding: %v %v", owned, err)
	}
	chat, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, "owner", profile, binding, "")
	if err != nil {
		t.Fatal(err)
	}
	reg := &recordingRegistrar{}
	if err := env.api.registerWorkScheduleTools(reg, "work", "owner", chat.SessionID, binding.WorkspacePath, false); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.tools["create_project_schedule"].exec(ctx, map[string]interface{}{"name": "Check", "message": "Check status", "in_minutes": float64(60)}); err != nil {
		t.Fatal(err)
	}
	jobs, err := env.svc.JobsForUser(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.ProjectID == "beta" && job.Schedule.Name == "Check" {
			if job.Schedule.ChatKey != "" {
				t.Fatalf("main schedule pinned to physical key %q", job.Schedule.ChatKey)
			}
			return
		}
	}
	t.Fatal("created schedule missing")
}

// PLAT-871: the automations tool's documented one-time forms on a Crew. calendar_items were dropped (400 "cron_expression,
// cadence_hours or run_at is required") and run_at was refused; both must save, come back as dates and reach the clock.
func TestCrewOneTimeScheduleFormsReachTheClock(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	crewOwnerTabsFixture(t, env)
	env.svc.users = func(string) []string { return []string{"owner"} }
	svc := &SchedulerService{api: env.api}
	ctx := internalBotRequestContext(t.Context(), "owner")
	at := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Minute)
	for name, timing := range map[string]map[string]any{
		"dated": {"schedule_type": "calendar", "calendar_items": []any{map[string]any{"date": at.Format("2006-01-02"), "time": at.Format("15:04")}}},
		"at":    {"run_at": at.Format(time.RFC3339)},
	} {
		body := map[string]any{"entity_type": "product", "product_profile_id": "work", "product_project_id": "beta",
			"name": name, "timezone": "UTC", "enabled": true, "messages": []any{"Send the report"}}
		for key, value := range timing {
			body[key] = value
		}
		encoded, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		createScheduledJobHandler(svc)(w, httptest.NewRequest(http.MethodPost, "/api/scheduler/jobs", strings.NewReader(string(encoded))).WithContext(ctx))
		if w.Code != http.StatusCreated {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
		jobs, err := env.svc.JobsForUser(ctx, "owner")
		if err != nil {
			t.Fatal(err)
		}
		var id string
		for _, job := range jobs {
			if job.Schedule.Name != name {
				continue
			}
			id = job.ID()
			if d := projectTimerDecision(job, at.Add(-time.Minute)); d.Run {
				t.Fatalf("%s ran early: %+v", name, d)
			}
			if d := projectTimerDecision(job, at.Add(time.Second)); !d.Run {
				t.Fatalf("%s did not come due: %+v", name, d)
			}
		}
		if id == "" {
			t.Fatalf("%s: schedule not found after saving", name)
		}
		// A new timing form replaces the saved dates.
		w = httptest.NewRecorder()
		update := httptest.NewRequest(http.MethodPut, "/api/scheduler/jobs/x", strings.NewReader(`{"cron_expression":"0 9 * * *"}`)).WithContext(ctx)
		updateScheduledJobHandler(svc)(w, mux.SetURLVars(update, map[string]string{"id": id}))
		if w.Code != http.StatusOK {
			t.Fatalf("%s to cron: %d %s", name, w.Code, w.Body)
		}
	}
	past := `{"entity_type":"product","product_profile_id":"work","product_project_id":"beta","name":"late","enabled":true,"messages":["x"],"run_at":"2020-01-01T00:00:00Z"}`
	w := httptest.NewRecorder()
	createScheduledJobHandler(svc)(w, httptest.NewRequest(http.MethodPost, "/api/scheduler/jobs", strings.NewReader(past)).WithContext(ctx))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("past run_at: %d %s", w.Code, w.Body)
	}
}

// PLAT-871-23: "in five minutes" must never be reported as "every five minutes".
func TestCreatedScheduleReplyDistinguishesRepeatFromOneTime(t *testing.T) {
	previousLocal := time.Local
	time.Local = time.FixedZone("server-local", 3600)
	t.Cleanup(func() { time.Local = previousLocal })
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	ctx := internalBotRequestContext(t.Context(), "owner")
	reg := &recordingRegistrar{}
	if err := env.api.registerWorkScheduleTools(reg, "work", "owner", "", linkBetaPath, false); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, summary string
		timing        map[string]interface{}
		repeats       bool
	}{
		{"Recurring check", "Repeats every 5 minutes", map[string]interface{}{"cron_expression": "*/5 * * * *", "timezone": "UTC"}, true},
		{"Daily check", "Repeats every day at 09:30", map[string]interface{}{"cron_expression": "30 9 * * *", "timezone": "Asia/Kolkata"}, true},
		{"Weekday check", "Repeats on weekdays at 09:30", map[string]interface{}{"cron_expression": "30 9 * * 1-5", "timezone": "Asia/Kolkata"}, true},
		{"One check without a zone", "One time at", map[string]interface{}{"in_minutes": float64(5)}, false},
		{"One check", "One time at", map[string]interface{}{"in_minutes": float64(5), "timezone": "Asia/Kolkata"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]interface{}{"name": tc.name, "message": "Check status"}
			for key, value := range tc.timing {
				args[key] = value
			}
			out, err := reg.tools["create_project_schedule"].exec(ctx, args)
			if err != nil {
				t.Fatal(err)
			}
			var reply struct {
				TimingSummary string `json:"timing_summary"`
			}
			if err := json.Unmarshal([]byte(out), &reply); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(reply.TimingSummary, tc.summary) || (!tc.repeats && !strings.Contains(reply.TimingSummary, "does not repeat")) {
				t.Fatalf("missing or incorrect saved timing summary: %q", reply.TimingSummary)
			}
			zone, _ := tc.timing["timezone"].(string)
			if zone == "" {
				zone = "UTC"
			}
			if !strings.Contains(reply.TimingSummary, "("+zone+")") {
				t.Fatalf("saved timing summary lost the chosen timezone: %q", reply.TimingSummary)
			}
			records, err := readProjectSchedules(ctx, linkBetaPath)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, record := range records {
				if record.Name != tc.name {
					continue
				}
				found = true
				if record.When.Timezone != zone {
					t.Fatalf("chosen timezone not saved: %+v", record.When)
				}
				if tc.repeats {
					if record.When.Cron != tc.timing["cron_expression"].(string) || len(record.When.Dates) != 0 {
						t.Fatalf("recurrence not saved: %+v", record.When)
					}
				} else {
					if record.When.Cron != "" || len(record.When.Dates) != 1 {
						t.Fatalf("one-time timing not saved: %+v", record.When)
					}
					date := record.When.Dates[0]
					if !strings.Contains(reply.TimingSummary, date.Date+" "+date.Time) {
						t.Fatalf("reply differs from saved local date: summary=%q saved=%+v", reply.TimingSummary, record.When)
					}
				}
			}
			if !found {
				t.Fatal("saved schedule missing")
			}
			listed, err := reg.tools["list_project_schedules"].exec(ctx, map[string]interface{}{})
			if err != nil {
				t.Fatal(err)
			}
			var page struct {
				Schedules []struct {
					Name          string `json:"name"`
					TimingSummary string `json:"timing_summary"`
				} `json:"schedules"`
			}
			if err := json.Unmarshal([]byte(listed), &page); err != nil {
				t.Fatal(err)
			}
			matched := false
			for _, job := range page.Schedules {
				if job.Name == tc.name {
					matched = true
					if job.TimingSummary != reply.TimingSummary {
						t.Fatalf("saved list timing differs from creation: list=%q created=%q", job.TimingSummary, reply.TimingSummary)
					}
				}
			}
			if !matched {
				t.Fatal("saved schedule absent from list")
			}
		})
	}
}
