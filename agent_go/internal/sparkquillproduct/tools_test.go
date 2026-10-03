package sparkquillproduct

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

// fakeWorkspace is the slice of the workspace API the family tools use:
// GET and PUT /api/documents/<path>.
type fakeWorkspace struct {
	mu    sync.Mutex
	files map[string]string
}

func (f *fakeWorkspace) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		// GET /api/documents?folder=… lists a folder's direct entries.
		if r.URL.Path == "/api/documents" && r.Method == http.MethodGet {
			folder := strings.Trim(r.URL.Query().Get("folder"), "/") + "/"
			seen := map[string]bool{}
			var entries []map[string]interface{}
			for fp := range f.files {
				if !strings.HasPrefix(fp, folder) {
					continue
				}
				rest := strings.TrimPrefix(fp, folder)
				if i := strings.Index(rest, "/"); i >= 0 {
					if dir := folder + rest[:i]; !seen[dir] {
						seen[dir] = true
						entries = append(entries, map[string]interface{}{"filepath": dir, "type": "folder"})
					}
					continue
				}
				entries = append(entries, map[string]interface{}{"filepath": fp, "type": "file"})
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": entries})
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/documents/") {
			http.NotFound(w, r)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/api/documents/")
		// POST /api/documents/<path>/move relocates a file.
		if r.Method == http.MethodPost && strings.HasSuffix(p, "/move") {
			src := strings.TrimSuffix(p, "/move")
			var body struct {
				Destination string `json:"destination_path"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			content, ok := f.files[src]
			if !ok || body.Destination == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			delete(f.files, src)
			f.files[body.Destination] = content
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
			return
		}
		switch r.Method {
		case http.MethodGet:
			content, ok := f.files[p]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "not found"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{"filepath": p, "content": content}})
		case http.MethodPut:
			var body struct {
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.files[p] = body.Content
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

type eventSink struct {
	mu     sync.Mutex
	events []*orchestratorevents.ProductInteractionEvent
}

func (s *eventSink) emit(ev any) {
	if e, ok := ev.(*orchestratorevents.ProductInteractionEvent); ok {
		s.mu.Lock()
		s.events = append(s.events, e)
		s.mu.Unlock()
	}
}

func (s *eventSink) kinds() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, e := range s.events {
		out = append(out, e.Kind)
	}
	return out
}

func newToolHarness(t *testing.T) (*fakeWorkspace, *eventSink, agentprofiles.ToolRuntimeContext, string) {
	t.Helper()
	fake := &fakeWorkspace{files: map[string]string{}}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	sink := &eventSink{}
	rt := agentprofiles.ToolRuntimeContext{UserID: "u1", SessionID: "s1", WorkspacePath: "Chats/SparkQuill", Emit: sink.emit}
	return fake, sink, rt, srv.URL
}

func build(t *testing.T, factory agentprofiles.ToolFactory, rt agentprofiles.ToolRuntimeContext) agentprofiles.ToolSpec {
	t.Helper()
	spec, err := factory(rt, nil)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestFamilyStateToolsWriteFamilyJSONAndMirrors(t *testing.T) {
	fake, sink, rt, url := newToolHarness(t)
	fake.files["_users/u1/Chats/SparkQuill/family.json"] = `{"engine":"codex-cli","parent_model":"gpt-6.1-sol","child_model":"gpt-6-luna","connection_id":"family-account","parent_reasoning_effort":"medium","child_reasoning_effort":"high"}`
	ctx := context.Background()

	profile := build(t, setChildProfileFactory(url), rt)
	if _, err := profile.Execute(ctx, map[string]interface{}{"name": "Maya", "grade": "6"}); err != nil {
		t.Fatal(err)
	}
	if _, err := profile.Execute(ctx, map[string]interface{}{"board": "CBSE"}); err != nil {
		t.Fatal(err)
	}
	label := build(t, setParentLabelFactory(url), rt)
	if _, err := label.Execute(ctx, map[string]interface{}{"label": "mom"}); err != nil {
		t.Fatal(err)
	}
	var state FamilyState
	if err := json.Unmarshal([]byte(fake.files["_users/u1/Chats/SparkQuill/family.json"]), &state); err != nil {
		t.Fatal(err)
	}
	if state.Child == nil || state.Child.Name != "Maya" || state.Child.Grade != "6" || state.Child.Board != "CBSE" || state.ParentLabel != "mom" {
		t.Fatalf("family.json = %+v", state)
	}
	if state.Engine != "codex-cli" || state.ParentModel != "gpt-6.1-sol" || state.ChildModel != "gpt-6-luna" || state.ConnectionID != "family-account" || state.ParentReasoningEffort != "medium" || state.ChildReasoningEffort != "high" {
		t.Fatalf("family profile update discarded AI settings: %+v", state)
	}
	if !strings.Contains(fake.files["_users/u1/Chats/SparkQuill/memory/child-profile.json"], `"Maya"`) {
		t.Fatalf("memory mirrors missing: %v", fake.files)
	}
	if k := sink.kinds(); len(k) != 3 || k[0] != "family_updated" {
		t.Fatalf("events = %v", k)
	}
	if vars := ParentPromptVariables(state); vars["CHILD_WHO"] != "Maya, Grade 6 (CBSE)" || vars["CHILD_INFO_NUDGE"] != "" {
		t.Fatalf("prompt variables should now see the saved family: %+v", vars)
	}
}

func TestCreateLearningActivityWritesManifestAndProject(t *testing.T) {
	fake, sink, rt, url := newToolHarness(t)
	ctx := context.Background()
	create := build(t, createLearningActivityFactory(url), rt)

	if _, err := create.Execute(ctx, map[string]interface{}{"dir": "Math/Fractions/x", "title": "T"}); err == nil {
		t.Fatal("nested subject/topic paths must be refused now that activities are flat")
	}
	if _, err := create.Execute(ctx, map[string]interface{}{"dir": "activities/2026-09-03-fractions", "title": "T", "items": []interface{}{"quick-check.html"}}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing item must be refused: %v", err)
	}
	fake.files["_users/u1/Chats/SparkQuill/activities/2026-09-03-fractions/quick-check.html"] = "<html>"
	fake.files["_users/u1/Chats/SparkQuill/activities/2026-09-03-fractions/quick-check-KEY.md"] = "answers"
	out, err := create.Execute(ctx, map[string]interface{}{"dir": "_users/u1/Chats/SparkQuill/activities/2026-09-03-fractions", "title": "Fractions — Quick Check", "subject": "Math", "topic": "Fractions",
		"items": []interface{}{"quick-check.html", "quick-check-KEY.md"}, "goal": "answer all ten on her own", "persona": "playful coach"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"items":1`) {
		t.Fatalf("answer key must never be an item: %s", out)
	}
	// The activity folder is the child's sandbox: the key must have left it.
	if _, still := fake.files["_users/u1/Chats/SparkQuill/activities/2026-09-03-fractions/quick-check-KEY.md"]; still {
		t.Fatal("the answer key must not stay inside the activity folder")
	}
	if fake.files["_users/u1/Chats/SparkQuill/keys/2026-09-03-fractions-KEY.md"] != "answers" {
		t.Fatalf("the answer key must move to keys/: %v", fake.files)
	}
	if !strings.Contains(out, `"answer_keys_moved_to":["keys/2026-09-03-fractions-KEY.md"]`) {
		t.Fatalf("the tool must report where the key went: %s", out)
	}
	var manifest ActivityManifest
	if err := json.Unmarshal([]byte(fake.files["_users/u1/Chats/SparkQuill/activities/2026-09-03-fractions/activity.json"]), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Subject != "Math" || manifest.Goal == "" || len(manifest.Items) != 1 {
		t.Fatalf("manifest = %+v", manifest)
	}
	var project activityProject
	if err := json.Unmarshal([]byte(fake.files["_users/u1/Chats/SparkQuill/activities/2026-09-03-fractions/product.json"]), &project); err != nil {
		t.Fatal(err)
	}
	if project.Product != ChildProfileID || project.ID != "2026-09-03-fractions" || !strings.HasPrefix(project.SessionID, "product-") {
		t.Fatalf("product.json must bind the folder to the child profile: %+v", project)
	}

	// A page written in the activity vocabulary is rendered into the finished
	// page, listed under its rendered name, and its section map is recorded.
	fake.files["_users/u1/Chats/SparkQuill/family.json"] = `{"child":{"name":"Maya","grade":"6","board":"CBSE"}}`
	fake.files["_users/u1/Chats/SparkQuill/activities/2026-09-03-fractions/notes.sq.html"] = `<h1>Fractions</h1><section data-role="learn"><h2>Idea</h2><p>Pieces must match.</p></section><section data-role="check"><h2>Check</h2><div class="q" data-marks="2"><p>1/2+1/3</p></div><input></section>`
	out, err = create.Execute(ctx, map[string]interface{}{"dir": "activities/2026-09-03-fractions", "title": "Fractions", "items": []interface{}{"notes.sq.html"}, "goal": "learn then check"})
	if err != nil {
		t.Fatal(err)
	}
	rendered := fake.files["_users/u1/Chats/SparkQuill/activities/2026-09-03-fractions/notes.html"]
	if !strings.Contains(rendered, `id="q1"`) || !strings.Contains(rendered, `data-role="check"`) || !strings.Contains(rendered, "SQ.choose") {
		t.Fatalf("rendered page wrong:\n%s", rendered)
	}
	if !strings.Contains(rendered, `<input>`) || !strings.Contains(out, `"notes.html"`) || !strings.Contains(out, `"marks":2`) {
		t.Fatalf("result = %s", out)
	}
	if err := json.Unmarshal([]byte(fake.files["_users/u1/Chats/SparkQuill/activities/2026-09-03-fractions/activity.json"]), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Sections) != 2 || manifest.Sections[1].Role != RoleCheck || manifest.Sections[1].Questions[0] != "q1" || manifest.Items[0] != "notes.html" {
		t.Fatalf("manifest = %+v", manifest)
	}
	// Re-finalizing keeps the child's conversation.
	if _, err := create.Execute(ctx, map[string]interface{}{"dir": "activities/2026-09-03-fractions", "title": "Fractions — Quick Check v2", "goal": "same"}); err != nil {
		t.Fatal(err)
	}
	var again activityProject
	_ = json.Unmarshal([]byte(fake.files["_users/u1/Chats/SparkQuill/activities/2026-09-03-fractions/product.json"]), &again)
	if again.SessionID != project.SessionID || again.Title != "Fractions — Quick Check v2" {
		t.Fatalf("session id must survive re-finalizing: %+v vs %+v", again, project)
	}
	if k := sink.kinds(); len(k) != 3 || k[0] != "activity_created" {
		t.Fatalf("events = %v", k)
	}
}

func TestTransientToolsEmitProductInteractions(t *testing.T) {
	_, sink, rt, _ := newToolHarness(t)
	ctx := context.Background()
	celebrate := build(t, celebrateFactory(), rt)
	if out, err := celebrate.Execute(ctx, map[string]interface{}{"stars": float64(7), "reason": "finished!"}); err != nil || !strings.Contains(out, `"stars_awarded":3`) {
		t.Fatalf("stars must clamp to 3: %s %v", out, err)
	}
	scene := build(t, showSceneFactory(), rt)
	if _, err := scene.Execute(ctx, map[string]interface{}{"html": "<b>hi</b>"}); err != nil {
		t.Fatal(err)
	}
	if k := strings.Join(sink.kinds(), ","); k != "celebrate,scene" {
		t.Fatalf("events = %s", k)
	}
	for _, e := range sink.events {
		if e.Product != ProductName || e.GetEventType() != orchestratorevents.ProductInteraction {
			t.Fatalf("event mis-tagged: %+v", e)
		}
	}
}

func TestOpenToolsRefuseMissingTargetsAndUndeclaredPresentation(t *testing.T) {
	fake, _, rt, url := newToolHarness(t)
	ctx := context.Background()
	undeclared := build(t, openFileFactory(url, false), rt)
	if _, err := undeclared.Execute(ctx, map[string]interface{}{"path": "reports/progress.html"}); err == nil || !strings.Contains(err.Error(), "presentation kind") {
		t.Fatalf("open_file without a presentation binding must fail closed: %v", err)
	}
	rt.Presentation = &agentprofiles.PresentationBinding{Kind: "document.file"}
	open := build(t, openFileFactory(url, false), rt)
	if _, err := open.Execute(ctx, map[string]interface{}{"path": "reports/progress.html"}); err == nil || !strings.Contains(err.Error(), "no file") {
		t.Fatalf("missing file must be refused before any presentation is written: %v", err)
	}
	fake.files["_users/u1/Chats/SparkQuill/activities/a1/activity.json"] = "{not json"
	rt.Presentation = &agentprofiles.PresentationBinding{Kind: "sparkquill.activity"}
	openActivity := build(t, openActivityFactory(url), rt)
	if _, err := openActivity.Execute(ctx, map[string]interface{}{"dir": "activities/a1"}); err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Fatalf("corrupt manifest must be reported: %v", err)
	}
	if _, err := openActivity.Execute(ctx, map[string]interface{}{"dir": "activities/nope"}); err == nil || !strings.Contains(err.Error(), "no activity") {
		t.Fatalf("missing activity must be reported: %v", err)
	}
}

// open_file's existence check must work for any file, not just ones whose
// bytes happen to be readable as text (e.g. a generated PNG from image_gen) —
// it must behave like AgentWorks' own generic file open, not assume HTML/text.
func TestOpenFileOpensAnyFileNotJustText(t *testing.T) {
	fake, _, rt, url := newToolHarness(t)
	ctx := context.Background()
	fake.files["_users/u1/Chats/SparkQuill/image-demo/owl.png"] = "\x89PNG\r\n\x1a\nnot-real-but-not-utf8-either\xff\xfe"
	rt.Presentation = &agentprofiles.PresentationBinding{Kind: "document.file"}
	open := build(t, openFileFactory(url, false), rt)
	// The fake harness doesn't implement the presentations-persist endpoint,
	// so this can't assert full success end-to-end — only that the
	// existence check (the part this test targets) no longer rejects a
	// binary file as "no file at ...".
	if _, err := open.Execute(ctx, map[string]interface{}{"path": "image-demo/owl.png"}); err != nil && strings.Contains(err.Error(), "no file") {
		t.Fatalf("open_file's existence check must accept a binary file, not just text: %v", err)
	}
}

func TestInboxNoteNamesTheUnfiledUploads(t *testing.T) {
	if got := InboxNote(nil); got != "" {
		t.Fatalf("empty inbox produced a note: %q", got)
	}
	got := InboxNote([]string{"_users/u1/Chats/SparkQuill/inbox/worksheet.pdf", "_users/u1/Chats/SparkQuill/inbox/photo.jpg"})
	for _, want := range []string{"2 unfiled upload(s)", "worksheet.pdf", "photo.jpg", "process-file", "otherwise leave it for later"} {
		if !strings.Contains(got, want) {
			t.Fatalf("inbox note %q lacks %q", got, want)
		}
	}
}

func TestParseFolderListingAcceptsTheDocumentsAPIShapes(t *testing.T) {
	cases := map[string]string{
		"bare array":     `[{"filepath":"inbox/a.pdf","type":"file"}]`,
		"data wrapper":   `{"success":true,"data":[{"filepath":"inbox/a.pdf","type":"file"}]}`,
		"single folder":  `{"filepath":"inbox","type":"folder","children":[{"filepath":"inbox/a.pdf","type":"file"}]}`,
		"wrapped folder": `{"data":{"filepath":"inbox","type":"folder","children":[{"filepath":"inbox/a.pdf","type":"file"}]}}`,
	}
	for name, raw := range cases {
		got := parseFolderListing([]byte(raw))
		if len(got) != 1 || got[0].FilePath != "inbox/a.pdf" {
			t.Fatalf("%s: parsed %+v", name, got)
		}
	}
}

func TestPinAndUnpinPageKeepTheAppsStateFile(t *testing.T) {
	fake, sink, rt, url := newToolHarness(t)
	ctx := context.Background()
	pin := build(t, pinPageFactory(url), rt)
	unpin := build(t, unpinPageFactory(url), rt)
	if _, err := pin.Execute(ctx, map[string]interface{}{"path": "pages/exams.html", "title": "Exams"}); err == nil {
		t.Fatal("pinning a page that does not exist must fail")
	}
	fake.files["_users/u1/Chats/SparkQuill/pages/exams.html"] = "<h1>Exams</h1>"
	if _, err := pin.Execute(ctx, map[string]interface{}{"path": "_users/u1/Chats/SparkQuill/pages/exams.html", "title": "Exams"}); err != nil {
		t.Fatal(err)
	}
	out, err := pin.Execute(ctx, map[string]interface{}{"path": "pages/exams.html", "title": "Term exams"})
	if err != nil || !strings.Contains(out, `"pins":1`) {
		t.Fatalf("re-pinning must rename, not duplicate: %s %v", out, err)
	}
	var st pinsState
	if err := json.Unmarshal([]byte(fake.files["_users/u1/Chats/SparkQuill/state/pins.json"]), &st); err != nil {
		t.Fatal(err)
	}
	if st.Key != "pins" || len(st.Data.Pins) != 1 || st.Data.Pins[0].Path != "pages/exams.html" || st.Data.Pins[0].Title != "Term exams" {
		t.Fatalf("pins state = %+v", st)
	}
	if _, err := unpin.Execute(ctx, map[string]interface{}{"path": "pages/other.html"}); err == nil {
		t.Fatal("unpinning something not pinned must fail")
	}
	if _, err := unpin.Execute(ctx, map[string]interface{}{"path": "pages/exams.html"}); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(fake.files["_users/u1/Chats/SparkQuill/state/pins.json"]), &st); err != nil || len(st.Data.Pins) != 0 {
		t.Fatalf("pins after unpin = %+v err=%v", st, err)
	}
	if k := sink.kinds(); len(k) != 3 || k[0] != "pins_updated" {
		t.Fatalf("events = %v", k)
	}
}

func TestSweepAnswerKeysMovesStrayKeysOutOfEveryActivity(t *testing.T) {
	fake, _, rt, url := newToolHarness(t)
	fake.files["_users/u1/Chats/SparkQuill/activities/2026-09-01-old/old.html"] = "<html>"
	fake.files["_users/u1/Chats/SparkQuill/activities/2026-09-01-old/old-KEY.md"] = "k1"
	fake.files["_users/u1/Chats/SparkQuill/activities/2026-09-01-old/extra-KEY.md"] = "k2"
	fake.files["_users/u1/Chats/SparkQuill/activities/2026-09-02-new/activity.json"] = "{}"
	ws := newFamilyWorkspace(url, rt, rt.WorkspacePath)
	if n := ws.sweepAnswerKeys(context.Background()); n != 2 {
		t.Fatalf("moved %d keys, want 2: %v", n, fake.files)
	}
	for _, left := range []string{"_users/u1/Chats/SparkQuill/activities/2026-09-01-old/old-KEY.md", "_users/u1/Chats/SparkQuill/activities/2026-09-01-old/extra-KEY.md"} {
		if _, still := fake.files[left]; still {
			t.Fatalf("%s must leave the activity folder", left)
		}
	}
	got := 0
	for fp := range fake.files {
		if strings.HasPrefix(fp, "_users/u1/Chats/SparkQuill/keys/2026-09-01-old-") && strings.HasSuffix(fp, "KEY.md") {
			got++
		}
	}
	if got != 2 {
		t.Fatalf("both keys must land under keys/ for the activity: %v", fake.files)
	}
	if ws.sweepAnswerKeys(context.Background()) != 0 {
		t.Fatal("a second sweep must find nothing")
	}
}
