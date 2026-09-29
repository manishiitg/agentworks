package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/mark3labs/mcp-go/mcp"
)

// externalMCPElicitation adapts the hosted endpoint to the shared function-
// call elicitation logic. Every call it makes is a fresh dispatch through
// handleExternalCall with the current request's credentials, so retries are
// re-authorized; requestState never grants anything. The endpoint is
// stateless (a fresh MCP server per HTTP request), so only modern
// per-request capabilities count, never the legacy session exchange.
func (api *StreamingAPI) externalMCPElicitation(r *http.Request, allowed map[string]externalTool) agentworksclient.FunctionElicitationHost {
	return agentworksclient.FunctionElicitationHost{
		Available: func(tool string) bool { _, ok := allowed[tool]; return ok },
		Call: func(ctx context.Context, tool string, args map[string]any) (json.RawMessage, *mcp.CallToolResult) {
			rec, err := api.externalMCPInvoke(ctx, r, tool, args)
			if err != nil {
				return nil, mcp.NewToolResultError("failed to encode tool arguments: " + err.Error())
			}
			if rec.status < 200 || rec.status >= 300 {
				return nil, externalMCPDispatchResult(rec)
			}
			return json.RawMessage(rec.body.Bytes()), nil
		},
		Render: func(raw json.RawMessage) *mcp.CallToolResult {
			rec := &externalMCPRecorder{header: http.Header{}, status: http.StatusOK}
			rec.body.Write(raw)
			return externalMCPDispatchResult(rec)
		},
	}
}
