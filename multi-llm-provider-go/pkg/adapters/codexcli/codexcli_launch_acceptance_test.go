package codexcli

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestCodexLaunchAcceptanceUsesRolloutBeforePaneRenders(t *testing.T) {
	startedAt := time.Now()
	path := writeCodexTurnRollout(t,
		rolloutRow(startedAt.Add(-time.Minute), `{"type":"task_started","turn_id":"previous"}`),
	)
	session := &codexInteractiveSession{rolloutPath: path}
	captures := 0
	capture := func(context.Context, string) (string, error) {
		captures++
		if captures == 2 {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.WriteString(rolloutRow(startedAt, `{"type":"task_started","turn_id":"current"}`) + "\n")
			closeErr := f.Close()
			if err != nil || closeErr != nil {
				t.Fatalf("append accepted turn: %v, close: %v", err, closeErr)
			}
		}
		return "", errors.New("terminal has not rendered yet")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := waitForCodexInitialPromptAcceptedWith(ctx, "cold", "call the slow tool", nil, false, codexSubmissionWait{
		oracle: codexTurnStartOracle(session, startedAt), capture: capture,
	})
	if err != nil {
		t.Fatalf("accepted rollout must release startup despite missing pane: %v", err)
	}
	if captures != 2 {
		t.Fatalf("captures = %d, want 2; previous turns must not confirm launch", captures)
	}
}

func TestCodexLaunchAcceptanceWaitsForAuthoritativeTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 650*time.Millisecond)
	defer cancel()
	_, err := waitForCodexInitialPromptAcceptedWith(ctx, "cold", "new turn", nil, false, codexSubmissionWait{
		oracle: func() (bool, bool) { return false, true },
		capture: func(context.Context, string) (string, error) {
			return codex0153WorkingPane, nil
		},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("historical pane activity must not confirm an unaccepted turn: %v", err)
	}
}

func TestCodexLaunchAcceptanceKeepsUnboundPaneFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := waitForCodexInitialPromptAcceptedWith(ctx, "cold", "new turn", nil, false, codexSubmissionWait{
		oracle: func() (bool, bool) { return false, false },
		capture: func(context.Context, string) (string, error) {
			return codex0153WorkingPane, nil
		},
	})
	if err != nil {
		t.Fatalf("pane fallback without a bound rollout: %v", err)
	}
}

func TestCodexLaunchAcceptanceIgnoresIdleLoadingPane(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 650*time.Millisecond)
	defer cancel()
	_, err := waitForCodexInitialPromptAcceptedWith(ctx, "cold", "new turn", nil, false, codexSubmissionWait{
		oracle: func() (bool, bool) { return false, false },
		capture: func(context.Context, string) (string, error) {
			return "╭─ OpenAI Codex ─╮\nmodel: loading\nTake your time. The cursor can wait.\n› Find and fix a bug in @filename\n? for shortcuts", nil
		},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("loading screen must not confirm an unaccepted turn: %v", err)
	}
}
