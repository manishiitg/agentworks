package agycli

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/acpcli"
)

// agyGoal is one goal turn: agy's own /goal command runs until the goal is finished. agy has no goal API (no
// pause, resume or read), so AgentWorks keeps the goal's state: active while the /goal turn runs, complete when it
// ends, paused when it is stopped or fails. Start again (resume) sends /goal with the same objective again in the
// same conversation. Show, pause and clear never reach agy; the server answers them.
type agyGoal struct {
	owner, objective, native string
	started                  time.Time
	emit                     func(llmtypes.StreamChunk)
}

// agyGoalTurn reads the chat's admitted goal command for this turn. A set or resume returns the messages with
// `/goal <objective>` as the turn's only new message.
func agyGoalTurn(ctx context.Context, owner, native string, messages []llmtypes.MessageContent, opts *llmtypes.CallOptions) ([]llmtypes.MessageContent, *agyGoal, error) {
	command, ok := llmtypes.NativeGoalCommandForSession(ctx, owner)
	if !ok {
		return messages, nil, nil
	}
	if command.Action != "set" && command.Action != "resume" {
		return nil, nil, fmt.Errorf("Antigravity can't %s a goal from a turn", command.Action)
	}
	objective := strings.TrimSpace(command.Objective)
	if objective == "" {
		return nil, nil, fmt.Errorf("No goal is set")
	}
	if offers, known := acpcli.AgentOffersCommand(owner, agyACPAgent, "goal"); known && !offers {
		log.Printf("[AGENT_GOAL] session=%s provider=agy-cli action=skipped reason=goal_command_not_offered", owner)
		return nil, nil, llmtypes.NativeGoalUnavailable("this Antigravity version")
	}
	// Earlier history stays; the person's pending /goal text is replaced by agy's own command.
	end := len(messages)
	for end > 0 && messages[end-1].Role == llmtypes.ChatMessageTypeHuman {
		end--
	}
	out := append(append([]llmtypes.MessageContent{}, messages[:end]...), llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "/goal " + objective}}})
	goal := &agyGoal{owner: owner, objective: objective, native: native, started: time.Now(), emit: func(chunk llmtypes.StreamChunk) {
		if opts.StreamChan == nil {
			return
		}
		// Not tied to the turn's context: a stopped goal turn still records that it stopped.
		select {
		case opts.StreamChan <- chunk:
		case <-time.After(5 * time.Second):
		}
	}}
	goal.record("active", "intent_"+command.Action, false)
	return out, goal, nil
}

// finish records how the /goal turn ended: complete when agy ended it, paused when it was stopped or failed.
func (g *agyGoal) finish(ctx context.Context, resp *llmtypes.ContentResponse, err error) {
	if handle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(resp); ok && handle.NativeSessionID != "" {
		g.native = handle.NativeSessionID
	}
	status, reason := "complete", "native_turn_finished"
	if err != nil || ctx.Err() != nil {
		status, reason = "paused", "native_turn_ended_early"
	}
	g.record(status, reason, true)
}

func (g *agyGoal) record(status, reason string, timed bool) {
	goal := &llmtypes.NativeGoal{Objective: g.objective, Status: status}
	if timed {
		seconds := int64(time.Since(g.started).Seconds())
		goal.TimeUsedSeconds = &seconds
	}
	log.Printf("[AGENT_GOAL] session=%s provider=agy-cli native=%s action=recorded status=%s reason=%s", g.owner, g.native, status, reason)
	g.emit(llmtypes.NativeGoalChunk(llmtypes.NativeGoalUpdate{Provider: "agy-cli", NativeSessionID: g.native, Goal: goal, Reason: reason}))
}
