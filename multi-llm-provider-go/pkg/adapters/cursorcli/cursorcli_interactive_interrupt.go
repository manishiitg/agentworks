package cursorcli

import (
	"context"
	"fmt"

	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxinput"
)

// SendCursorInteractiveControlKey injects a tmux control key (e.g. "Escape",
// "C-c") into a registered Cursor CLI interactive session without sending Enter.
// Returns an error if no session is registered for the owner.
func sendCursorTmuxControlKey(ctx context.Context, ownerSessionID, key string) error {
	sessionName, ok := activeCursorInteractiveSession(ownerSessionID)
	if !ok {
		return fmt.Errorf("no active Cursor interactive session registered for owner session %s", ownerSessionID)
	}
	_, err := tmuxinput.Default.Do(ctx, tmuxinput.Request{
		SessionID: sessionName,
		Source:    "cursor-cli-control",
		Priority:  tmuxinput.PriorityForKey(key),
	}, func(ctx context.Context) error {
		return runCursorCommand(ctx, nil, "tmux", "send-keys", "-t", sessionName, key)
	})
	return err
}
