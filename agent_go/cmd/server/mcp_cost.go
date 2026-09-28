package server

import (
	"log"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

// recordMCPBridgeCall counts a real MCP endpoint invocation. These requests
// run outside the agent event stream in code execution mode. The MCP service
// did not report a charge, so this event has unknown cost rather than a
// fabricated zero-dollar price. The LLM's token usage is recorded separately.
func (api *StreamingAPI) recordMCPBridgeCall(sessionID, server, tool string) {
	if api == nil || api.costLedger == nil || strings.TrimSpace(server) == "" {
		return
	}
	entry := costledger.Entry{
		Timestamp:    time.Now().UTC(),
		SessionID:    sessionID,
		Scope:        "tool",
		Component:    "mcp:" + strings.TrimSpace(server),
		ToolName:     strings.TrimSpace(tool),
		BillingBasis: "unpriced",
	}
	if active, ok := api.getActiveSession(sessionID); ok {
		entry.UserID = active.UserID
		entry.WorkflowID = active.WorkspacePath
		entry.SourcePlatform = active.BotPlatform
	}
	if err := api.costLedger.Append(entry); err != nil {
		log.Printf("[COST_LEDGER] Failed to record MCP call %s/%s: %v", server, tool, err)
	}
}
