package server

import (
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The agent program's launch or reuse goes on the message's [TURN_SPEED] line (#3724).
func init() {
	llmtypes.OnAgentProcessEvent = events.TurnSpeedProcess
}
