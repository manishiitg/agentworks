package musecli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/internal/stalldiag"
	"io"
	"os"
	"time"
)

// museRunTerminal reads only run-level terminal events for the accepted turn.
// Task "completed" and model "model_completed" events occur inside tool
// loops, so neither can establish that the interactive turn is finished.
func museRunTerminal(logPath, runID string, minSeq int64) (status, reason string, found bool, err error) {
	var offset int64
	return museRunTerminalSince(logPath, runID, minSeq, &offset)
}

// museRunTerminalSince advances only past complete JSONL rows. A partial
// append remains unread for the next poll, and large retained sessions are
// scanned once rather than rescanned from byte zero every second.
func museRunTerminalSince(logPath, runID string, minSeq int64, offset *int64) (status, reason string, found bool, err error) {
	if runID == "" {
		return "", "", false, fmt.Errorf("muse run ID is empty")
	}
	f, err := os.Open(logPath)
	if err != nil {
		return "", "", false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", "", false, err
	}
	if *offset > info.Size() {
		*offset = 0 // log replaced or truncated
	}
	if _, err := f.Seek(*offset, io.SeekStart); err != nil {
		return "", "", false, err
	}
	reader := bufio.NewReader(f)
	for {
		line, readErr := reader.ReadBytes('\n')
		if readErr == io.EOF {
			break // retry any incomplete trailing row on the next poll
		}
		if readErr != nil {
			return "", "", false, readErr
		}
		*offset += int64(len(line))
		if !bytes.Contains(line, []byte(`"terminal"`)) {
			continue
		}
		var row struct {
			Sequence    int64  `json:"sequence"`
			PayloadType string `json:"payload_type"`
			Payload     struct {
				Kind  string `json:"kind"`
				RunID string `json:"run_id"`
				Event struct {
					Kind     string `json:"kind"`
					Terminal string `json:"terminal"`
					Reason   string `json:"reason"`
				} `json:"event"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &row) != nil || row.Sequence <= minSeq ||
			row.PayloadType != "runtime.session" || row.Payload.Kind != "run" ||
			row.Payload.RunID != runID || row.Payload.Event.Kind != "terminal" {
			continue
		}
		status, reason, found = row.Payload.Event.Terminal, row.Payload.Event.Reason, true
	}
	return status, reason, found, nil
}

// museWaitTurnTerminal uses the native run result as the completion gate.
// tmux remains the input transport and a source of P0 blocker checks while
// waiting. A final pane capture is best-effort P1 presentation data.
func museWaitTurnTerminal(ctx context.Context, session, logPath, runID string, minSeq int64, timeout time.Duration) (string, error) {
	if runID == "" {
		return "", fmt.Errorf("muse accepted intent has no run ID")
	}
	deadline := time.Now().Add(timeout)
	var pane string
	var offset int64
	quiet := &stalldiag.Watch{Provider: "muse-cli", Session: session}
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		status, reason, found, err := museRunTerminalSince(logPath, runID, minSeq, &offset)
		if err != nil {
			return "", fmt.Errorf("read muse run terminal event: %w", err)
		}
		if found {
			if status != "completed" {
				return "", fmt.Errorf("muse run %s ended %q: %s", runID, status, reason)
			}
			// Completion no longer depends on pane prompt text or log quiet.
			if finalPane, err := museTmuxCapturePane(ctx, session); err == nil {
				pane = finalPane
			}
			return pane, nil
		}
		if !museTmuxSessionAlive(ctx, session) {
			return "", fmt.Errorf("muse tmux session %q died before run completion", session)
		}
		pane, err = museTmuxCapturePane(ctx, session)
		if err != nil {
			return "", fmt.Errorf("capture pane waiting for muse run: %w", err)
		}
		quiet.Observe(time.Now(), pane, true)
		pending, err := museHandlePendingQuestion(ctx, session, pane)
		if err != nil {
			return "", err
		}
		if musePaneShowsBlockingGate(pane) {
			return "", fmt.Errorf("muse TUI hit a trust/auth gate mid-turn; pane:\n%s", pane)
		}
		if timeout > 0 && time.Now().After(deadline) {
			if pending {
				return "", musePendingUserInputError(pane)
			}
			return "", fmt.Errorf("timed out waiting for muse run %s terminal event; latest pane:\n%s", runID, pane)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
