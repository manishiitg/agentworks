package agentworksclient

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// Configuration only: the provider's native Monitor tool starts the consumer.
// It cannot attach a chat, start the daemon, approve tools or expand a lease.
func museMonitorResult(out LocalBridgeReply, broker *museMonitorBroker) (*mcp.CallToolResult, error) {
	result := map[string]any{"monitor_active": out.MonitorActive, "session_id": out.Attachment.SessionID, "attachment_id": out.Attachment.ID}
	if !out.MonitorActive {
		path, err := broker.prepare(out)
		if err != nil {
			return mcp.NewToolResultError("cannot prepare private Muse Monitor metadata pipe"), nil
		}
		exe, err := os.Executable()
		if err != nil {
			return mcp.NewToolResultError("cannot locate installed AgentWorks CLI"), nil
		}
		quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
		result["monitor"] = map[string]any{"command": quote(exe) + " bridge message watch --provider muse --session " + quote(out.Attachment.SessionID) + " --attachment-id " + quote(out.Attachment.ID) + " --subscription-file " + quote(path), "persistent": true, "wake_delay_ms": 0, "show_lines": true, "description": "AgentWorks inbox"}
	}
	raw, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(raw)), nil
}
