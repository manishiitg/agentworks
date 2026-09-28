package common

import mcpagent "github.com/manishiitg/mcpagent/agent"

func init() {
	mcpagent.BridgeTokenForSession = BridgeTokenForSession
	mcpagent.ProtectManagedProjectionWrites = ProtectCodingAgentProjectionWrites
}
