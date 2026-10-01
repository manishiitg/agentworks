package workspace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
)

// Dashboard HTML and workflow-DB writes through the workspace tools must
// refresh open Report views; other writes must not.
func TestToolWritesToDashboardOrDBPublishReportNotices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"results":[],"total_rows_affected":1}}`))
	}))
	defer server.Close()
	client := NewClient(server.URL)
	sub := livefeed.Default.Subscribe()
	defer livefeed.Default.Unsubscribe(sub)
	ctx := context.Background()

	if _, err := client.UpdateWorkspaceFile(ctx, UpdateWorkspaceFileParams{Filepath: "Workflow/trader/notes.md", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := sub.Drain(); len(got) != 0 {
		t.Fatalf("non-dashboard write published %v", got)
	}
	if _, err := client.UpdateWorkspaceFile(ctx, UpdateWorkspaceFileParams{Filepath: "Workflow/trader/planning/plan.json", Content: `{"steps":[]}`}); err != nil {
		t.Fatal(err)
	}
	if got, _ := sub.Drain(); !reflect.DeepEqual(got, []livefeed.Notice{{Kind: livefeed.Plan, Workflow: "Workflow/trader"}}) {
		t.Fatalf("plan write notices = %v", got)
	}

	if _, err := client.UpdateWorkspaceFile(ctx, UpdateWorkspaceFileParams{Filepath: "Workflow/trader/db/reports/index.html", Content: "<html>"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.MutateAuthorizedWorkflowDB(ctx, MutateWorkflowDBParams{DBPath: "Workflow/other/db/db.sqlite"}); err != nil {
		t.Fatal(err)
	}
	got, _ := sub.Drain()
	want := []livefeed.Notice{{Kind: livefeed.Report, Workflow: "Workflow/trader"}, {Kind: livefeed.Report, Workflow: "Workflow/other"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("notices = %v, want %v", got, want)
	}
}
