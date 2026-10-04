package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRelayLegacyTimersAreIgnoredWithoutChangingReleaseFiles(t *testing.T) {
	f := newExternalRelayFixture(t)
	var manifest WorkflowManifest
	if err := json.Unmarshal([]byte(f.mock.files["Workflow/invoices/workflow.json"]), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Schedules[0].AfterScheduleID = "timer"
	timer := WorkflowSchedule{ID: "timer", Enabled: true, ScheduleType: "cron", CronExpression: "* * * * *", GroupNames: []string{"default"}}
	calendar := WorkflowSchedule{ID: "calendar", Enabled: true, ScheduleType: "calendar", CalendarItems: []CalendarScheduleItem{{Date: "2030-01-01", Time: "12:00"}}, GroupNames: []string{"default"}}
	manifest.Schedules = append(manifest.Schedules, timer, calendar)
	raw, _ := json.Marshal(manifest)
	for _, workspace := range []string{"Workflow/invoices", "Workflow/invoices/.relay_releases/v1"} {
		file := workspace + "/workflow.json"
		f.mock.files[file] = string(raw)
		got, found, err := ReadWorkflowManifest(context.Background(), workspace)
		if err != nil || !found {
			t.Fatalf("read %s: found=%v err=%v", workspace, found, err)
		}
		if len(got.Schedules) != 1 || !got.Schedules[0].IsFunctionTrigger() || len(scheduleDependencyIDs(got.Schedules[0])) != 0 {
			t.Fatalf("timers or their dependency survived: %+v", got.Schedules)
		}
		if f.mock.files[file] != string(raw) {
			t.Fatal("reading the Relay rewrote its saved file")
		}
		if workspace == "Workflow/invoices" {
			if err := WriteWorkflowManifest(context.Background(), workspace, got); err != nil {
				t.Fatal(err)
			}
			var saved WorkflowManifest
			if err := json.Unmarshal([]byte(f.mock.files[file]), &saved); err != nil {
				t.Fatal(err)
			}
			if len(saved.Schedules) != 1 || !saved.Schedules[0].IsFunctionTrigger() {
				t.Fatal("next draft save retained retired timers")
			}
			got.Schedules = append(got.Schedules, timer)
			if err := WriteWorkflowManifest(context.Background(), workspace, got); err == nil || !strings.Contains(err.Error(), "API function triggers only") {
				t.Fatalf("new Relay timer was saved: %v", err)
			}
		}
	}
	// Ordinary workflows retain their timer schedules.
	manifest.Kind = ""
	manifest.RelayOutputStepID = ""
	raw, _ = json.Marshal(manifest)
	f.mock.files["Workflow/invoices/workflow.json"] = string(raw)
	got, _, err := ReadWorkflowManifest(context.Background(), "Workflow/invoices")
	if err != nil || len(got.Schedules) != 3 {
		t.Fatalf("ordinary schedules changed: %+v, %v", got, err)
	}
}

func TestRelaySchedulerRegistersNoTimersButKeepsFunctionDelivery(t *testing.T) {
	f := newExternalRelayFixture(t)
	svc := NewSchedulerService(f.api)
	manifest, _, err := ReadWorkflowManifest(context.Background(), "Workflow/invoices")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"cron", "calendar", ""} {
		timer := WorkflowSchedule{ID: "timer", Enabled: true, ScheduleType: kind, CronExpression: "* * * * *", CalendarItems: []CalendarScheduleItem{{Date: "2030-01-01", Time: "12:00"}}, GroupNames: []string{"default"}}
		sctx := buildScheduleContext("Workflow/invoices", manifest, timer)
		key := scheduleRuntimeKey(sctx)
		svc.jobs[key] = &registeredJob{sctx: sctx}
		if err := svc.LoadSchedule(sctx); err != nil {
			t.Fatal(err)
		}
		if len(svc.jobs) != 0 {
			t.Fatal("Relay timer registered")
		}
		svc.triggerSchedule(sctx, time.Now())
		if len(svc.runtimeStates) != 0 {
			t.Fatal("Relay timer created runtime state")
		}
	}
	functionContext := buildScheduleContext("Workflow/invoices", manifest, manifest.Schedules[0])
	if err := svc.LoadSchedule(functionContext); err != nil {
		t.Fatal(err)
	}
	if svc.GetWorkspaceForSchedule("process") != "Workflow/invoices" || len(svc.jobs) != 0 {
		t.Fatal("API function registration changed")
	}
	manifest.Kind = ""
	ordinary := buildScheduleContext("Workflow/invoices", manifest, WorkflowSchedule{ID: "normal", Enabled: true, ScheduleType: "cron", CronExpression: "0 * * * *", GroupNames: []string{"default"}})
	if err := svc.LoadSchedule(ordinary); err != nil {
		t.Fatal(err)
	}
	if len(svc.jobs) != 1 {
		t.Fatal("ordinary workflow timer not registered")
	}
}

func TestRelayExternalScheduleToolsAreRefused(t *testing.T) {
	f := newExternalRelayFixture(t)
	_, raw := f.token(t, "owner", []string{"workflows:read", "files:read", "runs:execute"}, true)
	for _, name := range []string{"list_schedules", "get_schedule_runs", "trigger_schedule"} {
		args := map[string]any{"workflow_id": "invoices"}
		if name != "list_schedules" {
			args["schedule_id"] = "process"
		}
		body := f.relayCall(t, raw, name, args, 400)
		if body["error"].(map[string]any)["code"] != "relay_api_only" {
			t.Fatalf("%s: %v", name, body)
		}
	}
}
