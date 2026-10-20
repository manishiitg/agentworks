package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
)

func TestLaptopSettingsOwnerAndOfflineDowngrade(t *testing.T) {
	f := newMCPLaptopFixture(t)
	api := f.api
	const root = "Crew/dashboard-team"
	request := func(method, owner, body string) *http.Request {
		return mux.SetURLVars(profileRouteRequest(method, "/laptop", []byte(body), owner), map[string]string{"id": "work", "project_id": "dashboard-team"})
	}
	for _, user := range []string{"owner", "other"} {
		response := httptest.NewRecorder()
		api.handleCrewLaptopSettings(response, request(http.MethodGet, user, ""))
		if user == "owner" && response.Code != 200 || user != "owner" && response.Code != 403 {
			t.Fatalf("%s read: %d %s", user, response.Code, response.Body.String())
		}
	}
	file, err := laptopSettingsPath("owner", root)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	settings := crewLaptopSettings{DeviceID: "offline-laptop", Folders: []localfiles.FolderSelection{{ID: "reports", Path: "/home/person/Reports", Access: "write"}}}
	data, _ := json.Marshal(settings)
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	settings.Folders[0].Access = "read"
	data, _ = json.Marshal(settings)
	response := httptest.NewRecorder()
	api.handleCrewLaptopSettings(response, request(http.MethodPut, "owner", string(data)))
	if response.Code != 200 {
		t.Fatalf("offline downgrade: %d %s", response.Code, response.Body.String())
	}
	saved, err := readLaptopSettings("owner", root)
	if err != nil || saved.Folders[0].Access != "read" {
		t.Fatalf("permission did not save: %+v %v", saved, err)
	}
	settings.Folders[0].Access = "write"
	data, _ = json.Marshal(settings)
	response = httptest.NewRecorder()
	api.handleCrewLaptopSettings(response, request(http.MethodPut, "owner", string(data)))
	if response.Code != 400 {
		t.Fatal("offline laptop accepted expanded access")
	}
}
