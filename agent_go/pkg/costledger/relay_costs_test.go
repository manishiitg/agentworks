package costledger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

func TestRelayCostsIncludePublishedVersionsAndKeepVersionScopes(t *testing.T) {
	l, err := NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	draft := "Workflow/relay-costs"
	releases := workflowtypes.RelayReleaseRoot(draft)
	paths := []string{draft, releases + "/v1", releases + "/v2", workflowtypes.RelayReleaseRoot("Workflow/other") + "/v1"}
	for i, p := range paths {
		err := l.Append(Entry{EventID: p, WorkflowID: p, RunID: "iteration-0", Scope: "workflow_execution", Timestamp: time.Date(2026, 9, 28+i, 0, 0, 0, 0, time.UTC), Provider: "openai", ModelID: "test", LLMCallCount: 1, PromptTokens: 10, TotalCostUSD: 1})
		if err != nil {
			t.Fatal(err)
		}
	}
	summary, err := l.SummarizeWorkflow(draft)
	if err != nil || summary.Total.TotalCostUSD != 3 {
		t.Fatalf("draft totals: %+v, %v", summary, err)
	}
	v1, err := l.SummarizeWorkflow(releases + "/v1")
	if err != nil || v1.Total.TotalCostUSD != 1 {
		t.Fatalf("v1 totals: %+v, %v", v1, err)
	}
	page, more, err := l.SummarizeWorkflowOverview(draft, "2026-09-30", "2026-09-30")
	if err != nil || !more || page.Total.TotalCostUSD != 3 || len(page.ByDate) != 1 {
		t.Fatalf("overview/pagination: %+v more=%t err=%v", page, more, err)
	}
}

func TestRelayCostCopyRequiresMatchingSourceNamespace(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	draft := "Workflow/relay-costs"
	release := workflowtypes.RelayReleaseRoot(draft) + "/v1"
	if err := os.MkdirAll(filepath.Join(docs, release), 0700); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{draft, "Workflow/other"} {
		raw, _ := json.Marshal(map[string]string{"version": "v1", "source_workspace": source})
		if err := os.WriteFile(filepath.Join(docs, release, "release.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		paths := WorkspaceCostCopies(release)
		want := 1
		if source == draft {
			want = 2
		}
		if len(paths) != want || (want == 2 && paths[1] != draft) {
			t.Fatalf("source=%s copies=%v", source, paths)
		}
	}
}
