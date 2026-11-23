package cursorcli

import (
	"context"
	"errors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/nativeinput"
	"strings"
	"sync"
	"time"
)

var errCursorComposerChanged = errors.New("Cursor composer changed before input delivery")

// The same controls serve both GenerateContent and direct retained-session
// follow-ups. Web approval remains opt-in; no broader approval policy is added.
type cursorRuntimeControls struct {
	autoApproveWebSearch bool
	lastSent             map[string]time.Time
}

func (c *cursorRuntimeControls) handle(ctx context.Context, sessionName, captured string) bool {
	if hasCursorMCPToolApprovalPrompt(captured) {
		key := cursorMCPApprovalKey(ctx, captured)
		// A decision authorizes only the prompt the person saw. Never allow a
		// changed operation, and never use Tab (which persists an allowlist grant).
		samePrompt := func(current string) bool {
			return hasCursorMCPToolApprovalPrompt(current) && strings.TrimSpace(cursorVisiblePaneText(current)) == strings.TrimSpace(cursorVisiblePaneText(captured))
		}
		if ctx.Err() == nil {
			_, _ = sendCursorControlIfVisible(ctx, sessionName, "cursor-mcp-approval-once", samePrompt, key)
		}
		return true
	}
	controls := []struct {
		source   string
		visible  func(string) bool
		key      string
		interval time.Duration
		enabled  bool
	}{
		{"cursor-followups-send-now", hasCursorQueuedFollowupsSendPrompt, "C-m", time.Second, true},
		// Bridge-only child agents do not reliably inherit the scoped bridge.
		{"cursor-mode-switch-reject", hasCursorModeSwitchPrompt, "n", time.Second, true},
		{"cursor-web-approval", hasCursorWebAccessApprovalPrompt, "y", 2 * time.Second, c.autoApproveWebSearch},
	}
	for _, control := range controls {
		if !control.enabled || !control.visible(captured) {
			continue
		}
		if time.Since(c.lastSent[control.source]) >= control.interval {
			// Re-read inside the shared broker: a stale approval must never
			// inject a control key into the user's normal composer.
			if handled, err := sendCursorControlIfVisible(ctx, sessionName, control.source, control.visible, control.key); err == nil && handled {
				if c.lastSent == nil {
					c.lastSent = make(map[string]time.Time)
				}
				c.lastSent[control.source] = time.Now()
			}
		}
		return true
	}
	return false
}

type cursorRetainedControls struct {
	cancel context.CancelFunc
	done   chan struct{}
}

var cursorRetainedControlRegistry = struct {
	sync.Mutex
	sessions map[string]*cursorRetainedControls
}{sessions: make(map[string]*cursorRetainedControls)}

// Called while the provider session is locked, after a successful turn or
// launch-only return. Direct Send calls do not re-enter the response loop.
func startCursorRetainedControls(parent context.Context, sessionName string, autoApproveWebSearch bool) {
	stopCursorRetainedControls(sessionName)
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	watcher := &cursorRetainedControls{cancel: cancel, done: make(chan struct{})}
	cursorRetainedControlRegistry.Lock()
	cursorRetainedControlRegistry.sessions[sessionName] = watcher
	cursorRetainedControlRegistry.Unlock()
	go func() {
		defer func() {
			cancel()
			cursorRetainedControlRegistry.Lock()
			if cursorRetainedControlRegistry.sessions[sessionName] == watcher {
				delete(cursorRetainedControlRegistry.sessions, sessionName)
			}
			cursorRetainedControlRegistry.Unlock()
			close(watcher.done)
		}()
		controls := cursorRuntimeControls{autoApproveWebSearch: autoApproveWebSearch}
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pollCtx, pollCancel := context.WithTimeout(ctx, 2*time.Second)
				captured, err := captureCursorPane(pollCtx, sessionName)
				if err == nil {
					controls.handle(ctx, sessionName, captured)
				}
				pollCancel()
				if isCursorTmuxSessionLostError(err) {
					return
				}
			}
		}
	}()
}

// Stop before re-entering GenerateContent or unregistering/closing a session.
// Waiting for done prevents the old handler from racing a changed policy.
func stopCursorRetainedControls(sessionName string) {
	cursorRetainedControlRegistry.Lock()
	watcher := cursorRetainedControlRegistry.sessions[sessionName]
	delete(cursorRetainedControlRegistry.sessions, sessionName)
	cursorRetainedControlRegistry.Unlock()
	if watcher != nil {
		watcher.cancel()
		<-watcher.done
	}
}

// Configured bridge tools are already allowed. An unexpected request in
// unattended work is denied; attended approval permits only this invocation.
func cursorMCPApprovalKey(ctx context.Context, captured string) string {
	if !llmtypes.CodingAgentQuestionsAvailable(ctx) {
		return "n"
	}
	allowed, err := nativeinput.ApproveCommand(ctx, "Cursor requests permission to run this MCP tool:\n\n"+cursorVisiblePaneText(captured))
	if err == nil && allowed {
		return "y"
	}
	return "n"
}
