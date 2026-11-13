package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
)

// A chat's native cost counting runs only around its turns. Before this, every
// chat that ever used a native CLI kept a 1-second poller forever and every
// checkpoint restarted at server start (139 on one server, 515 on a laptop).
func TestNativeCostCountingRunsOnlyAroundTurnsAndResumesFromCheckpoint(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	oldIdle := nativeCostIdleStop
	nativeCostIdleStop = 50 * time.Millisecond
	t.Cleanup(func() { nativeCostIdleStop = oldIdle })
	t.Cleanup(func() { costobserver.SetNativeAccountingFactory(nil) })

	newAPI := func() *StreamingAPI { return &StreamingAPI{costLedger: costledger.NewLedger("http://127.0.0.1:1")} }
	floor := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	base := costledger.Entry{SessionID: "chat-1", UserID: "user-1", Timestamp: floor}

	api := newAPI()
	acc := api.nativeCostAccounting(base, "claude-code").(*nativeCostAccountant)
	running := func(a *nativeCostAccountant) bool { a.mu.Lock(); defer a.mu.Unlock(); return a.running }
	waitStopped := func(a *nativeCostAccountant) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for running(a) {
			if time.Now().After(deadline) {
				t.Fatal("counting still running after the turn ended and the idle wait passed")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !running(acc) {
		t.Fatal("turn started but counting is not running")
	}
	waitStopped(acc)
	s, ok := readNativeCostCheckpoint(acc.file)
	if !ok || s.Active {
		t.Fatalf("checkpoint after stop: ok=%v active=%v, want kept and inactive", ok, s.Active)
	}

	// The next turn resumes the same accountant from its saved floor.
	acc.Begin(costledger.Entry{SessionID: "chat-1", UserID: "user-1", Timestamp: time.Now()})
	if !running(acc) {
		t.Fatal("next turn did not resume counting")
	}
	if !acc.state.Floor.Equal(floor) {
		t.Fatalf("floor moved to %v, want %v (records before it would be missed or counted twice)", acc.state.Floor, floor)
	}
	waitStopped(acc)

	// A restart starts nothing for an idle chat; its next turn loads the checkpoint.
	api.stopNativeCostAccounting()
	restarted := newAPI()
	restarted.initNativeCostAccounting()
	if n := len(restarted.nativeCosts); n != 0 {
		t.Fatalf("restart started %d accountants, want 0", n)
	}
	again := restarted.nativeCostAccounting(costledger.Entry{SessionID: "chat-1", UserID: "user-1", Timestamp: time.Now()}, "claude-code").(*nativeCostAccountant)
	if !again.state.Floor.Equal(floor) {
		t.Fatalf("after restart floor = %v, want checkpoint floor %v", again.state.Floor, floor)
	}

	// Counting still running at shutdown restarts with the server.
	restarted.stopNativeCostAccounting()
	third := newAPI()
	third.initNativeCostAccounting()
	if n := len(third.nativeCosts); n != 1 {
		t.Fatalf("restart with a running chat started %d accountants, want 1", n)
	}
	third.stopNativeCostAccounting()
	if _, err := os.Stat(filepath.Join(os.Getenv("AGENTWORKS_STATE_ROOT"), "native-cost")); err != nil {
		t.Fatalf("checkpoint folder: %v", err)
	}
}
