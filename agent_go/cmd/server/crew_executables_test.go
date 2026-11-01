package server

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowkb"
	"net/http"
	"net/http/httptest"

	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PLAT-746-265: an editable executable cannot choose its owner or gain another
// Crew's grants; revoking a co-owner affects the very next access check.
func TestCrewExecutableUsesTrustedParentAndLiveAccess(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENT_PRODUCTS", "work,agentworks,relays")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator"},{"id":"builder","username":"builder","role":"creator"},{"id":"stranger","username":"stranger","role":"creator"}]}`)
	registry := defaultProjectOwners()
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: "lab", ProjectID: "crew-lab", OwnerID: "owner", Shared: true, Private: true, AudienceConfigured: true, CoOwners: []string{"builder"}}); err != nil {
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
	if err := os.MkdirAll(filepath.Join(docs, binding.Root), 0755); err != nil {
		t.Fatal(err)
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
	if err := registry.modify(func(records map[string]projectOwnerRecord) (bool, error) {
		record := records["work/lab"]
		record.CoOwners = nil
		records["work/lab"] = record
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := workflowAccessForManifest(&UserClaims{UserID: "builder", Username: "builder"}, manifest); got != WorkflowAccessNone {
		t.Fatal("revoked co-owner retained access", got)
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
}

// PR498: a saved empty group selection must refuse before request construction,
// ledger admission or a worker capable of choosing its own group.
func TestScheduleEmptySelectionRefusedBeforeLaunch(t *testing.T) {
	svc := NewSchedulerService(&StreamingAPI{})
	sc := &ScheduleContext{WorkflowKind: "", Schedule: WorkflowSchedule{ID: "no-work", Name: "No work", CronExpression: "0 9 * * *", Timezone: "UTC", GroupNames: []string{}}}
	if err := svc.LoadSchedule(sc); err == nil || !strings.Contains(err.Error(), "choose at least one input set") {
		t.Fatal("empty selection did not refuse at admission", err)
	}
	if _, _, err := svc.executeJob(context.Background(), sc, "empty-run"); err == nil || !strings.Contains(err.Error(), "choose at least one input set") {
		t.Fatal("empty selection did not refuse before execution", err)
	}
	request := svc.buildWorkshopRequest(context.Background(), sc, "")
	options := request["execution_options"].(map[string]interface{})
	groups, ok := options["enabled_group_names"].([]string)
	if !ok || groups == nil || len(groups) != 0 {
		t.Fatal("empty selection collapsed to automatic", options)
	}
}
