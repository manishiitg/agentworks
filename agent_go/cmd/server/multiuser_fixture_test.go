package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// PLAT-442 step 5: one reusable multi-user fixture. Two users, A and B; a docs tree with a Crew owned by A, a Code
// owned by A, and a Goal that A owns and B reads. No real CLI and no network: the workspace service is the mock, the
// slot table is a temp file, the docs root and the CLI state root are temp folders.
//
// The layout says where each project lives, so the same assertions run against today's per-user folders
// (legacyIdentityLayout) and, when the Crew move (PLAT-442 step 4) lands, against `Crew/<id>`
// (a layout whose CrewPhysical and CrewShort both return "Crew/<folder>").

const (
	fixtureUserA = "user-a"
	fixtureUserB = "user-b"
	// Folder names under the project roots; the ids are what the manifests carry.
	fixtureCrewFolder = "alpha-c1a2b3c4"
	fixtureCrewID     = "c1a2b3c4-0000"
	fixtureCodeFolder = "app-c0de0001"
	fixtureCodeID     = "c0de0001-0000"
	fixtureGoal       = "Workflow/goal-1"
	fixtureSlotA      = "slot08"
	fixtureSlotB      = "slot09"
)

// identityLayout is where the fixture's projects live.
type identityLayout struct {
	// CrewPhysical is the Crew's root as storage spells it (what a reader sends).
	CrewPhysical func(owner, folder string) string
	// CrewShort is the root as its owner's UI spells it ("" owner-relative spelling).
	CrewShort func(folder string) string
	// CodePhysical / CodeShort are the same for the owner's private Code.
	CodePhysical func(owner, folder string) string
	CodeShort    func(folder string) string
}

// legacyIdentityLayout is today's: every project in its owner's private tree.
func legacyIdentityLayout() identityLayout {
	return identityLayout{
		CrewPhysical: func(owner, folder string) string {
			return workspaceref.PhysicalPath(owner, workspaceref.CrewProjectsRoot, folder)
		},
		CrewShort: func(folder string) string { return workspaceref.CrewProjectsRoot + "/" + folder },
		CodePhysical: func(owner, folder string) string {
			return workspaceref.PhysicalPath(owner, workspaceref.CodeProjectsRoot, folder)
		},
		CodeShort: func(folder string) string { return workspaceref.CodeProjectsRoot + "/" + folder },
	}
}

// identityExpectation is the run-as identity each turn kind must resolve to. It is the owner-facing statement of
// who runs as whom; change it here, once, when a decision changes.
type identityExpectation struct {
	// CodeOwnerSlot: A's Code turn runs as A's slot.
	CodeOwnerSlot string
	// CrewOwnerSlot / CrewReaderSlot: what a Crew CLI turn runs as. A Crew CLI starts in an isolated runtime folder
	// under the app's state root, so today both are the app account ("").
	CrewOwnerSlot  string
	CrewReaderSlot string
	// GoalSlot: a Goal turn runs as the app account.
	GoalSlot string
}

// todayIdentity is what the platform does now (PLAT-442 step 2 keeps behaviour).
var todayIdentity = identityExpectation{CodeOwnerSlot: fixtureSlotA}

type multiUserFixture struct {
	t      *testing.T
	Layout identityLayout
	Docs   string
	State  string
	Mock   *mockWorkspaceAPI
	API    *StreamingAPI
	Code   agentprofiles.Profile
	Crew   agentprofiles.Profile
}

func newMultiUserFixture(t *testing.T, layout identityLayout) *multiUserFixture {
	t.Helper()
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"`+fixtureUserA+`","username":"a","can_create":true},{"id":"`+fixtureUserB+`","username":"b","can_create":true}]}`)

	root := t.TempDir()
	f := &multiUserFixture{t: t, Layout: layout, Docs: filepath.Join(root, "docs"), State: filepath.Join(root, "state"), Mock: &mockWorkspaceAPI{files: map[string]string{}}}
	t.Setenv("WORKSPACE_DOCS_PATH", f.Docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", f.State)

	// The host's slot table: A and B each hold a slot; slots are on.
	table := filepath.Join(root, "slots.json")
	if err := os.WriteFile(table, []byte(`{"slots":{"`+fixtureSlotA+`":"`+fixtureUserA+`","`+fixtureSlotB+`":"`+fixtureUserB+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTWORKS_SLOTS", "on")
	t.Setenv("AGENTWORKS_SLOTS_FILE", table)

	crewRoot := layout.CrewPhysical(fixtureUserA, fixtureCrewFolder)
	codeRoot := layout.CodePhysical(fixtureUserA, fixtureCodeFolder)
	f.put(crewRoot+"/product.json", `{"schema_version":1,"product":"work","id":"`+fixtureCrewID+`","title":"Alpha","session_id":"work:project:`+fixtureCrewID+`","owner_id":"`+fixtureUserA+`"}`)
	f.put(crewRoot+"/workflow.json", `{"schema_version":1,"id":"`+fixtureCrewID+`","label":"Alpha","capabilities":{}}`)
	f.put(crewRoot+"/code/notes.md", "crew notes")
	f.put(codeRoot+"/product.json", `{"schema_version":1,"product":"code","id":"`+fixtureCodeID+`","title":"App","session_id":"code:project:`+fixtureCodeID+`","owner_id":"`+fixtureUserA+`"}`)
	f.put(codeRoot+"/workflow.json", `{"schema_version":1,"id":"`+fixtureCodeID+`","label":"App","capabilities":{}}`)
	f.put(codeRoot+"/code/main.go", "package main")
	f.put(fixtureGoal+"/workflow.json", `{"schema_version":1,"id":"goal-1","label":"Goal","created_by":"`+fixtureUserA+`","access":{"owners":["`+fixtureUserA+`"],"readers":["`+fixtureUserB+`"]},"capabilities":{}}`)
	f.put(fixtureGoal+"/planning/plan.json", "{}")

	// The server's owner registry (PLAT-449): the startup scan registers the projects in their owners' trees from
	// the path; a Crew at the shared root is registered by whatever created or moved it (here: the fixture).
	migrateProductOwners(f.Docs)
	if workspaceref.MustParse(crewRoot).IsShared() {
		if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: fixtureCrewFolder, OwnerID: fixtureUserA, ProjectID: fixtureCrewID, Shared: true}); err != nil {
			t.Fatal(err)
		}
	}

	ws := httptest.NewServer(f.Mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)

	registry := agentprofiles.NewRegistry()
	f.Code = agentprofiles.Profile{
		ID: "code", Name: "Code", Version: 1, SystemPromptTemplate: "hi", BuiltIn: true, Product: "code",
		Runtime: agentprofiles.RuntimePolicy{
			Transport:    "auto",
			Conversation: agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject},
			Workspace:    agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, Root: "Chats", ProjectsRoot: workspaceref.CodeProjectsRoot},
		},
	}
	f.Crew = agentprofiles.Profile{
		ID: "work", Name: "Crew", Version: 1, SystemPromptTemplate: "hi", BuiltIn: true, Product: "work",
		Runtime: agentprofiles.RuntimePolicy{
			Transport:    "auto",
			Conversation: agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject},
			Workspace:    agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, Root: "Chats", ProjectsRoot: workspaceref.CrewProjectsRoot},
		},
	}
	for _, p := range []agentprofiles.Profile{f.Code, f.Crew} {
		if err := registry.RegisterProfile(p); err != nil {
			t.Fatal(err)
		}
	}
	f.API = &StreamingAPI{agentProfiles: registry}
	return f
}

// put writes a file to the mock workspace service and to the docs folder on disk (a CLI runtime links to the real
// project folder, so the folder must exist).
func (f *multiUserFixture) put(rel, content string) {
	f.t.Helper()
	f.Mock.mu.Lock()
	f.Mock.files[rel] = content
	f.Mock.mu.Unlock()
	full := filepath.Join(f.Docs, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// Ctx is a request context for a signed-in user.
func (f *multiUserFixture) Ctx(userID string) context.Context {
	return context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: userID, Username: userID})
}

func (f *multiUserFixture) Claims(userID string) *UserClaims {
	return &UserClaims{UserID: userID, Username: userID}
}

// Proxy asks the workspace proxy's gate (the one hop that knows who is calling) whether a raw request may pass:
// 0 means allowed, otherwise the refusing status.
func (f *multiUserFixture) Proxy(userID, method, workspacePath string) int {
	f.t.Helper()
	req := httptest.NewRequest(method, "/api/wp/api/documents/"+workspacePath, nil)
	if method == http.MethodPut || method == http.MethodPost {
		req = httptest.NewRequest(method, "/api/wp/api/documents/"+workspacePath, jsonBody(map[string]string{"content": "x"}))
		req.Header.Set("Content-Type", "application/json")
	}
	req = req.WithContext(f.Ctx(userID))
	status, _, cleanup := workspaceProxyCrossUserBlock(req, userID)
	if cleanup != nil {
		cleanup()
	}
	return status
}

func jsonBody(v interface{}) *bytes.Reader {
	data, _ := json.Marshal(v)
	return bytes.NewReader(data)
}
