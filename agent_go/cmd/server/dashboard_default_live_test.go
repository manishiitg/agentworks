package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Drive shared live reads through the real workspace store: declarations
// change without republishing, while authored and pinned HTML stays saved.
func TestLiveDefaultDashboardTracksOnlyCallablePublishedFunctions(t *testing.T) {
	// Stored output of the previous platform template, before content-first Ask.
	oldPage, err := os.ReadFile("testdata/automatic-dashboard-inline-chat.html")
	if err != nil {
		t.Fatal(err)
	}
	if !unmodifiedDefaultDashboard(string(oldPage)) || !strings.Contains(string(oldPage), "data-report-chat-panel") {
		t.Fatal("the fixture must be an untouched automatic page with the old inline chat")
	}
	for _, published := range []bool{false, true} {
		label := "native"
		if published {
			label = "published-source"
		}
		t.Run(label, func(t *testing.T) {
			f, _ := newExternalCrewCommandFixture(t)
			owner := &UserClaims{UserID: "owner", Username: "owner"}
			reader := &UserClaims{UserID: "other", Username: "other"}
			ctx := context.WithValue(context.Background(), UserContextKey, owner)
			root := externalCommandCrewRoot
			type documentResult struct {
				dashboards.Result
				ResourceID string `json:"resource_id"`
			}
			op := func(action string, args map[string]any) documentResult {
				t.Helper()
				if args == nil {
					args = map[string]any{}
				}
				args["workspace"], args["action"], args["dashboard_id"] = root, action, defaultDashboardID
				raw, _ := json.Marshal(args)
				w := httptest.NewRecorder()
				f.api.handleDashboards(w, adminRequest(http.MethodPost, "/api/dashboards", string(raw), owner, nil))
				if w.Code != 200 {
					t.Fatalf("%s: %d %s", action, w.Code, w.Body.String())
				}
				var result documentResult
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				return result
			}
			op("share", map[string]any{"mode": "everyone", "create_dashboard": true})
			created := op("get", nil)
			// Seed historical platform output through the same workspace writer
			// as automatic creation, without the authored-page HTML validator.
			old, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: root, Action: "update", ID: defaultDashboardID, ExpectedRevision: created.Dashboard.Revision, Files: map[string]string{"index.html": string(oldPage)}, Remove: []string{"index.tsx"}, IncludeDraft: true})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: root, Action: "publish", ID: defaultDashboardID, ExpectedRevision: old.Dashboard.Revision, IncludeDraft: true}); err != nil {
				t.Fatal(err)
			}
			before := op("get", nil)
			original := before.Snapshot.Files["index.html"]
			if original != string(oldPage) {
				t.Fatal("the stored snapshot must keep the previous inline-chat template")
			}
			if before.ResourceID == "" {
				t.Fatal("the browser has no dashboard view identity")
			}
			view := func(claims *UserClaims) (int, map[string]any) {
				t.Helper()
				r := mux.SetURLVars(adminRequest(http.MethodGet, "/api/dashboards/"+before.ResourceID, "", claims, nil), map[string]string{"dashboard": before.ResourceID})
				w := httptest.NewRecorder()
				f.api.handleDashboardQuestionView(w, r)
				var out map[string]any
				_ = json.Unmarshal(w.Body.Bytes(), &out)
				return w.Code, out
			}
			livePage := func() string {
				t.Helper()
				status, out := view(reader)
				page, ok := out["live_page"].(map[string]any)
				if status != 200 || !ok || page["document_path"] != dashboards.Document(defaultDashboardID, "") {
					t.Fatalf("browser view lacks the server's automatic page: %d %v", status, out)
				}
				html := page["html"].(string)
				if !strings.Contains(html, `AskButton`) || strings.Contains(html, `ChatPanel`) {
					t.Fatal("the old automatic snapshot must receive the new live button layout")
				}
				return html
			}
			live := dashboards.Document(defaultDashboardID, "")
			read := func(document string, claims *UserClaims) *httptest.ResponseRecorder {
				t.Helper()
				r := adminRequest(http.MethodGet, reportPreviewAPIPrefix+"file?workspace="+url.QueryEscape(root)+"&path="+url.QueryEscape(document), "", claims, nil)
				w := httptest.NewRecorder()
				f.api.handleReportPreviewFile(w, r)
				return w
			}
			write := func(raw string) {
				t.Helper()
				if err := writeFileToWorkspace(ctx, root+"/functions.json", raw); err != nil {
					t.Fatal(err)
				}
			}
			if published {
				record := &publishedProject{Root: root, WorkspaceID: "team", ProfileID: "work", OwnerID: "owner", PublisherID: "owner", ProjectID: "laptop", Revision: "published-functions", SourcePaths: []string{"functions.json"}}
				f.write(t, record.sourceRoot()+"/functions.json", `{"functions":[{"id":"published-reply","name":"published_reply","description":"Published reply","enabled":true,"action":{"type":"message"},"input":{"messages":["Return the reply."]}}]}`)
				f.write(t, root+"/functions.json", `{"functions":[{"id":"unpublished-reply","name":"unpublished_reply","description":"Draft reply","enabled":true,"action":{"type":"message"},"input":{"messages":["Not published."]}}]}`)
				if err := savePublishedProject(record); err != nil {
					t.Fatal(err)
				}
				w := read(live, reader)
				if w.Code != 200 || !strings.Contains(w.Body.String(), `published_reply`) || strings.Contains(w.Body.String(), "unpublished_reply") {
					t.Fatalf("Run page did not use the published function list: %d %s", w.Code, w.Body.String())
				}
				if page := livePage(); !strings.Contains(page, `published_reply`) || strings.Contains(page, "unpublished_reply") {
					t.Fatal("browser view did not exclude the unpublished function")
				}
				return
			}
			feed := livefeed.Default.Subscribe()
			defer livefeed.Default.Unsubscribe(feed)
			write(`{"functions":[{"id":"draft-reply","name":"draft_reply","description":"Draft a reply","enabled":true,"action":{"type":"message"},"input":{"messages":["Return the reply."]}}]}`)
			notices, _ := feed.Drain()
			found := false
			for _, notice := range notices {
				found = found || notice.Kind == livefeed.Report && notice.Workflow == root
			}
			if !found {
				t.Fatalf("declaration save did not notify the open dashboard: %v", notices)
			}
			if w := read(live, reader); w.Code != 200 || !strings.Contains(w.Body.String(), `draft_reply`) {
				t.Fatalf("new function is missing without republish: %d %s", w.Code, w.Body.String())
			}
			if !strings.Contains(livePage(), `draft_reply`) {
				t.Fatal("browser metadata pinned the old page instead of supplying the live function")
			}
			if w := read(dashboards.Document(defaultDashboardID, before.Dashboard.Published), reader); w.Code != 200 || w.Body.String() != original {
				t.Fatalf("a selected publication changed: %d %s", w.Code, w.Body.String())
			}
			bounded := *reader
			bounded.AccessToken = &accesstokens.Token{ID: "snapshot-reader", Scopes: []string{"dashboards:read", "crews:read", "crews:run"}, AllCrews: true, FileGuard: &wf.FolderGuard{ReadPaths: []string{dashboards.Document(defaultDashboardID, before.Dashboard.Published)}}}
			if status, out := view(&bounded); (status != 200 && status != http.StatusNotFound) || out["live_page"] != nil {
				t.Fatalf("a saved-revision grant became a live-page grant: %d %v", status, out)
			}
			boundedContext := context.WithValue(context.Background(), UserContextKey, &bounded)
			if page, automatic, err := f.api.liveDefaultDashboard(boundedContext, &bounded, root, live, []byte(original)); err != nil || automatic || string(page) != original {
				t.Fatalf("the renderer changed a bounded snapshot: automatic=%v error=%v", automatic, err)
			}
			if after := op("get", nil); after.Dashboard.Revision != before.Dashboard.Revision || after.Dashboard.Published != before.Dashboard.Published {
				t.Fatal("a live read wrote or republished the saved dashboard")
			}
			write(`{"functions":[]}`)
			if w := read(live, reader); w.Code != 200 || strings.Contains(w.Body.String(), "draft_reply") {
				t.Fatalf("removed function is still visible: %d %s", w.Code, w.Body.String())
			}
			if strings.Contains(livePage(), "draft_reply") {
				t.Fatal("browser metadata retained a removed function")
			}
			// Keep the old marker deliberately: an edited body still belongs to its author.
			marker := defaultDashboardMarker.FindString(original)
			custom := strings.Replace(defaultDashboardMarker.ReplaceAllString(original, ""), "Ask a question in plain words.", "My own dashboard layout.", 1)
			custom = strings.Replace(custom, "</body>", marker+"\n</body>", 1)
			updated := op("update", map[string]any{"expected_revision": before.Dashboard.Revision, "files": map[string]string{"index.html": custom}})
			op("publish", map[string]any{"expected_revision": updated.Dashboard.Revision})
			write(`{"functions":[{"id":"later-function","name":"later_function","description":"Later action","enabled":true,"action":{"type":"message"},"input":{"messages":["Return a result."]}}]}`)
			if w := read(live, reader); w.Code != 200 || w.Body.String() != custom {
				t.Fatalf("a custom dashboard was changed: %d %s", w.Code, w.Body.String())
			}
			if status, out := view(reader); status != 200 || out["live_page"] != nil {
				t.Fatalf("custom browser view must keep its pinned source: %d %v", status, out)
			}
			op("share", map[string]any{"mode": "private"})
			if status, _ := view(reader); status != http.StatusNotFound {
				t.Fatalf("revoked browser view remained available: %d", status)
			}
			if w := read(live, reader); w.Code != http.StatusForbidden {
				t.Fatalf("a revoked shared link remained readable: %d", w.Code)
			}
		})
	}
}
