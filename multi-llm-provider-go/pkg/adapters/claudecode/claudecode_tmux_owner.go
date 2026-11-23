package claudecode

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxinput"
)

// Reuse the owner tag already persisted for live reattachment. Unique launch
// names ensure delayed cleanup can never target a replacement process.
func closePreviousClaudeTmuxCopies(ctx context.Context, owner string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return
	}
	prefix := claudeInteractiveSessionPrefix() + "-"
	out, err := runCommandOutput(ctx, nil, "tmux", "list-sessions", "-F", "#{session_name}\t#{"+tmuxinput.OwnerSessionOption+"}")
	if err != nil {
		if !isTmuxNoServerError(err) {
			log.Printf("[CLI_RUNTIME] old_copy_lookup_failed chat=%s err=%v", owner, err)
		}
		return
	}
	for _, line := range strings.Split(out, "\n") {
		name, taggedOwner, ok := strings.Cut(line, "\t")
		if !ok || taggedOwner != owner || !strings.HasPrefix(name, prefix) {
			continue
		}
		if err := killClaudeInteractiveSession(ctx, name); err != nil {
			log.Printf("[CLI_RUNTIME] old_copy_close_failed chat=%s tmux=%s err=%v", owner, name, err)
			continue
		}
		log.Printf("[CLI_RUNTIME] old_copy_closed chat=%s tmux=%s", owner, name)
	}
}
