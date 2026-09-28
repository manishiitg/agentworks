package server

import (
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func protectManagedCodingAgentProjectionWrites(sessionID, workspaceRoot string) {
	common.ProtectCodingAgentProjectionWrites(sessionID, workspaceRoot)
}
