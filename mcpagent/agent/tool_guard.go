package mcpagent

import (
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/toolguard"
)

// guardCall describes one loop tool call for the application's tool guard.
// llmName is the name the model used; mcpName is the server's own name for an
// MCP tool.
func (a *Agent) guardCall(llmName, mcpName, serverName string, isCustom bool, client mcpclient.ClientInterface, args map[string]interface{}) toolguard.Call {
	call := toolguard.Call{SessionID: a.sessionID, Tool: llmName, Args: args}
	switch {
	case isVirtualTool(llmName):
		call.Kind = toolguard.KindVirtual
	case isCustom:
		call.Kind = toolguard.KindCustom
	default:
		call.Kind = toolguard.KindMCP
		call.Server = serverName
		call.Tool = mcpName
		var lister toolguard.Lister
		if client != nil {
			lister = client
		}
		call.Annotations = toolguard.AnnotationsFrom(lister, mcpName)
	}
	return call
}
