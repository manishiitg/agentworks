package codexcli

import (
	"context"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxinput"
)

// Codex allows one writer per thread. After the agent server restarts, the TUI
// that served a conversation can still be alive in tmux while the new process,
// having lost its in-memory registry, launches `codex resume` for the same
// thread: the resume dies with "thread … already has an active writer" and the
// conversation is stuck until someone finds the old pane by hand. The runtime
// tags every tmux session with its owner (tmuxinput.OwnerSessionOption), so
// the previous pane for this owner is identifiable and is reaped before a new
// one starts.

func listCodexTmuxSessionsForOwner(ctx context.Context, ownerSessionID string) []string {
	owner := strings.TrimSpace(ownerSessionID)
	if owner == "" {
		return nil
	}
	out, err := runCodexCommandOutput(ctx, nil, "tmux", "list-sessions", "-F", "#{session_name}\t#{"+tmuxinput.OwnerSessionOption+"}")
	if err != nil {
		return nil
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		name, tag, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || strings.TrimSpace(tag) != owner {
			continue
		}
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// reapOrphanCodexTmuxSessions kills every tmux session tagged with this owner
// except keep (the session about to be launched) and returns the names it
// removed.
func reapOrphanCodexTmuxSessions(ctx context.Context, ownerSessionID, keep string, logf func(string, ...interface{})) []string {
	var reaped []string
	for _, name := range listCodexTmuxSessionsForOwner(ctx, ownerSessionID) {
		if name == strings.TrimSpace(keep) {
			continue
		}
		if err := killCodexTmuxSession(ctx, name); err != nil {
			if logf != nil {
				logf("codex interactive could not reap orphaned tmux session %s for owner %s: %v", name, ownerSessionID, err)
			}
			continue
		}
		reaped = append(reaped, name)
	}
	return reaped
}
