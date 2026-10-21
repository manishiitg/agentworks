package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

// Drive shared live reads through the real workspace store: declarations
// change without republishing, while authored and pinned HTML stays saved.
func TestLiveDefaultDashboardTracksOnlyCallablePublishedFunctions(t *testing.T) {
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
			op := func(action string, args map[string]any) dashboards.Result {
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
				var result dashboards.Result
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				return result
			}
			op("share", map[string]any{"mode": "everyone"})
			before := op("get", nil)
			original := before.Snapshot.Files["index.html"]
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
				f.write(t, record.sourceRoot()+"/functions.json", `{"functions":[{"name":"published_reply","description":"Published reply","instructions":"Return the reply."}]}`)
				f.write(t, root+"/functions.json", `{"functions":[{"name":"unpublished_reply","description":"Draft reply","instructions":"Not published."}]}`)
				if err := savePublishedProject(record); err != nil {
					t.Fatal(err)
				}
				w := read(live, reader)
				if w.Code != 200 || !strings.Contains(w.Body.String(), `data-report-action="published_reply"`) || strings.Contains(w.Body.String(), "unpublished_reply") {
					t.Fatalf("Run page did not use the published function list: %d %s", w.Code, w.Body.String())
				}
				return
			}
			feed := livefeed.Default.Subscribe()
			defer livefeed.Default.Unsubscribe(feed)
			write(`{"functions":[{"name":"draft_reply","description":"Draft a reply","instructions":"Return the reply."}]}`)
			notices, _ := feed.Drain()
			found := false
			for _, notice := range notices {
				found = found || notice.Kind == livefeed.Report && notice.Workflow == root
			}
			if !found {
				t.Fatalf("declaration save did not notify the open dashboard: %v", notices)
			}
			if w := read(live, reader); w.Code != 200 || !strings.Contains(w.Body.String(), `data-report-action="draft_reply"`) {
				t.Fatalf("new function is missing without republish: %d %s", w.Code, w.Body.String())
			}
			if w := read(dashboards.Document(defaultDashboardID, before.Dashboard.Published), reader); w.Code != 200 || w.Body.String() != original {
				t.Fatalf("a selected publication changed: %d %s", w.Code, w.Body.String())
			}
			if after := op("get", nil); after.Dashboard.Revision != before.Dashboard.Revision || after.Dashboard.Published != before.Dashboard.Published {
				t.Fatal("a live read wrote or republished the saved dashboard")
			}
			write(`{"functions":[]}`)
			if w := read(live, reader); w.Code != 200 || strings.Contains(w.Body.String(), "draft_reply") {
				t.Fatalf("removed function is still visible: %d %s", w.Code, w.Body.String())
			}
			// Keep the old marker deliberately: an edited body still belongs to its author.
			marker := defaultDashboardMarker.FindString(original)
			custom := strings.Replace(defaultDashboardMarker.ReplaceAllString(original, ""), "Ask a question in plain words.", "My own dashboard layout.", 1)
			custom = strings.Replace(custom, "</body>", marker+"\n</body>", 1)
			updated := op("update", map[string]any{"expected_revision": before.Dashboard.Revision, "files": map[string]string{"index.html": custom}})
			op("publish", map[string]any{"expected_revision": updated.Dashboard.Revision})
			write(`{"functions":[{"name":"later_function","description":"Later action","instructions":"Return a result."}]}`)
			if w := read(live, reader); w.Code != 200 || w.Body.String() != custom {
				t.Fatalf("a custom dashboard was changed: %d %s", w.Code, w.Body.String())
			}
			op("share", map[string]any{"mode": "private"})
			if w := read(live, reader); w.Code != http.StatusForbidden {
				t.Fatalf("a revoked shared link remained readable: %d", w.Code)
			}
		})
	}
}
