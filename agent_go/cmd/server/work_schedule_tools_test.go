package server

import (
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestRegisterWorkScheduleToolsReadsPairedUsersProjectManifest(t *testing.T) {
	workspace, docs := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	const userID = "paired-owner"
	const publicPath = "Chats/Work/projects/agentworks-6068cfc3"
	docs.files["_users/"+userID+"/"+publicPath+"/product.json"] = `{"schema_version":1,"product":"work","id":"6068cfc3-039a-4b85-8ade-f8c655000701","title":"Agentworks"}`

	api := &StreamingAPI{productSchedules: &ProductScheduleService{}}
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

	api := &StreamingAPI{productSchedules: &ProductScheduleService{}}
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
