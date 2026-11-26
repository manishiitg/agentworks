package acpcli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Prompt steering (#3920): some agents (agy acp 1.5.0+) take a second session/prompt sent while one runs as a
// message into the running turn. They answer the running prompt end_turn at once and carry the work on under the new
// one, leaving running tools and subagents alone. One turn then spans several prompts; only the latest one's answer
// ends it. ACP has no steering method of its own, and session/cancel plus a new prompt would stop running tools.

// steerRequest is one message handed to the running turn.
type steerRequest struct {
	text string
	// params carries the message's pictures, prepared before it reaches the turn; nil for text only.
	params  any
	cleanup func()
	reply   chan error
}

// promptDone is one session/prompt's answer within a turn.
type promptDone struct {
	seq    int
	err    error
	result promptResult
}

// errSteerNotTaken: no running turn could take the message (the caller queues it for the next turn).
func errSteerNotTaken(reason string) error {
	return fmt.Errorf("%w: %s", llmtypes.ErrCodingAgentInputNotSent, reason)
}

// versionAtLeast reports whether a dotted version ("1.5.16") is at least want; an unreadable version is not.
func versionAtLeast(version string, want ...int) bool {
	fields := strings.Split(strings.TrimPrefix(strings.TrimSpace(version), "v"), ".")
	if len(fields) == 0 || fields[0] == "" {
		return false
	}
	for i, w := range want {
		if i >= len(fields) {
			return false
		}
		digits := fields[i]
		if end := strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }); end >= 0 {
			digits = digits[:end]
		}
		n, err := strconv.Atoi(digits)
		if err != nil {
			return false
		}
		if n != w {
			return n > w
		}
	}
	return true
}

// TurnSteerable reports whether this chat runs this agent and a turn that takes messages is running now.
func TurnSteerable(owner, agentID string) bool {
	sessions.Lock()
	s := sessions.m[owner]
	sessions.Unlock()
	if s == nil || s.agent.ID != agentID {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.steer != nil
}

// SessionSteersWithPrompt reports whether this chat runs this agent in a release that takes mid-turn prompts.
// False also when the chat has no session yet.
func SessionSteersWithPrompt(owner, agentID string) bool {
	sessions.Lock()
	defer sessions.Unlock()
	s := sessions.m[owner]
	return s != nil && s.agent.ID == agentID && s.steersWithPrompt
}

// SteerTurn hands a message to this chat's running turn. It returns once the turn has sent it to the agent, or an
// error wrapping llmtypes.ErrCodingAgentInputNotSent when no turn could take it.
func SteerTurn(ctx context.Context, owner, agentID, text string) error {
	if strings.TrimSpace(text) == "" {
		return errSteerNotTaken("message is empty")
	}
	sessions.Lock()
	s := sessions.m[owner]
	sessions.Unlock()
	if s == nil || s.agent.ID != agentID {
		return errSteerNotTaken("no session for this chat")
	}
	s.mu.Lock()
	ch, done := s.steer, s.steerDone
	s.mu.Unlock()
	if ch == nil {
		return errSteerNotTaken("no running turn takes messages")
	}
	req := steerRequest{text: text, cleanup: func() {}, reply: make(chan error, 1)}
	// Pictures go with the steered prompt itself, checked and attached like a turn's first prompt.
	if len(llmtypes.NativeImageInputsForSession(ctx, owner)) > 0 {
		base := map[string]any{"sessionId": s.currentSessionID(), "prompt": []any{map[string]any{"type": "text", "text": text}}}
		params, cleanup, err := paramsWithImages(ctx, s.agent.providerID(), owner, base, nil, s.caps.PromptCapabilities.Image)
		if err != nil {
			return err
		}
		req.params, req.cleanup = params, cleanup
	}
	defer req.cleanup()
	select {
	case ch <- req:
	case <-done:
		return errSteerNotTaken("the turn ended")
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-req.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// addUsage sums the usage of a turn's prompts.
func addUsage(total, u *llmtypes.Usage) *llmtypes.Usage {
	if u == nil {
		return total
	}
	if total == nil {
		copied := *u
		return &copied
	}
	sum := *total
	sum.InputTokens += u.InputTokens
	sum.OutputTokens += u.OutputTokens
	sum.TotalTokens += u.TotalTokens
	add := func(a, b *int) *int {
		if a == nil && b == nil {
			return nil
		}
		n := 0
		if a != nil {
			n += *a
		}
		if b != nil {
			n += *b
		}
		return &n
	}
	sum.CacheTokens = add(total.CacheTokens, u.CacheTokens)
	sum.ReasoningTokens = add(total.ReasoningTokens, u.ReasoningTokens)
	return &sum
}

// promptUsage is one prompt answer's usage.
func promptUsage(result promptResult) *llmtypes.Usage {
	u := result.Usage
	if u == nil {
		return nil
	}
	cached, reasoning := u.CachedRead, u.Thought
	return &llmtypes.Usage{InputTokens: u.Input, OutputTokens: u.Output, TotalTokens: u.Total, CacheTokens: &cached, ReasoningTokens: &reasoning}
}
