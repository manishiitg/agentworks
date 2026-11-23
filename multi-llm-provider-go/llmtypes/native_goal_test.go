package llmtypes

import (
	"context"
	"testing"
)

// A parent's explicit goal must not be inherited as a delegated child's
// native control or retry policy (PLAT-746).
func TestNativeGoalCommandIsScopedToItsOwnSession(t *testing.T) {
	ctx := WithNativeGoalCommand(context.Background(), NativeGoalCommand{SessionID: "parent", Action: "set", Objective: "finish task"})
	if _, ok := NativeGoalCommandForSession(ctx, "parent"); !ok {
		t.Fatal("own goal command missing")
	}
	pauseCtx := WithNativeGoalTurnPause(context.Background(), NativeGoalTurnPause{SessionID: "parent", Reason: "background_wake"})
	if _, ok := NativeGoalTurnPauseForSession(pauseCtx, "parent"); !ok {
		t.Fatal("own pause missing")
	}
	for _, session := range []string{"child", ""} {
		if _, ok := NativeGoalTurnPauseForSession(pauseCtx, session); ok {
			t.Fatal("goal wake pause crossed session boundary")
		}
		if _, ok := NativeGoalCommandForSession(ctx, session); ok {
			t.Fatal("goal command crossed session boundary")
		}
	}
}
