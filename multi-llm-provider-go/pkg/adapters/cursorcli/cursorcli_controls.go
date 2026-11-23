package cursorcli

import (
	"context"
	"errors"
	"fmt"
)

func cleanupCursorSDKSessions(ctx context.Context) error {
	cursorSDKSessions.Lock()
	owners := make([]string, 0, len(cursorSDKSessions.sessions))
	for owner := range cursorSDKSessions.sessions {
		owners = append(owners, owner)
	}
	cursorSDKSessions.Unlock()
	for _, owner := range owners {
		if err := ctx.Err(); err != nil {
			return err
		}
		closeCursorSDKInstance(owner, nil)
	}
	return nil
}

// interruptCursorSDK is the Stop button for an SDK chat: it cancels the running run.
func interruptCursorSDK(owner, key string) error {
	if key != "Escape" && key != "C-c" {
		return fmt.Errorf("Cursor supports cancellation only")
	}
	cursorSDKSessions.Lock()
	sdk := cursorSDKSessions.sessions[owner]
	cursorSDKSessions.Unlock()
	if sdk == nil {
		return fmt.Errorf("no active Cursor session")
	}
	sdk.mu.Lock()
	p, running := sdk.proc, sdk.running
	sdk.mu.Unlock()
	if p == nil || !running {
		return fmt.Errorf("no running Cursor turn")
	}
	return p.send(map[string]any{"type": "cancel"})
}

// Controls target the actual registered process, never retry through another transport.
func CloseCursorCLIInteractiveSessionForOwner(owner, reason string) {
	closeCursorSDKInstance(owner, nil)
	closeCursorTmuxForOwner(owner, reason)
}
func CleanupCursorCLIInteractiveSessions(ctx context.Context) error {
	return errors.Join(cleanupCursorSDKSessions(ctx), cleanupCursorTmuxSessions(ctx))
}
func InteractiveSessionRegistered(owner string) bool {
	if TmuxSessionRegistered(owner) {
		return true
	}
	cursorSDKSessions.Lock()
	defer cursorSDKSessions.Unlock()
	return cursorSDKSessions.sessions[owner] != nil
}
func SendCursorInteractiveControlKey(ctx context.Context, owner, key string) error {
	if TmuxSessionRegistered(owner) {
		return sendCursorTmuxControlKey(ctx, owner, key)
	}
	return interruptCursorSDK(owner, key)
}
