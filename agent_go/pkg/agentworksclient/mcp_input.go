package agentworksclient

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"
)

type MCPInputService interface {
	MCPInputCall(context.Context, string, map[string]any, mcp.CallToolRequest, *mcp.ElicitationCapability) *mcp.CallToolResult
}

func MCPURLSignInCandidate(target string, args map[string]any) bool {
	var account map[string]any
	switch target {
	case "workflow_google_account", "workflow_slack_account":
		account, _ = args["account"].(map[string]any)
	case "settings":
		if args["action"] != "google_account" && args["action"] != "slack_account" {
			return false
		}
		account, _ = args["account"].(map[string]any)
	case "crew_integrations":
		if args["action"] != "manage_google_account" && args["action"] != "manage_slack_account" {
			return false
		}
		account, _ = args["arguments"].(map[string]any)
	case "manage_vault_access":
		return args["operation"] == "connect_server" || args["operation"] == "sign_in_connection"
	case "vault":
		return args["action"] == "access_connect_server" || args["action"] == "access_sign_in_connection"
	default:
		return false
	}
	return account["action"] == "connect" || account["action"] == "reconnect"
}

func (c *Client) MCPInputCall(ctx context.Context, target string, args map[string]any, request mcp.CallToolRequest, capability *mcp.ElicitationCapability) *mcp.CallToolResult {
	raw, err := c.request(ctx, http.MethodPost, "/api/external/v1/mcp/elicitation", struct {
		Target         string                       `json:"target"`
		Arguments      map[string]any               `json:"arguments"`
		RequestState   string                       `json:"requestState"`
		InputResponses map[string]mcp.InputResponse `json:"inputResponses"`
		Capabilities   *mcp.ClientCapabilities      `json:"capabilities"`
	}{target, args, request.Params.RequestState, request.Params.InputResponses, &mcp.ClientCapabilities{Elicitation: capability}}, true)
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	var result mcp.CallToolResult
	if json.Unmarshal(raw, &result) != nil {
		return mcp.NewToolResultError("The sign-in service returned an invalid response.")
	}
	return &result
}
