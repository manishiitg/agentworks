package server

import (
	"context"
	"encoding/json"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// PLAT-746-158: history must retain current Crew/token/file authorization;
// completion waits for the turn's children and survives later file edits.
func TestCrewSnapshotsKeepTurnHistoryAndCurrentFileAccess(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("LOCAL_MODE", "false")
	// Bind private storage exactly as the server registry does, keeping the
	// existing fixture's other Crew records and access checks intact.
	registry := filepath.Join(os.Getenv("AGENTWORKS_STATE_ROOT"), "ownership", "projects.json")
	raw, err := os.ReadFile(registry)
	if err != nil {
		t.Fatal(err)
	}
	var owners map[string]any
	if err := json.Unmarshal(raw, &owners); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"alpha", "beta"} {
		owners["projects"].(map[string]any)["work/"+id].(map[string]any)["project_id"] = id
	}
	raw, _ = json.Marshal(owners)
	if err := os.WriteFile(registry, raw, 0600); err != nil {
		t.Fatal(err)
	}
	private, _ := crewroot.PrivateRoot(docs, "beta")
	alphaPrivate, _ := crewroot.PrivateRoot(docs, "alpha")
	for _, dir := range []string{private, alphaPrivate, filepath.Join(docs, linkAlphaPath), filepath.Join(docs, linkBetaPath, "visible"), filepath.Join(docs, linkBetaPath, "denied")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(docs, linkBetaPath, path), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("visible/main.txt", "before\n")
	write("denied/other.txt", "private before\n")
	api := lifecycleTestAPI()
	api.eventStore = storeevents.NewEventStore(100)
	api.eventStore.AddEvent("session", storeevents.Event{ID: "earlier", Type: "user_message", SessionID: "session"})
	api.lastQueryRequests = map[string]QueryRequest{"session": {SelectedFolder: linkBetaPath}}
	// PLAT-746-158: Cowork turns must not create Git storage, even when a
	// browser or native client sends the same ordinary chat request as Studio.
	modeFile := filepath.Join(private, "product.json")
	if err := os.WriteFile(modeFile, []byte(`{"mode":"cowork"}`), 0600); err != nil {
		t.Fatal(err)
	}
	api.prepareCrewTurnSnapshot("cowork-turn", "session", QueryRequest{SelectedFolder: linkBetaPath})
	if _, exists := api.crewTurnSnapshots.Load("cowork-turn"); exists {
		t.Fatal("Cowork started a file capture")
	}
	entries, err := os.ReadDir(private)
	if err != nil || len(entries) != 1 || entries[0].Name() != "product.json" {
		t.Fatalf("Cowork created snapshot storage: %v %v", entries, err)
	}
	// Switching the saved project mode enables the following turn; no reload
	// or client-provided mode is needed. The rest exercises real Git capture.
	if err := os.WriteFile(modeFile, []byte(`{"mode":"dev"}`), 0600); err != nil {
		t.Fatal(err)
	}
	// A Crew with no private index defers the send-path capture
	// (PLAT-746-163); build it as the background index build does.
	roots, found, err := crewStorageRoots(linkBetaPath)
	if err != nil || !found {
		t.Fatalf("Crew roots: %v %v", found, err)
	}
	if _, err := (filesnapshots.Store{Root: roots.Files, Private: roots.Private}).Warm(time.Minute); err != nil {
		t.Fatal(err)
	}
	api.trackConversationTurnStart("turn", "session", QueryRequest{SelectedFolder: linkBetaPath})
	api.prepareCrewTurnSnapshot("turn", "session", QueryRequest{SelectedFolder: linkBetaPath})
	value, ok := api.crewTurnSnapshots.Load("turn")
	if !ok {
		t.Fatal("snapshot was not prepared")
	}
	snapshot := value.(*crewTurnSnapshot)
	// The send does not wait for the before-capture; the test's edits below
	// come after it, as the accepted-risk rule assumes.
	<-snapshot.beforeDone
	api.trackSyntheticConversationTurnStart("child", "session", "turn", "child task")
	api.eventStore.AddEvent("session", storeevents.Event{ID: "reply", Type: "unified_completion", SessionID: "session"})
	api.bindCrewSnapshotCompletion("turn", 0)
	endSequence := api.crewSnapshotSequence("session")
	api.completeTrackedExecution("turn", trackedExecutionStatusCompleted, "", nil)
	if api.crewSnapshotReady(snapshot) {
		t.Fatal("snapshot finished while child still writes")
	}
	write("visible/main.txt", "after\n")
	write("denied/other.txt", "private after\n")
	api.completeTrackedExecution("child", trackedExecutionStatusCompleted, "", nil)
	// The ordinary observer waits for the existing handoff projection too.
	if !api.crewSnapshotReady(snapshot) {
		t.Fatal("completed child did not settle")
	}
	// A subsequent response arrives before the asynchronous file capture.
	api.eventStore.AddEvent("session", storeevents.Event{ID: "next-reply", Type: "unified_completion", SessionID: "session"})
	api.finishCrewTurnSnapshot(snapshot)
	reference := api.eventStore.LastEventsRaw("session", 1)[0]
	payload, _ := json.Marshal(reference)
	if reference.Type != "reply_file_changes" || snapshot.completedSequence.Load() != endSequence {
		t.Fatalf("late snapshot lost its reply boundary: %s", payload)
	}
	// The card renders from the event: the summary (counts, files, group) is
	// computed once at capture, under the same path rules as the compare op.
	if !strings.Contains(string(payload), `"added":2,"files":2,"groups":[{"kind":"crew_files"`) || !strings.Contains(string(payload), `{"path":"visible/main.txt","status":"M","added":1,"removed":1}`) {
		t.Fatalf("reply event has no file summary: %s", payload)
	}
	if got := api.trackedWorkflowExecutions["turn"].Metadata["file_snapshot_id"]; got != filesnapshots.ID("turn") {
		t.Fatalf("execution reference: %q", got)
	}
	write("visible/main.txt", "later turn\n")
	bounded := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, CrewIDs: []string{"beta"}, FileGuard: &wf.FolderGuard{ReadPaths: []string{"visible"}}}}
	args := map[string]any{"crew_id": "beta", "snapshot_id": filesnapshots.ID("turn"), "file": "visible/main.txt"}
	code, out := externalCrewRequest(t, env, bounded, "compare_crew_snapshot", args)
	encoded, _ := json.Marshal(out)
	if code != 200 || !strings.Contains(string(encoded), "+after") || strings.Contains(string(encoded), "private after") || strings.Contains(string(encoded), "later turn") || strings.Contains(string(encoded), "denied/") {
		t.Fatalf("historical file access: %d %s", code, encoded)
	}
	args["side"] = "after"
	code, out = externalCrewRequest(t, env, bounded, "compare_crew_snapshot", args)
	if code != 200 || out["content"] != "after\n" {
		t.Fatalf("MCP saved file: %d %+v", code, out)
	}
	args["file"] = "denied/other.txt"
	if code, _ := externalCrewRequest(t, env, bounded, "compare_crew_snapshot", args); code != 404 {
		t.Fatalf("MCP denied saved file: %d", code)
	}
	delete(args, "side")
	args["file"] = "denied/other.txt"
	if code, _ := externalCrewRequest(t, env, bounded, "compare_crew_snapshot", args); code != 404 {
		t.Fatalf("denied file: %d", code)
	}
	args["crew_id"] = "alpha"
	if code, _ := externalCrewRequest(t, env, bounded, "compare_crew_snapshot", args); code != 404 {
		t.Fatalf("denied Crew: %d", code)
	}
	// The HTTP route must use the same reader and current file guard.
	req := httptest.NewRequest("GET", "/api/workspace-snapshots?workspace_path=Crew/beta&op=snapshot_compare&snapshot_id="+filesnapshots.ID("turn")+"&file=visible/main.txt", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, bounded))
	w := httptest.NewRecorder()
	requireWorkflowReadAccessCallerRelative(env.api.handleCrewFileSnapshots)(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "+after") {
		t.Fatalf("HTTP history: %d %s", w.Code, w.Body.String())
	}
	query := req.URL.Query()
	query.Set("op", "snapshot_file")
	query.Set("side", "after")
	req.URL.RawQuery = query.Encode()
	w = httptest.NewRecorder()
	requireWorkflowReadAccessCallerRelative(env.api.handleCrewFileSnapshots)(w, req)
	if w.Code != 200 || w.Body.String() != "after\n" {
		t.Fatalf("HTTP saved file: %d %s", w.Code, w.Body.String())
	}
	query.Set("file", "denied/other.txt")
	req.URL.RawQuery = query.Encode()
	w = httptest.NewRecorder()
	requireWorkflowReadAccessCallerRelative(env.api.handleCrewFileSnapshots)(w, req)
	if w.Code != 404 {
		t.Fatalf("HTTP denied saved file: %d", w.Code)
	}
	// A website route must not bypass the token's Crew selection merely because
	// the same person owns another Crew. Both roots exist in this fixture.
	deniedRequest := httptest.NewRequest("GET", "/api/workspace-snapshots?workspace_path=Crew/alpha", nil).WithContext(req.Context())
	deniedResponse := httptest.NewRecorder()
	requireWorkflowReadAccessCallerRelative(env.api.handleCrewFileSnapshots)(deniedResponse, deniedRequest)
	if deniedResponse.Code != 404 {
		t.Fatalf("HTTP Crew token bound: %d %s", deniedResponse.Code, deniedResponse.Body.String())
	}
	// A rejected delivery leaves no made-up comparison or active observer.
	api.prepareCrewTurnSnapshot("rejected", "session", QueryRequest{SelectedFolder: linkBetaPath})
	api.discardUnstartedCrewSnapshot("rejected")
	if _, exists := api.crewTurnSnapshots.Load("rejected"); exists {
		t.Fatal("undelivered snapshot remains active")
	}
}
