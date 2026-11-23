package claudecode

import (
	"context"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A retained CLI applies its environment once, at launch, so reusing the live
// process across a scope change silently kept the previous turn's credentials
// alive -- revoking one required killing the session by hand. This drives REAL
// persistent Claude sessions across turns and asserts on the tmux session
// actually backing them, because the identity of the live process is the only
// thing that proves whether the old environment survived.
func TestClaudeRetainedSessionReplacedWhenScopeChanges(t *testing.T) {
	skipClaudeInteractivePersistentE2E(t)

	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	ownerSessionID := "claude-scope-retention-" + randomHex(4)
	turn := func(t *testing.T, scope map[string]string) string {
		t.Helper()
		options := []llmtypes.CallOption{
			WithInteractiveSessionID(ownerSessionID),
			WithPersistentInteractiveSession(true),
			WithEffort("low"),
		}
		if scope != nil {
			options = append(options, llmtypes.WithCodingAgentSecretEnvironment(scope))
		}
		resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with exactly: ok"}},
		}}, options...)
		if err != nil {
			t.Fatalf("turn failed: %v", err)
		}
		if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].GenerationInfo == nil {
			t.Fatalf("turn returned no generation info: %#v", resp)
		}
		name, _ := resp.Choices[0].GenerationInfo.Additional["claude_code_session"].(string)
		if name == "" {
			t.Fatalf("turn returned no tmux session: %#v", resp.Choices[0].GenerationInfo.Additional)
		}
		return name
	}

	first := turn(t, map[string]string{"SECRET_TENANT": "one"})

	// Same scope: the process must be REUSED. Without this half, a fingerprint
	// that simply never matched would also pass the checks below.
	if same := turn(t, map[string]string{"SECRET_TENANT": "one"}); same != first {
		t.Fatalf("unchanged scope churned the session: %q then %q", first, same)
	}

	// Rotated value: a live process still holding "one" is the bug.
	rotated := turn(t, map[string]string{"SECRET_TENANT": "two"})
	if rotated == first {
		t.Fatalf("rotating a credential reused the process launched with the old one: %q", rotated)
	}

	// Removal is the case that matters most -- narrowing must not be silent.
	removed := turn(t, map[string]string{})
	if removed == rotated {
		t.Fatalf("removing every credential reused the process that still holds them: %q", removed)
	}
}
