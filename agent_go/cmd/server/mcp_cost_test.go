package server

import (
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

func TestRecordMCPBridgeCallUsesSessionAttributionWithoutInventingPrice(t *testing.T) {
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	api := &StreamingAPI{
		costLedger: ledger,
		activeSessions: map[string]*ActiveSessionInfo{
			"bot-slack--thread": {
				SessionID: "bot-slack--thread", UserID: "bot-slack-route", WorkspacePath: "Workflow/demo", BotPlatform: "slack",
			},
		},
	}
	api.recordMCPBridgeCall("bot-slack--thread", "github", "list_issues")
	summary, err := ledger.Summarize("", "")
	if err != nil {
		t.Fatal(err)
	}
	if summary.ByWorkflowMCP["Workflow/demo"]["github"].Calls != 1 || summary.ByWorkflowMCP["Workflow/demo"]["github"].UnpricedCalls != 1 {
		t.Fatalf("incorrect MCP attribution: %+v", summary.ByWorkflowMCP)
	}
	if summary.ByWorkflowBot["Workflow/demo"]["slack\x00bot-slack-route"] == nil {
		t.Fatalf("missing bot attribution: %+v", summary.ByWorkflowBot)
	}
	if summary.Total.TotalCostUSD != 0 || summary.Total.CallCount != 0 {
		t.Fatalf("MCP activity invented a price or LLM call: %+v", summary.Total)
	}
}
