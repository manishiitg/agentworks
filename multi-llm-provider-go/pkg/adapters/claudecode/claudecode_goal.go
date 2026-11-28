package claudecode

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Claude Code's own /goal (#4626): `/goal <condition>` sets a goal on the native session, and after every stop
// Claude checks it; an unmet check continues the turn with "Stop hook feedback:\n[<condition>]: <why>". A met
// goal clears itself; `/goal clear` clears it. The goal stays on the session when a turn is stopped.
//
// As with Antigravity, AgentWorks keeps the chat's goal state: a goal runs only during its /goal turn (set, or
// Start again), so a stopped goal never carries on behind an ordinary message. Before any other turn, a goal
// still set on the native session is cleared with `/goal clear`, a local command that calls no model.
const (
	claudeGoalSetPrefix     = "Goal set: "
	claudeGoalClearedPrefix = "Goal cleared"
	claudeGoalActivePrefix  = "Goal active: "
	claudeGoalNonePrefix    = "No goal set"
	claudeGoalClearCommand  = "/goal clear"
)

// claudeNativeGoals: whether a chat's native Claude session still has a goal set, as far as this process knows.
var claudeNativeGoals sync.Map // owner -> bool

type claudeGoalClearOnlyKey struct{}

type claudeGoal struct {
	owner, objective, native string
	started                  time.Time
	checks                   int
	emit                     func(llmtypes.StreamChunk)
}

// claudeGoalTurn reads the chat's goal command for this turn. A set or resume makes `/goal <objective>` the
// turn's prompt; other goal actions are the server's and never reach Claude.
func claudeGoalTurn(ctx context.Context, owner, native string, opts *llmtypes.CallOptions) (*claudeGoal, string, error) {
	command, ok := llmtypes.NativeGoalCommandForSession(ctx, owner)
	if !ok {
		return nil, "", nil
	}
	if command.Action != "set" && command.Action != "resume" {
		return nil, "", fmt.Errorf("Claude Code can't %s a goal from a turn", command.Action)
	}
	objective := strings.Join(strings.Fields(command.Objective), " ")
	if objective == "" {
		return nil, "", fmt.Errorf("No goal is set")
	}
	goal := &claudeGoal{owner: owner, objective: objective, native: native, started: time.Now(), emit: func(chunk llmtypes.StreamChunk) {
		if opts == nil || opts.StreamChan == nil {
			return
		}
		// Not tied to the turn's context: a stopped goal turn still records that it stopped.
		select {
		case opts.StreamChan <- chunk:
		case <-time.After(5 * time.Second):
		}
	}}
	claudeNativeGoals.Store(owner, true)
	goal.record("active", "intent_"+command.Action, false)
	return goal, "/goal " + objective, nil
}

// check records one of Claude's unmet goal checks.
func (g *claudeGoal) check(reason string) {
	g.checks++
	g.record("active", "native_check", true, strings.TrimSpace(reason))
}

// finish records how the /goal turn ended. Claude ends a goal turn when its check finds the goal met, so an
// answered turn is done unless the transcript still shows the goal set; a stopped or failed turn is stopped.
func (g *claudeGoal) finish(ctx context.Context, err error, native, workingDir, home string) {
	if native != "" {
		g.native = native
	}
	status, reason := "complete", "native_goal_met"
	switch {
	case err != nil || ctx.Err() != nil:
		status, reason = "paused", "native_turn_ended_early"
	case claudeTranscriptGoalSet(g.native, workingDir, home) == claudeTranscriptGoalActive:
		status, reason = "paused", "native_turn_ended_unmet"
	default:
		claudeNativeGoals.Store(g.owner, false)
	}
	g.record(status, reason, true)
}

func (g *claudeGoal) record(status, reason string, timed bool, lastCheck ...string) {
	goal := &llmtypes.NativeGoal{Objective: g.objective, Status: status}
	if timed {
		seconds := int64(time.Since(g.started).Seconds())
		goal.TimeUsedSeconds = &seconds
	}
	if g.checks > 0 {
		checks := g.checks
		goal.Checks = &checks
	}
	if len(lastCheck) > 0 {
		goal.LastCheck = lastCheck[0]
	}
	log.Printf("[AGENT_GOAL] session=%s provider=claude-code native=%s action=recorded status=%s reason=%s checks=%d", g.owner, g.native, status, reason, g.checks)
	g.emit(llmtypes.NativeGoalChunk(llmtypes.NativeGoalUpdate{Provider: "claude-code", NativeSessionID: g.native, Goal: goal, Reason: reason}))
}

// claudeGoalCheckReason returns the reason of one of Claude's goal checks, from a Stop hook feedback message.
func claudeGoalCheckReason(ev claudeStreamEvent) (string, bool) {
	feedback, ok := claudeSyntheticStopFeedback(ev)
	if !ok || !claudeGoalCheckFeedback.MatchString(feedback) {
		return "", false
	}
	return claudeGoalCheckFeedback.ReplaceAllString(feedback, ""), true
}

// claudeGoalNotice reports Claude's own reply to a /goal command (a <synthetic> message such as "Goal set: …"),
// which the goal bar shows instead of the reply.
func claudeGoalNotice(ev claudeStreamEvent) bool {
	if ev.Type != "assistant" || ev.Message == nil || ev.Message.Model != "<synthetic>" {
		return false
	}
	for _, block := range ev.Message.Content {
		text := strings.TrimSpace(block.Text)
		if block.Type == "text" && (strings.HasPrefix(text, claudeGoalSetPrefix) || strings.HasPrefix(text, claudeGoalClearedPrefix) || strings.HasPrefix(text, claudeGoalActivePrefix) || strings.HasPrefix(text, claudeGoalNonePrefix)) {
			return true
		}
	}
	return false
}

type claudeTranscriptGoal int

const (
	claudeTranscriptGoalUnknown claudeTranscriptGoal = iota
	claudeTranscriptGoalNone
	claudeTranscriptGoalActive
)

// claudeTranscriptGoalSet reads whether the native session's transcript ends with a goal still set: the last of
// "Goal set", "Goal cleared" and a met goal check decides.
func claudeTranscriptGoalSet(native, workingDir, home string) claudeTranscriptGoal {
	if native == "" {
		return claudeTranscriptGoalUnknown
	}
	file, _, err := openClaudeTranscript(native, workingDir, home)
	if err != nil {
		return claudeTranscriptGoalUnknown
	}
	defer file.Close()
	state := claudeTranscriptGoalNone
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "Goal ") && !strings.Contains(line, `"goal_status"`) {
			continue
		}
		state = claudeTranscriptGoalLine(line, state)
	}
	if scanner.Err() != nil {
		return claudeTranscriptGoalUnknown
	}
	return state
}

func claudeTranscriptGoalLine(line string, state claudeTranscriptGoal) claudeTranscriptGoal {
	switch {
	case strings.Contains(line, `"subtype":"local_command"`) && strings.Contains(line, "<local-command-stdout>"+claudeGoalSetPrefix):
		return claudeTranscriptGoalActive
	case strings.Contains(line, `"subtype":"local_command"`) && strings.Contains(line, "<local-command-stdout>"+claudeGoalClearedPrefix):
		return claudeTranscriptGoalNone
	case strings.Contains(line, "Goal cleared after an unrecoverable error"):
		return claudeTranscriptGoalNone
	case strings.Contains(line, `"type":"goal_status"`) && strings.Contains(line, `"met":true`):
		return claudeTranscriptGoalNone
	}
	return state
}

// claudeNativeGoalLeft reports whether an ordinary turn must clear a goal still set on the native session first.
func claudeNativeGoalLeft(owner, native, workingDir, home string) bool {
	if native == "" {
		return false
	}
	if known, ok := claudeNativeGoals.Load(owner); ok {
		return known.(bool)
	}
	// After a restart nothing is known: the transcript says, once.
	left := claudeTranscriptGoalSet(native, workingDir, home) == claudeTranscriptGoalActive
	claudeNativeGoals.Store(owner, left)
	return left
}

func claudeGoalClearOnly(ctx context.Context) bool {
	only, _ := ctx.Value(claudeGoalClearOnlyKey{}).(bool)
	return only
}

// clearNativeGoal sends `/goal clear` on the chat's session as its own silent turn.
func (c *ClaudeCodeInteractiveAdapter) clearNativeGoal(ctx context.Context, owner string, messages []llmtypes.MessageContent, opts *llmtypes.CallOptions) {
	quiet := *opts
	quiet.StreamChan = nil
	_, err := c.generateContentStructured(context.WithValue(ctx, claudeGoalClearOnlyKey{}, true), messages, &quiet)
	if err != nil {
		log.Printf("[AGENT_GOAL] session=%s provider=claude-code action=failed reason=native_clear err=%v", owner, err)
		return
	}
	claudeNativeGoals.Store(owner, false)
	log.Printf("[AGENT_GOAL] session=%s provider=claude-code action=cleared reason=goal_not_running", owner)
}
