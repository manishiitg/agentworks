package cursorcli

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// /plan: one message in Cursor's plan mode, the next back in the chat's own mode, and a
// bridge's first send states the mode because a resumed agent keeps its saved one.
func TestCursorSDKPlanModePerSend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	script := `const assert=require('assert');const {sendMode}=require('./cursor_sdk_bridge.cjs');
assert.equal(sendMode(undefined,undefined,undefined),'agent');
assert.equal(sendMode(undefined,undefined,'agent'),undefined);
assert.equal(sendMode('plan',undefined,'agent'),'plan');
assert.equal(sendMode('plan',undefined,'plan'),'plan');
assert.equal(sendMode(undefined,undefined,'plan'),'agent');
assert.equal(sendMode(undefined,'plan','plan'),'plan');`
	if output, err := exec.CommandContext(ctx, "node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("Cursor plan mode: %v\n%s", err, output)
	}
}
