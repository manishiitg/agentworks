package server

import (
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The agent program's launch or reuse, and the moments inside its turn, go on the message's [TURN_SPEED] lines (#3724).
func init() {
	llmtypes.OnAgentProcessEvent = events.TurnSpeedProcess
	llmtypes.OnAgentTurnStage = events.TurnSpeedStage
}
