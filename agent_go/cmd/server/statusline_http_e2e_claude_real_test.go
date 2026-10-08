package server

import (
	"context"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	claudecodeadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

// TestTerminalStatusLineHTTPCapturesRealClaudeCodeTelemetry drives the shared
// statusline HTTP e2e harness against a real claude-code run. It additionally
// proves claude-code surfaces the real model (model.display_name) and cost.
// Gated on RUN_CLAUDE_CODE_EXPERIMENTAL_LIVE_E2E=1 + the claude binary in PATH.
func TestTerminalStatusLineHTTPCapturesRealClaudeCodeTelemetry(t *testing.T) {
	runStatusLineHTTPE2E(t, statusLineE2ECase{
		name:             "claude",
		envGate:          "RUN_CLAUDE_CODE_EXPERIMENTAL_LIVE_E2E",
		binary:           "claude",
		wantStatusExtras: true,
		cleanup:          func() { _ = claudecodeadapter.CleanupClaudeCodeTmuxSessions(context.Background()) },
		run: func(ctx context.Context, sessionID string) (*llmtypes.StatusLine, error) {
			model := statusLineModelOr("CLAUDE_CODE_EXPERIMENTAL_MODEL", "claude-haiku-5-5")
			a := claudecodeadapter.NewClaudeCodeInteractiveAdapter(model, &e2eMockLogger{})
			// A persistent interactive session mirrors production: claude writes its
			// statusline temp file under the live tmux session, and the session stays
			// in the registry so GetStatusLine can resolve ownerSessionID → tmux name
			// after the turn. A non-persistent turn defer-unregisters that mapping on
			// return, so the pull would miss the file (it's keyed by the real tmux id).
			opts := []llmtypes.CallOption{
				claudecodeadapter.WithInteractiveSessionID(sessionID),
				claudecodeadapter.WithPersistentInteractiveSession(true),
			}
			if _, err := a.GenerateContent(ctx, statusLineOKPrompt(), opts...); err != nil {
				return nil, err
			}
			return a.GetStatusLine(ctx, sessionID)
		},
	})
}
