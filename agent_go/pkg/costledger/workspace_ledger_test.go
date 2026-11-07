package costledger

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceLedgerPathScopesToWorkflowFolderOnly(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())

	cases := []struct {
		name          string
		workspacePath string
		wantEmpty     bool
	}{
		{"workflow path", "Workflow/social-media", false},
		{"workflow path with slashes", "/Workflow/social-media/", false},
		{"empty", "", true},
		{"plain chat", "Chats", true},
		{"per-user chats", "_users/default/Chats", true},
		{"not actually under Workflow/", "WorkflowSomethingElse", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := WorkspaceLedgerPath(tc.workspacePath)
			if tc.wantEmpty && got != "" {
				t.Fatalf("WorkspaceLedgerPath(%q) = %q, want empty (not a workflow folder)", tc.workspacePath, got)
			}
			if !tc.wantEmpty && got == "" {
				t.Fatalf("WorkspaceLedgerPath(%q) = empty, want a resolved path", tc.workspacePath)
			}
			if !tc.wantEmpty && filepath.Base(got) != "costs.sqlite" {
				t.Fatalf("WorkspaceLedgerPath(%q) = %q, want it to end in costs/costs.sqlite", tc.workspacePath, got)
			}
		})
	}
}

func TestWorkspaceLedgerOpensUnderTheWorkflowsOwnFolderAndIsReused(t *testing.T) {
	for _, bound := range []bool{false, true} {
		t.Run(fmt.Sprint(bound), func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("WORKSPACE_DOCS_PATH", root)
			t.Cleanup(resetWorkspaceLedgersForTest)
			t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
			if bound {
				state := os.Getenv("AGENTWORKS_STATE_ROOT")
				os.MkdirAll(filepath.Join(state, "ownership/crew-executables"), 0700)
				os.MkdirAll(filepath.Join(root, "Crew/example"), 0700)
				os.MkdirAll(filepath.Join(root, "_system/crew-data/crew"), 0700)
				os.WriteFile(filepath.Join(state, "ownership/projects.json"), []byte(`{"projects":{"work/example":{"product":"work","folder":"example","project_id":"crew","owner_id":"owner"}}}`), 0600)
				raw, _ := json.Marshal(map[string]string{"id": "sequence", "crew_id": "crew", "crew_root": "Crew/example", "workspace_path": "Workflow/social-media", "kind": "workflow"})
				key := fmt.Sprintf("%x.json", sha256.Sum256([]byte("Workflow/social-media")))
				os.WriteFile(filepath.Join(state, "ownership/crew-executables", key), raw, 0600)
			}

			ledger, err := WorkspaceLedger("Workflow/social-media")
			if err != nil {
				t.Fatalf("WorkspaceLedger: %v", err)
			}
			if ledger == nil {
				t.Fatal("expected a non-nil ledger for a real workflow path")
			}

			wantPath := filepath.Join(root, "Workflow", "social-media", "costs", "costs.sqlite")
			if bound {
				wantPath = filepath.Join(root, "_system/crew-data/crew/workflows/sequence/costs/costs.sqlite")
			}
			if _, err := os.Stat(wantPath); err != nil {
				t.Fatalf("expected the ledger file at %s, stat error: %v", wantPath, err)
			}

			again, err := WorkspaceLedger("Workflow/social-media")
			if err != nil {
				t.Fatalf("WorkspaceLedger (second call): %v", err)
			}
			if again != ledger {
				t.Fatal("expected the same *Ledger instance to be reused for the same workspace path, got a different one")
			}
			if err := ledger.Append(Entry{WorkflowID: "Workflow/social-media", ExecutionID: "moved-cost", Scope: "workflow_execution", LLMCallCount: 1, TotalCostUSD: 1.23}); err != nil {
				t.Fatal(err)
			}
			summary, err := ledger.SummarizeWorkflow("Workflow/social-media")
			if err != nil || summary == nil || summary.Total.TotalCostUSD != 1.23 {
				t.Fatal("moved cost missing", summary, err)
			}
			if bound {
				if _, err := os.Stat(filepath.Join(root, "Workflow/social-media")); !os.IsNotExist(err) {
					t.Fatal("a cost event recreated the old workflow folder")
				}
			}

		})
	}
}

func TestWorkspaceLedgerIsNilForNonWorkflowPaths(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Cleanup(resetWorkspaceLedgersForTest)

	ledger, err := WorkspaceLedger("")
	if err != nil {
		t.Fatalf("WorkspaceLedger(\"\"): %v", err)
	}
	if ledger != nil {
		t.Fatal("expected a nil ledger for an empty workspace path")
	}

	ledger, err = WorkspaceLedger("_users/default/Chats")
	if err != nil {
		t.Fatalf("WorkspaceLedger(chats path): %v", err)
	}
	if ledger != nil {
		t.Fatal("expected a nil ledger for a non-workflow path")
	}
}

// PLAT-184 isolation. Two different workflows' ledgers must be two
// genuinely separate files -- an entry appended to one must never be
// readable from the other. This is the property the whole migration exists
// to guarantee: a Social Media Pulse pass must not be able to see Upwork's
// cost data, or vice versa.
func TestWorkspaceLedgersForDifferentWorkflowsAreIsolated(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Cleanup(resetWorkspaceLedgersForTest)

	socialMedia, err := WorkspaceLedger("Workflow/social-media")
	if err != nil || socialMedia == nil {
		t.Fatalf("WorkspaceLedger(social-media): ledger=%v err=%v", socialMedia, err)
	}
	upwork, err := WorkspaceLedger("Workflow/upwork")
	if err != nil || upwork == nil {
		t.Fatalf("WorkspaceLedger(upwork): ledger=%v err=%v", upwork, err)
	}
	if socialMedia == upwork {
		t.Fatal("two different workflows resolved to the same *Ledger instance")
	}

	if err := socialMedia.Append(Entry{
		WorkflowID:   "Workflow/social-media",
		ExecutionID:  "exec-social-media-only",
		Scope:        "workflow_execution",
		LLMCallCount: 1,
		TotalCostUSD: 1.23,
	}); err != nil {
		t.Fatalf("Append to social-media ledger: %v", err)
	}

	socialMediaSummary, err := socialMedia.SummarizeWorkflow("Workflow/social-media")
	if err != nil {
		t.Fatalf("SummarizeWorkflow(social-media): %v", err)
	}
	if socialMediaSummary == nil || socialMediaSummary.Total.TotalCostUSD <= 0 {
		t.Fatalf("expected the social-media ledger to see its own entry, got %+v", socialMediaSummary)
	}

	upworkSummary, err := upwork.SummarizeWorkflow("Workflow/social-media")
	if err != nil {
		t.Fatalf("SummarizeWorkflow against upwork ledger: %v", err)
	}
	if upworkSummary != nil && upworkSummary.Total.TotalCostUSD > 0 {
		t.Fatalf("Upwork's ledger file can see Social Media's entry -- isolation broken, got %+v", upworkSummary)
	}
}

func resetWorkspaceLedgersForTest() {
	workspaceLedgersMu.Lock()
	for _, ledger := range workspaceLedgers {
		_ = ledger.Close()
	}
	workspaceLedgers = make(map[string]*Ledger)
	workspaceLedgersMu.Unlock()
}
