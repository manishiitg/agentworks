package musecli

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A canceled launch can leave tmux alive before the process-local pool records
// it. The next user message must reclaim that exact owner name and answer,
// rather than fail every retry with "duplicate session".
func TestMuseOrphanedPersistentTmuxAcceptsNewMessages(t *testing.T) {
	requireMuseBinary(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	museEchoTestEnv(t)
	owner := "orphan-retry-" + museRandomSessionSuffix()
	tmuxName := musePersistentTmuxName(owner)
	if out, err := exec.CommandContext(t.Context(), "tmux", "new-session", "-d", "-s", tmuxName, "sleep 60").CombinedOutput(); err != nil {
		t.Fatalf("seed orphaned tmux session: %v: %s", err, out)
	}
	t.Cleanup(func() {
		KillMusePersistentSession(owner)
		_ = museClosePersistentTmux(tmuxName)
	})
	adapter := NewMuseCLIAdapter("", "", museTestLogger{})
	opts := []llmtypes.CallOption{WithPersistentInteractiveSession(true), WithInteractiveSessionID(owner), WithWorkingDir(t.TempDir())}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, token := range []string{"FIRST_AFTER_ORPHAN", "SECOND_AFTER_ORPHAN"} {
		resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
			llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Reply with exactly "+token),
		}, opts...)
		if err != nil {
			t.Fatalf("new message %q after orphaned tmux: %v", token, err)
		}
		if len(resp.Choices) == 0 || !strings.Contains(resp.Choices[0].Content, token) {
			t.Fatalf("new message %q was not answered: %#v", token, resp.Choices)
		}
		if got := resp.Choices[0].GenerationInfo.CodingProviderSessionHandle.TmuxSession; got != tmuxName {
			t.Fatalf("owner %q used tmux %q, want %q", owner, got, tmuxName)
		}
	}
}
