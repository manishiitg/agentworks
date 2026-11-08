package server

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowkb"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"io/fs"
	"net/http"
	"net/http/httptest"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// PLAT-746-265: an editable executable cannot choose its owner or gain another
// Crew's grants; revoking Team membership affects the very next access check.
func TestCrewExecutableUsesTrustedParentAndLiveAccess(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENT_PRODUCTS", "work,agentworks,relays")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator"},{"id":"builder","username":"builder","role":"creator"},{"id":"stranger","username":"stranger","role":"creator"}]}`)
	registry := defaultProjectOwners()
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: "lab", ProjectID: "crew-lab", OwnerID: "owner", Shared: true, Private: true, AudienceConfigured: true}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	if err := writeCrewTeam(crewTeam{ProjectID: "crew-lab", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "builder"}}}); err != nil {
		t.Fatal(err)
	}
	binding := crewExecutableBinding{ID: "executable", CrewID: "crew-lab", CrewRoot: "Crew/lab", Root: "Workflow/executable", Kind: "workflow"}
	if err := registerCrewExecutable(binding); err != nil {
		t.Fatal(err)
	}
	files := &mockWorkspaceAPI{files: map[string]string{"Workflow/executable/workflow.json": `{"id":"executable","label":"Example","created_by":"stranger","access":{"owners":["stranger"],"allowed_kb_writers":["consumer"]},"team":{"leader_id":"stranger"}}`}}
	host := httptest.NewServer(files)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	for _, spelling := range []string{"Workflow/executable", "/Workflow/executable/", "Workflow/executable/./", "Workflow/executable/code/main.py"} {
		resolved, err := findCrewExecutable(spelling)
		if err != nil || resolved == nil || resolved.ID != binding.ID {
			t.Fatalf("binding %q: %v %v", spelling, resolved, err)
		}
	}
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	// Manifest saves validate the real registered storage, not a retired
	// docs/Workflow directory supplied only by this HTTP fixture.
	for _, dir := range []string{binding.CrewRoot, "_system/crew-data/" + binding.CrewID} {
		if err := os.MkdirAll(filepath.Join(docs, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if resolved, err := findCrewExecutable(filepath.Join(docs, binding.Root)); err != nil || resolved == nil {
		t.Fatalf("absolute binding: %v", err)
	}
	manifest, found, err := ReadWorkflowManifest(context.Background(), binding.Root)
	if err != nil || !found {
		t.Fatalf("read: %v", err)
	}
	if workflowOwner(binding.Root) != "owner" || manifest.CreatedBy != "owner" || manifest.Team != nil || workflowExecutionOwnerUserID(manifest) != "owner" {
		t.Fatal("manifest overrode execution owner")
	}
	// Inheriting the parent audience must not erase a source owner's
	// separate KB writer opt-in during an unrelated read/edit/save cycle.
	// The spoofed team above tests read authority. Exercise the independent
	// writer-grant save with a normal Crew child (which has no child team).
	var saved map[string]interface{}
	_ = json.Unmarshal([]byte(files.files[manifestPath(binding.Root)]), &saved)
	delete(saved, "team")
	baseline, _ := json.Marshal(saved)
	files.files[manifestPath(binding.Root)] = string(baseline)
	manifest.Label = "Renamed"
	ownerCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	if err := WriteWorkflowManifest(ownerCtx, binding.Root, manifest); err != nil {
		t.Fatal(err)
	}
	manifest, found, err = ReadWorkflowManifest(context.Background(), binding.Root)
	if err != nil || !found || len(manifest.Access.AllowedKBWriters) != 1 || manifest.Access.AllowedKBWriters[0] != "consumer" {
		t.Fatalf("KB writer lost after save: %+v %v", manifest, err)
	}
	listed := filterWorkflowManifestsForUser(&UserClaims{UserID: "owner", Username: "owner"}, []DiscoveredWorkflow{{WorkspacePath: binding.Root, Manifest: manifest}})
	if len(listed) != 1 || listed[0].CrewID != binding.CrewID {
		t.Fatal("discovery lost the trusted Crew parent")
	}
	// Review regressions exercise the actual context and scoped access edges.
	sctx := buildScheduleContext(binding.Root, manifest, WorkflowSchedule{RunAs: "builder"})
	if sctx.OwnerUserID != "owner" {
		t.Fatal("schedule RunAs selected a coowner account")
	}
	// A non-owner's admitted call launches under a fresh scheduler context
	// and the trusted main owner, rather than borrowing the calling chat.
	parentAPI := &StreamingAPI{}
	scheduler := NewSchedulerService(parentAPI)
	const parentSession = "schedule-non-owner-call"
	runCtx := scheduler.activateScheduleRunLocked(&ScheduleRuntimeState{}, parentSession, time.Now())
	defer scheduler.releaseScheduleRunContext(parentSession)
	executionCtx := internalBotRequestContext(runCtx, sctx.OwnerUserID)
	principal := GetUserIDFromContext(executionCtx)
	if principal != "owner" {
		t.Fatalf("co-owner call selected parent identity %q", principal)
	}
	parentPath := stableBuilderConversationLogPath(executionCtx, binding.Root, principal, parentSession)
	parentAPI.persistChatConversationToPathWithTerminalSession(parentSession, "", "workflow", principal,
		[]llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Run the saved function"}}}},
		&ChatHistoryAgentRuntime{Kind: "workflow", WorkspacePath: binding.Root, WorkshopMode: "run"}, nil,
		parentPath)
	materializeWorkspaceFixtureFile(t, files, docs, parentPath)
	for _, user := range []string{"owner", "builder"} {
		request := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		_, allowed := parentAPI.inspectionParentSessionInAuthorizedWorkspace(request, parentSession, binding.Root)
		if allowed != (user == "owner") {
			t.Fatalf("saved parent access user=%s allowed=%v", user, allowed)
		}
	}
	for _, target := range []string{"executable", "unrelated"} {
		claims := &UserClaims{UserID: "bot-route", Provider: "bot_route", BotRouteWorkflowID: target, BotRouteGrant: "read"}
		want := WorkflowAccessRead
		if target == "unrelated" {
			want = WorkflowAccessNone
		}
		if got := workflowAccessForManifest(claims, manifest); got != want {
			t.Fatalf("bot target=%s access=%s", target, got)
		}
	}
	for _, id := range []string{binding.ID, "unrelated"} {
		_, allowed, err := (&StreamingAPI{}).checkBotWorkflowAccessForPlatform(context.Background(), "bot-route", "", services.ChannelRoute{WorkflowID: id, WorkspacePath: binding.Root}, "slack")
		if err != nil || allowed != (id == binding.ID) {
			t.Fatalf("channel target=%s allowed=%v error=%v", id, allowed, err)
		}
	}
	for _, user := range []string{"owner", "builder", "stranger"} {
		got := workflowAccessForManifest(&UserClaims{UserID: user, Username: user}, manifest)
		want := WorkflowAccessOwner
		if user == "stranger" {
			want = WorkflowAccessNone
		}
		if got != want {
			t.Fatalf("%s access=%s, want %s", user, got, want)
		}
	}
	if err := writeCrewTeam(crewTeam{ProjectID: "crew-lab", OwnerID: "owner", On: false}); err != nil {
		t.Fatal(err)
	}

	if got := workflowAccessForManifest(&UserClaims{UserID: "builder", Username: "builder"}, manifest); got != WorkflowAccessNone {
		t.Fatal("revoked Team member retained access", got)
	}
	// PLAT-746-265: the new Team roster must flow through the same live
	// workflow admission, without changing the saved automation's owner.
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	for _, state := range []struct{ on, invited, allowed bool }{{true, true, false}, {true, false, true}, {false, false, false}} {
		if err := writeCrewTeam(crewTeam{ProjectID: binding.CrewID, OwnerID: "owner", On: state.on, People: []crewTeamPerson{{UserID: "builder", Invited: state.invited}}}); err != nil {
			t.Fatal(err)
		}
		got := workflowAccessForManifest(&UserClaims{UserID: "builder", Username: "builder"}, manifest)
		if (got == WorkflowAccessOwner) != state.allowed {
			t.Fatalf("Team on=%v invited=%v workflow access=%s", state.on, state.invited, got)
		}
		if got := buildScheduleContext(binding.Root, manifest, WorkflowSchedule{RunAs: "builder"}).OwnerUserID; got != "owner" {
			t.Fatalf("Team membership changed automation identity to %q", got)
		}
	}
	file, _ := crewExecutableFile(binding.Root)
	if err := os.WriteFile(file, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	denied := httptest.NewRecorder()
	if requireWorkflowVisible(denied, sharedSecretsRequest("GET", "/", "stranger", nil), binding.Root) || denied.Code != http.StatusForbidden {
		t.Fatal("corrupt binding granted endpoint visibility", denied.Code)
	}
	if _, err := findCrewExecutable(binding.Root); err == nil {
		t.Fatal("corrupt binding fell back to manifest")
	}
	if other, err := findCrewExecutable("Workflow/unrelated"); err != nil || other != nil {
		t.Fatal("unrelated workflow blocked", err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := registerCrewExecutable(binding); err != nil {
		t.Fatal(err)
	}
	files.mu.Lock()
	files.files["Workflow/executable/workflow.json"] = `{"id":"other","label":"Spoof"}`
	files.mu.Unlock()
	if _, _, err := ReadWorkflowManifest(context.Background(), binding.Root); err == nil {
		t.Fatal("accepted changed executable identity")
	}
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator","disabled":true},{"id":"builder","username":"builder","role":"creator"}]}`)
	disabled := buildScheduleContext(binding.Root, manifest, WorkflowSchedule{RunAs: "builder"})
	if disabled.OwnerUserID != "" || validateExecutableSchedule(context.Background(), disabled) == nil {
		t.Fatal("disabled parent owner was bypassed at schedule admission")
	}
	if got := workflowExecutionOwnerUserID(manifest); got != "" {
		t.Fatal("disabled Crew owner fell back to another execution account", got)
	}
}

// PLAT-746-265 / PR498: a shared child must not launder private source files
// through a raw manifest whose audience omits the Crew's Run people.
func TestCrewKnowledgeSourceUsesInheritedAudienceAtEndpoint(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENT_PRODUCTS", "work,agentworks")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator"},{"id":"reader","username":"reader","role":"creator"}]}`)
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "kb-lab", ProjectID: "kb-crew", OwnerID: "owner", Shared: true, Private: true, AudienceConfigured: true, RunPeople: []string{"reader"}}); err != nil {
		t.Fatal(err)
	}
	binding := crewExecutableBinding{ID: "kb-child", CrewID: "kb-crew", CrewRoot: "Crew/kb-lab", Root: "Workflow/kb-child", Kind: "workflow"}
	if err := registerCrewExecutable(binding); err != nil {
		t.Fatal(err)
	}
	consumer := `{"id":"kb-child","created_by":"owner","access":{"owners":["owner"]},"knowledgebase_sources":[{"workflow_id":"private-source","alias":"private","access":"read"}]}`
	source := `{"id":"private-source","created_by":"owner","access":{"owners":["owner"]}}`
	files := &mockWorkspaceAPI{files: map[string]string{binding.Root + "/workflow.json": consumer}}
	host := httptest.NewServer(files)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	write := func(name, raw string) {
		t.Helper()
		p := filepath.Join(docs, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("_system/crew-data/kb-crew/.keep", "")
	write(binding.CrewRoot+"/.agentworks/workflows/kb-child/workflow.json", consumer)
	write(binding.Root+"/workflow.json", consumer)
	write("Workflow/source/workflow.json", source)
	write("Workflow/source/knowledgebase/secret.md", "private content")
	// Use the endpoint's actual root accessor, matching real runtime resolution.
	response := httptest.NewRecorder()
	(&StreamingAPI{}).handleWorkflowKnowledgebaseSources(response, sharedSecretsRequest("GET", "/api/workflows/knowledgebase-sources?workspace_path="+binding.Root+"&alias=private&path=secret.md", "reader", nil))
	if response.Code == http.StatusOK {
		t.Fatal("knowledge endpoint exposed private source", response.Body.String())
	}
	// Run access to public automation details must not reveal private chat choices.
	svc := NewSchedulerService(&StreamingAPI{})
	for _, user := range []string{"owner", "reader"} {
		listing := httptest.NewRecorder()
		svc.listWorkflowWebhooks(listing, sharedSecretsRequest("GET", "/api/workflow-webhooks?workspace_path="+binding.Root, user, nil))
		if listing.Code != http.StatusOK {
			t.Fatalf("trigger list: %d %s", listing.Code, listing.Body.String())
		}
		var data map[string]json.RawMessage
		if e := json.Unmarshal(listing.Body.Bytes(), &data); e != nil {
			t.Fatal(e)
		}
		_, choices := data["chat_choices"]
		if choices != (user == "owner") {
			t.Fatalf("%s chat choices exposed=%v", user, choices)
		}
		effective, _, e := ReadWorkflowManifest(context.Background(), binding.Root)
		if e != nil {
			t.Fatal(e)
		}
		response := buildJobResponseForCaller(sharedSecretsRequest("GET", "/", user, nil), binding.Root, effective, WorkflowSchedule{ID: "sample"}, ScheduleRuntimeState{}, WorkflowScheduleMissedStatus{})
		if (len(response.RunDestinations) > 0) != (user == "owner") {
			t.Fatalf("%s schedule chat names exposed", user)
		}
	}
	resolved, err := workflowkb.Resolve(docs, binding.Root, nil)
	if err == nil {
		for _, entry := range resolved {
			if entry.Available {
				t.Fatal("private knowledge approved for shared child")
			}
		}
	}
	// Raw-authority checking is exercised independently of the HTTP workspace mock.
	inherited, err := workflowkb.ReadManifest(docs, binding.Root)
	if err != nil {
		t.Fatal(err)
	}
	private, err := workflowkb.ReadManifest(docs, "Workflow/source")
	if err != nil {
		t.Fatal(err)
	}
	if workflowkb.AudienceCanRead(inherited, private) {
		t.Fatal("raw audience bypass")
	}
	private.Access.Readers = []string{"reader"}
	encoded, _ := json.Marshal(private)
	write("Workflow/source/workflow.json", string(encoded))
	allowed, err := workflowkb.Resolve(docs, binding.Root, nil)
	if err != nil || len(allowed) != 1 || !allowed[0].Available {
		t.Fatalf("authorized source unavailable: %v %v", allowed, err)
	}
	response = httptest.NewRecorder()
	(&StreamingAPI{}).handleWorkflowKnowledgebaseSources(response, sharedSecretsRequest("GET", "/api/workflows/knowledgebase-sources?workspace_path="+binding.Root+"&alias=private&path=secret.md", "reader", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "private content") {
		t.Fatalf("allowed endpoint: %d %s", response.Code, response.Body.String())
	}
	// PR498 P1: a joined Team member is part of the inherited audience even
	// when absent from the registry's permanent owners and Run people.
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	if err := defaultProjectOwners().modify(func(records map[string]projectOwnerRecord) (bool, error) {
		key := projectOwnerKey("work", "kb-lab")
		record := records[key]
		record.RunPeople = nil
		records[key] = record
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeCrewTeam(crewTeam{ProjectID: "kb-crew", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "reader"}}}); err != nil {
		t.Fatal(err)
	}
	if !registryListHas(crewCoOwnerIDs(binding.CrewRoot), "reader") {
		t.Fatal("fixture did not grant Team build access")
	}
	write("Workflow/source/workflow.json", source)
	response = httptest.NewRecorder()
	(&StreamingAPI{}).handleWorkflowKnowledgebaseSources(response, sharedSecretsRequest("GET", "/api/workflows/knowledgebase-sources?workspace_path="+binding.Root+"&alias=private&path=secret.md", "reader", nil))
	if response.Code == http.StatusOK {
		t.Fatal("Team member exposed owner-only source", response.Body.String())
	}
	write("Workflow/source/workflow.json", string(encoded))
	response = httptest.NewRecorder()
	(&StreamingAPI{}).handleWorkflowKnowledgebaseSources(response, sharedSecretsRequest("GET", "/api/workflows/knowledgebase-sources?workspace_path="+binding.Root+"&alias=private&path=secret.md", "reader", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "private content") {
		t.Fatalf("Team authorized source: %d %s", response.Code, response.Body.String())
	}
}

// PR498: a saved empty group selection must refuse before request construction,
// ledger admission or a worker capable of choosing its own group.
func TestRetiredScheduleGroupsRefusedBeforeLaunch(t *testing.T) {
	svc := NewSchedulerService(&StreamingAPI{})
	sc := &ScheduleContext{WorkflowKind: "", Schedule: WorkflowSchedule{ID: "no-work", Name: "No work", CronExpression: "0 9 * * *", Timezone: "UTC", GroupNames: []string{}}}
	if err := svc.LoadSchedule(sc); err == nil || !strings.Contains(err.Error(), "input_preset") {
		t.Fatal("empty selection did not refuse at admission", err)
	}
	if _, _, err := svc.executeJob(context.Background(), sc, "empty-run"); err == nil || !strings.Contains(err.Error(), "input_preset") {
		t.Fatal("empty selection did not refuse before execution", err)
	}
	request := svc.buildWorkshopRequest(context.Background(), sc, "")
	options := request["execution_options"].(map[string]interface{})
	if _, present := options["enabled_group_names"]; present {
		t.Fatal("retired batch selection was forwarded", options)
	}

}

func TestCrewExecutableCreationRechecksDemotedOwner(t *testing.T) {
	svc, _, ctx := newCrewCreationTestEnv(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator"}]}`)
	crew, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Automation lab", Purpose: "Reports", IdempotencyKey: "account-edit"})
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{projectActions: svc}
	for _, role := range []string{"creator", "viewer", "creator"} {
		*directory = `{"users":[{"id":"owner","username":"owner","role":"` + role + `"}]}`
		invalidateUserDirectoryCache()
		before, _ := readCrewExecutables()
		w := httptest.NewRecorder()
		api.handleCrewExecutables(w, httptest.NewRequest(http.MethodPost, "/api/crew-executables?crew_id="+crew.CrewID, strings.NewReader(`{"name":"Report","kind":"script"}`)).WithContext(ctx))
		after, _ := readCrewExecutables()
		if role == "viewer" {
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "account cannot edit") || len(after) != len(before) {
				t.Fatalf("demoted owner: HTTP%d %s", w.Code, w.Body)
			}
		} else if w.Code != http.StatusCreated || len(after) != len(before)+1 {
			t.Fatalf("editable owner: HTTP%d %s", w.Code, w.Body)
		}
	}
	// PLAT-746: a new workflow is always a Python script, even when an older caller asks for kind "workflow".
	w := httptest.NewRecorder()
	api.handleCrewExecutables(w, httptest.NewRequest(http.MethodPost, "/api/crew-executables?crew_id="+crew.CrewID, strings.NewReader(`{"name":"Daily work","kind":"workflow"}`)).WithContext(ctx))
	var created crewExecutableBinding
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &created) != nil || created.Kind != "script" {
		t.Fatalf("workflow creation: HTTP%d %s, want a script", w.Code, w.Body)
	}

}

// PLAT-746-265: parking Relay must stop new Crew Relay creation without
// hiding existing automation from its owner or changing its saved definition.
func TestCrewWorkflowsHideUnsupportedKindsWithoutChangingFiles(t *testing.T) {
	svc, files, ctx := newCrewCreationTestEnv(t)
	t.Setenv("AGENT_PRODUCTS", "work,agentworks")
	crew, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Workflow lab", Purpose: "Run reports", IdempotencyKey: "park-relay"})
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{projectActions: svc}
	for _, kind := range []string{"workflow", "retired"} {
		binding := crewExecutableBinding{ID: kind, CrewID: crew.CrewID, CrewRoot: crew.WorkspacePath, Root: "Workflow/" + kind, Kind: kind}
		if err := registerCrewExecutable(binding); err != nil {
			t.Fatal(err)
		}
		m := NewWorkflowManifest(kind)
		m.ID = kind
		if kind == "retired" {
			m.Kind = "retired"
		}
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		files.files[binding.Root+"/workflow.json"] = string(raw)
	}
	originalRelay := files.files["Workflow/retired/workflow.json"]
	path := "/api/crew-executables?crew_id=" + crew.CrewID
	list := httptest.NewRecorder()
	api.handleCrewExecutables(list, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
	var response struct {
		Executables []crewExecutableBinding `json:"executables"`
	}
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &response) != nil || len(response.Executables) != 1 || response.Executables[0].Kind != "workflow" {
		t.Fatalf("Crew list: %d %s", list.Code, list.Body)
	}
	before, _ := readCrewExecutables()
	rejected := httptest.NewRecorder()
	api.handleCrewExecutables(rejected, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"name":"New Relay","kind":"retired"}`)).WithContext(ctx))
	after, _ := readCrewExecutables()
	if rejected.Code != http.StatusBadRequest || len(after) != len(before) {
		t.Fatalf("new Relay was created: %d %s", rejected.Code, rejected.Body)
	}
	reg := &recordingRegistrar{}
	if err := api.registerCrewExecutableFileTools(reg, "owner", crew.WorkspacePath); err != nil {
		t.Fatal(err)
	}
	for _, request := range []map[string]interface{}{
		{"action": "write", "workspace_path": "Workflow/retired", "file": "relay.py", "content": "changed"},
		{"action": "write", "workspace_path": response.Executables[0].Root, "file": "relay.py", "content": "changed"},
	} {
		if _, err := reg.tools["crew_workflow_files"].exec(ctx, request); err == nil {
			t.Fatal("Crew editor accepted Relay source")
		}
	}
	if files.files["Workflow/retired/workflow.json"] != originalRelay {
		t.Fatal("parking Relay changed its definition")
	}
	// Saved data remains on disk, but its unsupported kind cannot become a workflow.
	m, found, err := ReadWorkflowManifest(ctx, "Workflow/retired")
	if found || (m != nil && workflowAccessForManifest(GetUserFromContext(ctx), m) != WorkflowAccessNone) {
		t.Fatalf("disabled Relay gained access: %v %v", found, err)
	}
}

// PLAT-746-265: a Team writer holding its native lock must use the same moved
// source as HTTP editors, rather than recreate a second plan in runtime storage.
func TestCrewAutomationNativeCommitUsesMovedSource(t *testing.T) {
	docs := t.TempDir()
	docs, _ = filepath.EvalSymlinks(docs)
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	root := registerTestCrew(t, "native-source", "owner", false)
	b := crewExecutableBinding{ID: "native-source", Root: "Workflow/native-source", CrewID: "crew-native-source", CrewRoot: root, Kind: "workflow"}
	if err := registerCrewExecutable(b); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), teamLockHeldKey{}, b.Root)
	for _, rel := range []string{"planning/plan.json", "code/step/main.py", "planning/changelog/entry.json"} {
		target := filepath.Join(docs, root, ".agentworks/workflows", b.ID, rel)
		if strings.HasPrefix(rel, "code/") {
			target = filepath.Join(docs, root, ".agentworks/code", b.ID, strings.TrimPrefix(rel, "code/"))
		}
		if strings.Contains(rel, "changelog") {
			target = filepath.Join(docs, "_system/crew-data", b.CrewID, "workflows", b.ID, rel)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if handled, err := writeHeldTeamFile(ctx, b.Root+"/"+rel, "saved"); err != nil || !handled {
			t.Fatal(rel, handled, err)
		}
		raw, err := os.ReadFile(target)
		if err != nil || string(raw) != "saved" {
			t.Fatal(rel, string(raw), err)
		}
		opened, name, err := wf.OpenWorkflowDirectory(docs, b.Root, rel)
		if err != nil {
			t.Fatal(rel, err)
		}
		raw, err = opened.ReadFile(name)
		opened.Close()
		if err != nil || string(raw) != "saved" {
			t.Fatal("direct plan routing", rel, err)
		}
		if !strings.Contains(rel, "changelog") {
			if _, err := os.Stat(filepath.Join(docs, b.Root, rel)); !os.IsNotExist(err) {
				t.Fatal("recreated old source", rel)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(docs, b.Root)); !os.IsNotExist(err) {
		t.Fatal("retired root recreated")
	}
	listed := map[string]bool{}
	err := fs.WalkDir(wf.WorkflowFS{Docs: docs, Root: b.Root}, ".", func(path string, d fs.DirEntry, err error) error {
		if err == nil {
			listed[path] = true
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"planning/plan.json", "code/step/main.py", "planning/changelog/entry.json"} {
		if !listed[p] {
			t.Fatalf("moved file missing from listing: %s (%v)", p, listed)
		}
	}

	// A planted source directory cannot redirect native plan edits.
	source := filepath.Join(docs, root, ".agentworks/workflows", b.ID, "planning")
	os.RemoveAll(source)
	os.Symlink(t.TempDir(), source)
	if opened, _, err := wf.OpenWorkflowDirectory(docs, b.Root, "planning/plan.json"); err == nil {
		opened.Close()
		t.Fatal("source symlink admitted")
	}

}

// PLAT-746: the Crew's single Schedules tab must retain its workflow timers,
// without including another Crew or granting a stranger access.
func TestCrewScheduleListIncludesOnlyAuthorizedChildren(t *testing.T) {
	svc, _, ctx := newCrewCreationTestEnv(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator"},{"id":"stranger","username":"stranger","role":"creator"},{"id":"runner","username":"runner","role":"viewer"}]}`)
	manifests := map[string]*WorkflowManifest{}
	for _, name := range []string{"one", "two"} {
		root := "Crew/" + name
		owner := "owner"
		if name == "two" {
			owner = "stranger"
		}
		if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: name, ProjectID: name, OwnerID: owner, Private: true, AudienceConfigured: true}); err != nil {
			t.Fatal(err)
		}
		binding := crewExecutableBinding{ID: name, CrewID: name, CrewRoot: root, Root: "Workflow/" + name, Kind: "workflow"}
		if err := registerCrewExecutable(binding); err != nil {
			t.Fatal(err)
		}
		manifest := NewWorkflowManifest(name)
		manifest.ID, manifest.CreatedBy, manifest.crewExecutable = name, owner, &binding
		manifest.Access = &WorkflowAccess{Owners: []string{owner}}
		manifest.Schedules = []WorkflowSchedule{{ID: "timer-" + name, Name: "Daily work", Mode: "workshop", Enabled: false}}
		manifests[binding.Root] = manifest
	}
	previous := workflowManifests
	workflowManifests = &workflowManifestIndex{
		list: func(context.Context) ([]string, map[string]manifestStamp, error) {
			return []string{"Workflow/one", "Workflow/two"}, nil, nil
		},
		read: func(_ context.Context, path string) (*WorkflowManifest, bool, error) {
			m, ok := manifests[path]
			return m, ok, nil
		},
	}
	t.Cleanup(func() { workflowManifests = previous })
	scheduler := NewSchedulerService(&StreamingAPI{projectActions: svc})
	for _, user := range []string{"owner", "stranger", "runner"} {
		request := httptest.NewRequest(http.MethodGet, "/api/scheduler/jobs?entity_type=product&workspace_path=Crew%2Fone", nil).WithContext(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: user, Username: user}))
		response := httptest.NewRecorder()
		listScheduledJobsHandler(scheduler)(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s HTTP%d %s", user, response.Code, response.Body)
		}
		var result struct {
			Jobs []ScheduledJobResponse `json:"jobs"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if user != "owner" {
			if len(result.Jobs) != 0 {
				t.Fatal("stranger saw private timers")
			}
			continue
		}
		if len(result.Jobs) != 1 || result.Jobs[0].CrewWorkspacePath != "Crew/one" || result.Jobs[0].WorkspacePath != "Workflow/one" {
			t.Fatalf("parent list lost or mixed child schedules: %+v", result.Jobs)
		}
	}
}

// The Scripts tab lists everything runnable: the Python a workflow's own steps run, with where it is used, beside the
// saved Crew scripts. A step that runs a saved Crew script is not listed twice, and other step types are not scripts.
func TestCrewExecutablesListWorkflowStepScripts(t *testing.T) {
	svc, files, ctx := newCrewCreationTestEnv(t)
	crew, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Step scripts", Purpose: "Reports", IdempotencyKey: "step-scripts"})
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{projectActions: svc}
	binding := crewExecutableBinding{ID: "reports", CrewID: crew.CrewID, CrewRoot: crew.WorkspacePath, Root: "Workflow/reports", Kind: "workflow"}
	if err := registerCrewExecutable(binding); err != nil {
		t.Fatal(err)
	}
	manifest := NewWorkflowManifest("Reports")
	manifest.ID = "reports"
	raw, _ := json.Marshal(manifest)
	files.files[binding.Root+"/workflow.json"] = string(raw)
	files.files[binding.Root+"/planning/plan.json"] = `{"steps":[
		{"type":"scripted","id":"normalize-statement","title":"Normalize statement"},
		{"type":"scripted","id":"call-saved","title":"Call saved","script":"script:helper"},
		{"type":"agent","id":"summarize","title":"Summarize"}]}`
	list := httptest.NewRecorder()
	api.handleCrewExecutables(list, httptest.NewRequest(http.MethodGet, "/api/crew-executables?crew_id="+crew.CrewID, nil).WithContext(ctx))
	var response struct {
		StepScripts []struct {
			Workflow string `json:"workflow"`
			StepID   string `json:"step_id"`
			Step     string `json:"step"`
		} `json:"step_scripts"`
	}
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &response) != nil {
		t.Fatalf("list: HTTP%d %s", list.Code, list.Body)
	}
	if len(response.StepScripts) != 1 || response.StepScripts[0].StepID != "normalize-statement" || response.StepScripts[0].Step != "Normalize statement" || response.StepScripts[0].Workflow == "" {
		t.Fatalf("step scripts = %+v, want only the step that runs its own Python", response.StepScripts)
	}
}
