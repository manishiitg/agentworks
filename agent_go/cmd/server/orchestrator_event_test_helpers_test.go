package server

import (
	"context"

	mcpagentevents "github.com/manishiitg/mcpagent/events"
)

type discardEvents struct{}

func (discardEvents) HandleEvent(context.Context, *mcpagentevents.AgentEvent) error { return nil }
func (discardEvents) Name() string                                                  { return "discard" }
