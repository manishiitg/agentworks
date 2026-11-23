package musecli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// museActiveComposerDraft reads only the last prompt glyph's input line.
// Older prompts and replies in scrollback cannot authorize clearing a draft.
func museActiveComposerDraft(pane string) (string, bool) {
	idx := strings.LastIndex(pane, "❯")
	if idx < 0 {
		return "", false
	}
	line := pane[idx+len("❯"):]
	if end := strings.IndexByte(line, '\n'); end >= 0 {
		line = line[:end]
	}
	return strings.TrimSpace(line), true
}

// museRememberUnconfirmedDraft reuses the retained-draft cleanup on the next
// explicit turn. It never clears or resubmits an uncertain prompt here.
func museRememberUnconfirmedDraft(owner, session string) {
	key, err := musePersistentKey(owner)
	if err != nil {
		return
	}
	musePersistentPool.Lock()
	defer musePersistentPool.Unlock()
	if entry := musePersistentPool.m[key]; entry != nil && entry.tmuxName == session {
		entry.stoppedPrompt = entry.lastSubmittedPrompt
	}
}

// musePrepareStoppedPrompt clears only our own visibly retained draft after
// Stop or unconfirmed intake. If the user edited it, refuse to append a new message.
func musePrepareStoppedPrompt(ctx context.Context, owner, session, pane string) error {
	key, err := musePersistentKey(owner)
	if err != nil {
		return err
	}
	musePersistentPool.Lock()
	entry := musePersistentPool.m[key]
	expected := ""
	if entry != nil && entry.tmuxName == session {
		expected = entry.stoppedPrompt
	}
	musePersistentPool.Unlock()
	if expected == "" {
		return nil
	}
	draft, ok := museActiveComposerDraft(pane)
	if !ok {
		return fmt.Errorf("Muse composer unavailable; new prompt was not typed")
	}
	if draft != "" {
		firstLine, _, _ := strings.Cut(strings.TrimSpace(expected), "\n")
		prefix := []rune(firstLine)
		if len(prefix) > 32 {
			prefix = prefix[:32]
		}
		if !strings.HasPrefix(draft, string(prefix)) {
			return fmt.Errorf("Muse composer changed; new prompt was not typed")
		}
		// At the idle prompt with our nonempty draft, Muse's Clear shortcut
		// clears the whole editor. Ctrl-U clears only the current line and
		// does nothing on the trailing blank lines left by a refused submit.
		clear := exec.CommandContext(ctx, "tmux", "send-keys", "-t", session, "C-c")
		if out, err := clear.CombinedOutput(); err != nil {
			return fmt.Errorf("clear retained Muse draft: %w\n%s", err, out)
		}
		deadline := time.Now().Add(3 * time.Second)
		for {
			pane, err = museTmuxCapturePane(ctx, session)
			if err != nil {
				return fmt.Errorf("verify retained Muse draft cleared: %w", err)
			}
			if current, ok := museActiveComposerDraft(pane); ok && current == "" {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("retained Muse draft remained in composer after clear; new prompt was not typed")
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	musePersistentPool.Lock()
	if current := musePersistentPool.m[key]; current != nil && current.tmuxName == session && current.stoppedPrompt == expected {
		current.stoppedPrompt = ""
	}
	musePersistentPool.Unlock()
	return nil
}
