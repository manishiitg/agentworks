package server

import (
	"strings"
	"testing"
	"time"
)

// Whoever starts a run gets its result: a chat that starts a run with run-now is
// resumed once with the terminal outcome, a failure arrives as a failure, and a
// chat that is also the schedule's own destination is not told a second time.
func TestRunNowResumesTheCallingChatOnceWithTheOutcome(t *testing.T) {
	api := lifecycleTestAPI()
	// The real completion loop would consume the channel this test reads.
	api.completionLoopStarted = map[string]bool{"chat-1": true}
	api.activeSessions = map[string]*ActiveSessionInfo{"chat-1": {SessionID: "chat-1", UserID: "owner"}}
	svc := &SchedulerService{api: api}
	ch := api.bgAgentRegistry.GetNotificationChannel("chat-1")

	run := func(destination, status, errMsg string) (delivered bool, agent *BackgroundAgent) {
		sctx := &ScheduleContext{WorkspacePath: "Workflow/example", OwnerUserID: "owner", Schedule: WorkflowSchedule{ID: "daily", Name: "Daily"}}
		svc.rememberScheduleCaller(sctx, "chat-1")
		if sctx.CallerSessionID != "chat-1" {
			t.Fatal("an active chat that asked for the run was not remembered")
		}
		sctx.ChatDestinationSessionID = destination
		runID := "run-" + status + destination
		notice := svc.beginCallerNotice(sctx, runID)
		if notice == nil {
			t.Fatal("the calling chat was not given a pending result")
		}
		notice.finish(sctx, status, errMsg, "iteration-1")
		notice.finish(sctx, status, errMsg, "iteration-1") // a second finish must not deliver again
		// Delivery can be asynchronous: wait for an expected one, and give an
		// unexpected one time to show.
		wait := 200 * time.Millisecond
		if destination == "" {
			wait = 2 * time.Second
		}
		select {
		case id := <-ch:
			if id != "schedule-run-"+runID {
				t.Fatalf("completion id = %q", id)
			}
			delivered = true
		case <-time.After(wait):
		}
		select {
		case <-ch:
			t.Fatal("the calling chat was told twice")
		case <-time.After(200 * time.Millisecond):
		}
		return delivered, api.bgAgentRegistry.Get("chat-1", "schedule-run-"+runID)
	}

	if delivered, agent := run("", "success", ""); !delivered || !strings.Contains(agent.GetSnapshot().Result, "finished: success") {
		t.Fatalf("success was not delivered with its outcome: delivered=%v", delivered)
	}
	if delivered, agent := run("", "error", "step failed"); !delivered || !strings.Contains(agent.GetSnapshot().Error, "step failed") {
		t.Fatalf("failure was not delivered as a failure: delivered=%v", delivered)
	}
	if delivered, _ := run("chat-1", "success", ""); delivered {
		t.Fatal("a chat that already receives the schedule's result in its own tab was told twice")
	}

	// Cron has no caller, and a scheduled session is never a caller.
	sctx := &ScheduleContext{Schedule: WorkflowSchedule{ID: "cron"}}
	svc.rememberScheduleCaller(sctx, "")
	svc.rememberScheduleCaller(sctx, "schedule-manual--abc_1")
	if sctx.CallerSessionID != "" || svc.beginCallerNotice(sctx, "run-cron") != nil {
		t.Fatal("a run with no calling chat was given a result to deliver")
	}
}
