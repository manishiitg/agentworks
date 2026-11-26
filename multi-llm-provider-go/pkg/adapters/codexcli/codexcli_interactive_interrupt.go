package codexcli

import (
	"context"
	"fmt"

	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxinput"
)

// SendCodexInteractiveControlKey injects a tmux control key (e.g. "Escape",
// "C-c") into a registered Codex CLI interactive session without sending Enter.
// Returns an error if no session is registered for the owner.
func SendCodexInteractiveControlKey(ctx context.Context, ownerSessionID, key string) error {
	sessionName, ok := activeCodexInteractiveSession(ownerSessionID)
	if !ok {
		if key != "Escape" && key != "C-c" {
			return fmt.Errorf("Codex app-server does not accept terminal key %q", key)
		}
		return InterruptCodexAppServer(ctx, ownerSessionID)
	}
	_, err := tmuxinput.Default.Do(ctx, tmuxinput.Request{
		SessionID: sessionName,
		Source:    "codex-cli-control",
		Priority:  tmuxinput.PriorityForKey(key),
	}, func(ctx context.Context) error {
		return runCodexCommand(ctx, nil, "tmux", "send-keys", "-t", sessionName, key)
	})
	return err
}
