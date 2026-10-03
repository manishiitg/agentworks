package browser

import (
	"context"
	"testing"
	"time"
)

func TestBrowserControlBlocksCommandsUntilReleased(t *testing.T) {
	release, ok := TryTakeBrowserControl("test-control")
	if !ok {
		t.Fatal("cannot take control")
	}
	defer release()
	if _, ok := TryTakeBrowserControl("test-control"); ok {
		t.Fatal("second controller admitted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := AcquireBrowserAutomation(ctx, "test-control"); err == nil {
		t.Fatal("command ran during manual control")
	}
	other, ok := TryTakeBrowserControl("other-workflow")
	if !ok {
		t.Fatal("unrelated workflow blocked")
	}
	other()
	release()
	commandDone, err := AcquireBrowserAutomation(context.Background(), "test-control")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := TryTakeBrowserControl("test-control"); ok {
		t.Fatal("control interrupted in-flight command")
	}
	commandDone()
	liveGates.Lock()
	defer liveGates.Unlock()
	if len(liveGates.entries) != 0 {
		t.Fatal("finished gates leaked")
	}
}

func TestWorkspaceViewerSharesCDPCommandLock(t *testing.T) {
	const port = 19231
	BindViewerCDPPort("cdp-viewer-test", port)
	defer BindViewerCDPPort("cdp-viewer-test", 0)
	release, ok := TryTakeWorkspaceBrowserControl("cdp-viewer-test")
	if !ok {
		t.Fatal("cannot take local Chrome control")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if unlock, err := acquireSharedCDPLock(ctx, port); err == nil {
		unlock()
		release()
		t.Fatal("agent CDP command bypassed viewer")
	}
	release()
	unlock, err := acquireSharedCDPLock(context.Background(), port)
	if err != nil {
		t.Fatal(err)
	}
	if release, ok := TryTakeWorkspaceBrowserControl("cdp-viewer-test"); ok {
		release()
		unlock()
		t.Fatal("viewer interrupted agent CDP action")
	}
	unlock()
	release, ok = TryTakeWorkspaceBrowserControl("cdp-viewer-test")
	if !ok {
		t.Fatal("failed CDP lock attempt leaked session gate")
	}
	release()
}

func TestLocalChromeStartupSharesViewerAndAgentLock(t *testing.T) {
	const port = 19232
	release, ok := TryTakeCDPBrowserControl(port)
	if !ok {
		t.Fatal("cannot lock startup")
	}
	if other, ok := TryTakeCDPBrowserControl(port); ok {
		other()
		release()
		t.Fatal("startup bypassed active control")
	}
	release()
	release, ok = TryTakeCDPBrowserControl(port)
	if !ok {
		t.Fatal("startup leaked CDP lock")
	}
	release()
}
