package llmtypes

import (
	"context"
	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

// NativeGoal is provider-owned state, not a host instruction to keep starting
// turns. Optional counters remain unknown when the native program omits them.
type NativeGoal struct {
	Objective       string `json:"objective"`
	Status          string `json:"status"`
	TokensUsed      *int64 `json:"tokens_used,omitempty"`
	TokenBudget     *int64 `json:"token_budget,omitempty"`
	TimeUsedSeconds *int64 `json:"time_used_seconds,omitempty"`
	Checks          *int   `json:"checks,omitempty"`
	LastCheck       string `json:"last_check,omitempty"`
}

type NativeGoalUpdate struct {
	Provider        string      `json:"provider"`
	NativeSessionID string      `json:"native_session_id"`
	Goal            *NativeGoal `json:"goal"`
	Reason          string      `json:"reason,omitempty"`
}

type NativeGoalCommand struct {
	SessionID string `json:"session_id"`
	Action    string `json:"action"`
	Objective string `json:"objective,omitempty"`
}

type nativeGoalCommandKey struct{}

// The host installs only an authenticated person's explicit command. Context
// keeps it out of saved prompts and out of instruction/launch fingerprints.
func WithNativeGoalCommand(ctx context.Context, command NativeGoalCommand) context.Context {
	return context.WithValue(ctx, nativeGoalCommandKey{}, command)
}

func NativeGoalCommandFromContext(ctx context.Context) (NativeGoalCommand, bool) {
	command, ok := ctx.Value(nativeGoalCommandKey{}).(NativeGoalCommand)
	return command, ok
}

// A parent may delegate using its context. Its goal command must never
// become a child's command or change the child's retry policy.
func NativeGoalCommandForSession(ctx context.Context, sessionID string) (NativeGoalCommand, bool) {
	command, ok := NativeGoalCommandFromContext(ctx)
	return command, ok && sessionID != "" && command.SessionID == sessionID
}

type NativeGoalUnavailableError struct{ Program string }

func (e *NativeGoalUnavailableError) Error() string {
	return "Goal mode is not available for " + e.Program + "."
}

// Wrap once so normal provider classification preserves the typed cause and
// never retries or translates an unavailable feature into another program.
func NativeGoalUnavailable(program string) error {
	return &llmerrors.Error{Kind: llmerrors.KindInvalidRequest, Provider: program, Err: &NativeGoalUnavailableError{Program: program}}
}

const StreamChunkTypeNativeGoal StreamChunkType = "native_goal"

func NativeGoalChunk(update NativeGoalUpdate) StreamChunk {
	return StreamChunk{Type: StreamChunkTypeNativeGoal, NativeGoal: &update}
}

// NativeGoalTurnPause is an authenticated host pause before an ordinary wake
// or post-Stop turn. It is not a goal command and never replaces that input.
type NativeGoalTurnPause struct{ SessionID, Reason string }
type nativeGoalTurnPauseKey struct{}

func WithNativeGoalTurnPause(ctx context.Context, pause NativeGoalTurnPause) context.Context {
	return context.WithValue(ctx, nativeGoalTurnPauseKey{}, pause)
}
func NativeGoalTurnPauseForSession(ctx context.Context, sessionID string) (NativeGoalTurnPause, bool) {
	pause, ok := ctx.Value(nativeGoalTurnPauseKey{}).(NativeGoalTurnPause)
	return pause, ok && sessionID != "" && pause.SessionID == sessionID
}
