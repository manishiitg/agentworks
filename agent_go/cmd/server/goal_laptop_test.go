package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
)

func TestGoalBuilderLaptopUsesSharedGrantsOnlyInInteractiveTurns(t *testing.T) {
	f := newMCPLaptopFixture(t)
	const root = "Workflow/invoices"
	f.write(t, root+"/workflow.json", `{"schema_version":1,"id":"wf_invoices","label":"Invoices","capabilities":{},"access":{"owners":["owner"],"editors":["other"]}}`)
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	ctx := context.WithValue(t.Context(), UserContextKey, owner)
	settings := crewLaptopSettings{DeviceOwnerID: "owner", DeviceID: "laptop", Commands: true, Folders: []localfiles.FolderSelection{{ID: "project", Path: "/tmp/project", Access: "write"}}}
	file, err := laptopSettingsPath(owner.UserID, root)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(settings)
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, person := range []string{"owner", "other"} {
		request := profileRouteRequest(http.MethodGet, "/workflow/laptop?workspace_path=Workflow%2Finvoices", nil, person)
		response := httptest.NewRecorder()
		f.api.handleGoalLaptopSettings(response, request)
		if response.Code != 200 {
			t.Fatalf("%s shared Goal settings: %d %s", person, response.Code, response.Body.String())
		}
		if shared, err := laptopSettingsPath(person, root); err != nil || shared != file {
			t.Fatal("Goal Builders received separate settings", err)
		}
	}
	base := QueryRequest{AgentMode: "workflow_phase", PhaseID: "workflow-builder", SelectedFolder: root, userID: "owner"}
	admit := func(req QueryRequest) QueryRequest {
		t.Helper()
		profile, _, err := f.api.admitQueryTarget(ctx, &req, "owner", "goal-session")
		if err != nil || profile != nil {
			t.Fatalf("Goal admission: %+v %v", profile, err)
		}
		return req
	}
	req := admit(base)
	if req.LaptopConnection == nil || req.LaptopConnection.DeviceID != "laptop" {
		t.Fatal("interactive Goal did not load its saved computer")
	}
	f.api.lastQueryRequests = map[string]QueryRequest{"goal-session": req}
	reg := &recordingRegistrar{}
	if err := f.api.registerLaptopTools(reg, newProductToolGate(nil), owner, false, "goal-session", root, req.LaptopConnection); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"list_local_files", "write_local_file", "copy_local_file_to_project", "execute_local_shell_command"} {
		if _, ok := reg.tools[name]; !ok {
			t.Fatalf("Goal missing %s", name)
		}
	}
	setup := &laptopToolCallRegistrar{name: "manage_laptop_access"}
	if err := f.api.registerLaptopAccessChatTool(setup, newProductToolGate(nil), owner, false, "goal-session", root); err != nil || setup.execute == nil {
		t.Fatalf("Goal chat setup unavailable: %v", err)
	}
	if _, err := setup.execute(ctx, map[string]any{"action": "get"}); err != nil {
		t.Fatalf("Goal chat setup failed: %v", err)
	}
	for _, source := range []string{"step", "schedule", "autopilot", "run", "run_mode", "child", "connector"} {
		t.Run(source, func(t *testing.T) {
			next := base
			switch source {
			case "step":
				next.PhaseID = "execution"
			case "schedule":
				next.TriggeredBy = "schedule"
			case "autopilot":
			case "run":
				next.PinRunMode = true
			case "run_mode":
				next.ExecutionOptions = &ExecutionOptions{WorkshopMode: "run"}
			case "child":
				next.ParentSessionID = "parent"
			case "connector":
				next.BotPlatform = "slack"
			}
			// A browser payload cannot grant the saved device to automated work.
			next.LaptopConnection = req.LaptopConnection
			next = admit(next)
			if next.LaptopConnection != nil {
				t.Fatal("non-Builder work inherited a laptop")
			}
			f.api.lastQueryRequests["goal-session"] = next
			if f.api.laptopTurnAllowed("goal-session", owner.UserID, *req.LaptopConnection) {
				t.Fatal("retained local tool accepted a later automated turn")
			}
			if _, err := setup.execute(ctx, map[string]any{"action": "get"}); err == nil {
				t.Fatal("retained setup tool accepted automated work")
			}
		})
	}
	f.write(t, root+"/workflow.json", `{"schema_version":1,"id":"wf_invoices","label":"Invoices","capabilities":{},"access":{"owners":["owner"],"readers":["other"]}}`)
	response := httptest.NewRecorder()
	f.api.handleGoalLaptopSettings(response, profileRouteRequest(http.MethodGet, "/workflow/laptop?workspace_path=Workflow%2Finvoices", nil, "other"))
	if response.Code != 403 {
		t.Fatal("Run share retained Goal laptop settings", response.Code)
	}
}
