package server

import "testing"

func TestRegisterWorkScheduleToolsReadsPairedUsersProjectManifest(t *testing.T) {
	workspace, docs := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	const userID = "paired-owner"
	const publicPath = "Chats/Work/projects/agentworks-6068cfc3"
	docs.files["_users/"+userID+"/"+publicPath+"/product.json"] = `{"schema_version":1,"product":"work","id":"6068cfc3-039a-4b85-8ade-f8c655000701","title":"Agentworks"}`

	api := &StreamingAPI{productSchedules: &ProductScheduleService{}}
	registrar := &recordingRegistrar{}
	if err := api.registerWorkScheduleTools(registrar, "work", userID, publicPath, false); err != nil {
		t.Fatalf("register Work tools from public conversation path: %v", err)
	}
	if _, ok := registrar.tools["list_project_schedules"]; !ok {
		t.Fatal("project schedule tools were not registered")
	}

	other := &recordingRegistrar{}
	if err := api.registerWorkScheduleTools(other, "work", "another-user", publicPath, false); err == nil {
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
	if err := api.registerWorkScheduleTools(registrar, "code", owner, publicPath, false); err != nil {
		t.Fatalf("register Code schedule tools: %v", err)
	}
	for _, name := range []string{"list_project_schedules", "create_project_schedule", "create_project_trigger"} {
		if _, ok := registrar.tools[name]; !ok {
			t.Fatalf("%s was not registered for a Code", name)
		}
	}
	if err := api.registerWorkScheduleTools(&recordingRegistrar{}, "work", owner, publicPath, false); err == nil {
		t.Fatal("a Code manifest authorized the Crew profile's tools")
	}
	if err := api.registerWorkScheduleTools(&recordingRegistrar{}, "code", "editor", publicPath, false); err == nil {
		t.Fatal("another person's Code manifest authorized schedule tools")
	}
}
