package agent

import (
	mcpagent "github.com/manishiitg/mcpagent/agent"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// Coding-agent bridges built here call the shared executor as their own
// session (pkg/common/bridge_token.go). The minter returns "" until the
// server enables session tokens, so other processes keep their own token.
func init() {
	mcpagent.BridgeTokenForSession = common.BridgeTokenForSession
}
