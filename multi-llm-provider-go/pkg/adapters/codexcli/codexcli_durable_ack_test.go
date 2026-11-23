package codexcli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeDurableAckFixture(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout-test.jsonl")
	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func durableUserRow(ts time.Time, text string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":%q}]}}`,
		ts.Format(time.RFC3339Nano), text)
}

func durableAssistantRow(ts time.Time, text string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":%q}]}}`,
		ts.Format(time.RFC3339Nano), text)
}

func TestCodexInitialPaneErrorDefersToRollout(t *testing.T) {
	since := time.Now().UTC().Add(-time.Second)
	message := "initial durable Codex prompt"
	path := writeDurableAckFixture(t, durableUserRow(time.Now().UTC(), message))
	session := &codexInteractiveSession{rolloutPath: path}
	if err := codexConfirmInitialSubmitAfterPaneError(context.Background(), session, message, since, fmt.Errorf("pane mismatch")); err != nil {
		t.Fatalf("durable user row must override pane mismatch: %v", err)
	}
}

func TestCodexRolloutUserMessageSince(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	msg := "Do not use tools. Reply exactly: DURABLE_ACK_1A2B"

	t.Run("exact match after since", func(t *testing.T) {
		rowAt := base.Add(2 * time.Second)
		path := writeDurableAckFixture(t,
			`{"type":"session_meta","payload":{"id":"thread-1","cwd":"/tmp/work"}}`,
			durableUserRow(rowAt, msg),
			durableAssistantRow(rowAt.Add(time.Second), "DURABLE_ACK_1A2B"),
		)
		at, ok := codexRolloutUserMessageSince(path, msg, base, 0)
		if !ok {
			t.Fatal("expected the user row to confirm the send")
		}
		if !at.Equal(rowAt) {
			t.Fatalf("row timestamp = %v, want %v", at, rowAt)
		}
	})

	t.Run("identical earlier message before offset does not confirm", func(t *testing.T) {
		firstAt := base.Add(-time.Minute)
		first := durableUserRow(firstAt, msg) + "\n"
		path := writeDurableAckFixture(t, strings.TrimSuffix(first, "\n"))
		offset := int64(len(first))
		if _, ok := codexRolloutUserMessageSince(path, msg, base, offset); ok {
			t.Fatal("a row before the pre-paste offset must not confirm a new send")
		}
	})

	t.Run("row older than since does not confirm", func(t *testing.T) {
		path := writeDurableAckFixture(t, durableUserRow(base.Add(-time.Hour), msg))
		if _, ok := codexRolloutUserMessageSince(path, msg, base, 0); ok {
			t.Fatal("a stale row must not confirm a new send")
		}
	})

	t.Run("assistant and environment rows do not match", func(t *testing.T) {
		rowAt := base.Add(time.Second)
		path := writeDurableAckFixture(t,
			durableAssistantRow(rowAt, msg),
			durableUserRow(rowAt, "<environment_context><cwd>/tmp/work</cwd></environment_context>"),
		)
		if _, ok := codexRolloutUserMessageSince(path, msg, base, 0); ok {
			t.Fatal("assistant output and environment context are not user acceptance")
		}
	})

	t.Run("malformed lines are skipped", func(t *testing.T) {
		rowAt := base.Add(time.Second)
		path := writeDurableAckFixture(t,
			`{"type":"response_item","payload":`,
			`not json at all`,
			durableUserRow(rowAt, msg),
		)
		if _, ok := codexRolloutUserMessageSince(path, msg, base, 0); !ok {
			t.Fatal("expected the valid user row to confirm despite malformed lines")
		}
	})

	t.Run("missing file has no proof", func(t *testing.T) {
		if _, ok := codexRolloutUserMessageSince(filepath.Join(t.TempDir(), "absent.jsonl"), msg, base, 0); ok {
			t.Fatal("a missing rollout must not confirm")
		}
	})

	t.Run("long message matches on prefix", func(t *testing.T) {
		long := "LIVE_FOLLOWUP_" + strings.Repeat("0123456789", 30)
		rowAt := base.Add(time.Second)
		path := writeDurableAckFixture(t, durableUserRow(rowAt, long[:200]+"…trailing-normalized"))
		if _, ok := codexRolloutUserMessageSince(path, long, base, 0); !ok {
			t.Fatal("expected a shared 200-char prefix to confirm a long send")
		}
	})
}

func TestCodexDurableAckBudget(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("CODEX_DURABLE_ACK_SECONDS", "")
		if got := codexDurableAckBudget(); got != 180*time.Second {
			t.Fatalf("budget = %v, want 180s", got)
		}
	})
	t.Run("override", func(t *testing.T) {
		t.Setenv("CODEX_DURABLE_ACK_SECONDS", "90")
		if got := codexDurableAckBudget(); got != 90*time.Second {
			t.Fatalf("budget = %v, want 90s", got)
		}
	})
	t.Run("clamped", func(t *testing.T) {
		t.Setenv("CODEX_DURABLE_ACK_SECONDS", "1")
		if got := codexDurableAckBudget(); got != 5*time.Second {
			t.Fatalf("budget = %v, want 5s floor", got)
		}
		t.Setenv("CODEX_DURABLE_ACK_SECONDS", "9999")
		if got := codexDurableAckBudget(); got != 300*time.Second {
			t.Fatalf("budget = %v, want 300s ceiling", got)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		t.Setenv("CODEX_DURABLE_ACK_SECONDS", "soon")
		if got := codexDurableAckBudget(); got != 180*time.Second {
			t.Fatalf("budget = %v, want 180s default", got)
		}
	})
}

func TestCodexPaneShowsQueuedMessage(t *testing.T) {
	msg := "New instruction: end with PINEAPPLEBUS_9Q."
	queued := "• Messages to be submitted after next tool call (press esc to interrupt and send immediately)"
	// Exact live layout from the 0.155.0 probe: banner, queued text,
	// ready footer repainted below.
	live := strings.Join([]string{
		"› Do not use tools. Write a 500-word essay on concrete.",
		"• Planning 500-word essay draft (5s • esc to interrupt)",
		queued,
		"  ↳ " + msg,
		"› Ask Codex to do anything",
		"  gpt-5.6-luna medium · /tmp/work",
	}, "\n")
	if !codexPaneShowsQueuedMessage(live, msg) {
		t.Fatal("expected the live busy-steer layout to read as queued")
	}
	if codexPaneShowsQueuedMessage(live, "some other message") {
		t.Fatal("a different message must not match the queued text")
	}

	flushed := strings.Join([]string{
		queued,
		"  ↳ " + msg,
		"PINEAPPLEBUS_9Q",
		"✻ Worked for 42s",
		"› Ask Codex to do anything",
	}, "\n")
	if codexPaneShowsQueuedMessage(flushed, msg) {
		t.Fatal("a completion below the banner means the queue flushed; it must not read as queued")
	}

	bannerWithoutText := strings.Join([]string{
		queued,
		"› Ask Codex to do anything",
	}, "\n")
	if codexPaneShowsQueuedMessage(bannerWithoutText, msg) {
		t.Fatal("a banner without this message text must not confirm it as queued")
	}

	if codexPaneShowsQueuedMessage("› Ask Codex to do anything\n", msg) {
		t.Fatal("an idle pane must not read as queued")
	}
}

func TestPollCodexDurableAck(t *testing.T) {
	msg := "Do not use tools. Reply exactly: POLL_ACK_9Z8Y"
	base := time.Now().UTC()

	t.Run("row landing mid-poll confirms", func(t *testing.T) {
		path := writeDurableAckFixture(t, `{"type":"session_meta","payload":{"id":"thread-1"}}`)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat fixture: %v", err)
		}
		go func() {
			time.Sleep(100 * time.Millisecond)
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
			if err != nil {
				return
			}
			defer f.Close()
			_, _ = fmt.Fprintln(f, durableUserRow(time.Now().UTC(), msg))
		}()
		ack, err := pollCodexDurableAck(context.Background(), msg, base, info.Size(), 5*time.Second, codexDurableAckPoll{
			resolve:  func() string { return path },
			interval: 10 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("poll error = %v", err)
		}
		if ack.Outcome != CodexDurableAckConfirmed || ack.ProofPath != path {
			t.Fatalf("ack = %+v, want confirmed with proof", ack)
		}
	})

	t.Run("occurrence 2 waits past the first row", func(t *testing.T) {
		path := writeDurableAckFixture(t, durableUserRow(base.Add(time.Second), msg))
		_, err := pollCodexDurableAck(context.Background(), msg, base, 0, 150*time.Millisecond, codexDurableAckPoll{
			resolve:    func() string { return path },
			interval:   10 * time.Millisecond,
			occurrence: 2,
		})
		if err == nil || !strings.Contains(err.Error(), "not durably acknowledged") {
			t.Fatalf("err = %v, want the second wait to time out on one row", err)
		}
	})

	t.Run("still queued at expiry is unflushed, not failed", func(t *testing.T) {
		path := writeDurableAckFixture(t, `{"type":"session_meta","payload":{"id":"thread-1"}}`)
		pane := "• Messages to be submitted after next tool call (press esc to interrupt and send immediately)\n  ↳ " + msg + "\n› Ask Codex to do anything\n"
		ack, err := pollCodexDurableAck(context.Background(), msg, base, 0, 150*time.Millisecond, codexDurableAckPoll{
			resolve:     func() string { return path },
			capture:     func(context.Context, string) (string, error) { return pane, nil },
			sessionName: "probe-session",
			interval:    10 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("unflushed must not error, got %v", err)
		}
		if ack.Outcome != CodexDurableAckUnflushed {
			t.Fatalf("ack = %+v, want accepted_but_unflushed", ack)
		}
	})

	t.Run("expiry capture runs under a live context", func(t *testing.T) {
		path := writeDurableAckFixture(t, `{"type":"session_meta","payload":{"id":"thread-1"}}`)
		pane := "• Messages to be submitted after next tool call (press esc to interrupt and send immediately)\n  ↳ " + msg + "\n› Ask Codex to do anything\n"
		ack, err := pollCodexDurableAck(context.Background(), msg, base, 0, 150*time.Millisecond, codexDurableAckPoll{
			resolve: func() string { return path },
			capture: func(cctx context.Context, _ string) (string, error) {
				if err := cctx.Err(); err != nil {
					return "", err
				}
				return pane, nil
			},
			sessionName: "probe-session",
			interval:    10 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("unflushed must not error, got %v", err)
		}
		if ack.Outcome != CodexDurableAckUnflushed {
			t.Fatalf("ack = %+v, want accepted_but_unflushed", ack)
		}
	})

	t.Run("parent cancel fails fast even when queued", func(t *testing.T) {
		path := writeDurableAckFixture(t, `{"type":"session_meta","payload":{"id":"thread-1"}}`)
		pane := "• Messages to be submitted after next tool call (press esc to interrupt and send immediately)\n  ↳ " + msg + "\n› Ask Codex to do anything\n"
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := pollCodexDurableAck(ctx, msg, base, 0, 5*time.Second, codexDurableAckPoll{
			resolve: func() string { return path },
			capture: func(cctx context.Context, _ string) (string, error) {
				if err := cctx.Err(); err != nil {
					return "", err
				}
				return pane, nil
			},
			sessionName: "probe-session",
			interval:    10 * time.Millisecond,
		})
		if err == nil {
			t.Fatal("expected cancellation to abort the poll, not report unflushed")
		}
	})

	t.Run("no row and no queue fails", func(t *testing.T) {
		path := writeDurableAckFixture(t, `{"type":"session_meta","payload":{"id":"thread-1"}}`)
		_, err := pollCodexDurableAck(context.Background(), msg, base, 0, 120*time.Millisecond, codexDurableAckPoll{
			resolve:     func() string { return path },
			capture:     func(context.Context, string) (string, error) { return "› Ask Codex to do anything\n", nil },
			sessionName: "probe-session",
			interval:    10 * time.Millisecond,
		})
		if err == nil || !strings.Contains(err.Error(), "not durably acknowledged") {
			t.Fatalf("err = %v, want a durable-ack failure", err)
		}
	})

	t.Run("context cancel aborts", func(t *testing.T) {
		path := writeDurableAckFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := pollCodexDurableAck(ctx, msg, base, 0, 5*time.Second, codexDurableAckPoll{
			resolve:  func() string { return path },
			interval: 10 * time.Millisecond,
		})
		if err == nil {
			t.Fatal("expected cancellation to abort the poll")
		}
	})
}

func TestStashAndPeekDurableReceipt(t *testing.T) {
	session := &codexInteractiveSession{ownerSessionID: "owner-1"}
	since := time.Now()
	stashCodexDurableReceipt(session, "  hello world  ", "/tmp/rollout.jsonl", 1234, since)
	receipt, ok := peekCodexDurableReceipt(session, "hello world")
	if !ok {
		t.Fatal("expected the stashed receipt to match the trimmed message")
	}
	if receipt.offset != 1234 || receipt.path != "/tmp/rollout.jsonl" || !receipt.since.Equal(since) {
		t.Fatalf("receipt = %+v, want the stashed snapshot", receipt)
	}
	if _, ok := peekCodexDurableReceipt(session, "other"); ok {
		t.Fatal("a different message must not match the receipt")
	}

	stale := &codexInteractiveSession{ownerSessionID: "owner-2"}
	stashCodexDurableReceipt(stale, "old", "/tmp/r.jsonl", 10, time.Now().Add(-time.Hour))
	if _, ok := peekCodexDurableReceipt(stale, "old"); ok {
		t.Fatal("an expired receipt must not match")
	}

	capped := &codexInteractiveSession{ownerSessionID: "owner-3"}
	for i := 0; i < codexMaxPendingDurableAcks+3; i++ {
		stashCodexDurableReceipt(capped, fmt.Sprintf("msg-%d", i), "/tmp/r.jsonl", int64(i), time.Now())
	}
	if _, ok := peekCodexDurableReceipt(capped, "msg-0"); ok {
		t.Fatal("receipts past the cap must drop oldest-first")
	}
	if _, ok := peekCodexDurableReceipt(capped, fmt.Sprintf("msg-%d", codexMaxPendingDurableAcks+2)); !ok {
		t.Fatal("the newest receipt must survive the cap")
	}
}

func TestTakeDurableReceiptFIFORepeat(t *testing.T) {
	session := &codexInteractiveSession{ownerSessionID: "owner-fifo"}
	base := time.Now().UTC().Truncate(time.Second)
	msg := "yes"
	// First send lands a row; the identical second send snapshots after it.
	row := durableUserRow(base.Add(time.Second), msg) + "\n"
	path := writeDurableAckFixture(t, strings.TrimSuffix(row, "\n"))
	secondOffset := int64(len(row))
	stashCodexDurableReceipt(session, msg, path, 0, base)
	stashCodexDurableReceipt(session, msg, path, secondOffset, base.Add(2*time.Second))

	first, ok := takeCodexDurableReceipt(session, msg)
	if !ok || first.offset != 0 {
		t.Fatalf("first take = (%+v %v), want the first send's baseline", first, ok)
	}
	second, ok := takeCodexDurableReceipt(session, msg)
	if !ok || second.offset != secondOffset {
		t.Fatalf("second take = (%+v %v), want the second send's baseline", second, ok)
	}
	if _, ok := takeCodexDurableReceipt(session, msg); ok {
		t.Fatal("no receipt must remain after two takes")
	}
	// The first wait is satisfied by the first row...
	if _, ok := codexRolloutUserMessageSince(path, msg, first.since, first.offset); !ok {
		t.Fatal("expected the first row to confirm the first wait")
	}
	// ...but the second wait cannot be satisfied by the first row.
	if _, ok := codexRolloutUserMessageSince(path, msg, second.since, second.offset); ok {
		t.Fatal("the first row must not confirm the repeated send")
	}
}

func TestTakeCodexDurableReceiptOccurrenceRepeat(t *testing.T) {
	session := &codexInteractiveSession{ownerSessionID: "owner-occ"}
	base := time.Now().UTC().Truncate(time.Second)
	msg := "yes"
	// Both identical sends snapshot before either row lands: same
	// offset on both receipts, so offset scoping alone cannot
	// disambiguate them — only occurrence can.
	path := writeDurableAckFixture(t, `{"type":"session_meta","payload":{"id":"thread-1"}}`)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}
	stashCodexDurableReceipt(session, msg, path, info.Size(), base)
	stashCodexDurableReceipt(session, msg, path, info.Size(), base.Add(time.Second))

	first, ok := takeCodexDurableReceipt(session, msg)
	if !ok || first.occurrence != 1 {
		t.Fatalf("first take = (%+v %v), want occurrence 1", first, ok)
	}
	second, ok := takeCodexDurableReceipt(session, msg)
	if !ok || second.occurrence != 2 {
		t.Fatalf("second take = (%+v %v), want occurrence 2", second, ok)
	}

	appendRow := func(text string, at time.Time) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			t.Fatalf("append fixture row: %v", err)
		}
		defer f.Close()
		if _, err := fmt.Fprintln(f, durableUserRow(at, text)); err != nil {
			t.Fatalf("append fixture row: %v", err)
		}
	}
	appendRow(msg, base.Add(2*time.Second))

	// The first wait is satisfied by the first row...
	if _, ok := codexRolloutUserMessageOccurrenceSince(path, msg, first.since, first.offset, first.occurrence); !ok {
		t.Fatal("expected the first row to confirm the first wait")
	}
	// ...but the second wait needs a second row.
	if _, ok := codexRolloutUserMessageOccurrenceSince(path, msg, second.since, second.offset, second.occurrence); ok {
		t.Fatal("the first row must not confirm the repeated send")
	}
	// A second row satisfies the second wait.
	appendRow(msg, base.Add(3*time.Second))
	if _, ok := codexRolloutUserMessageOccurrenceSince(path, msg, second.since, second.offset, second.occurrence); !ok {
		t.Fatal("expected the second row to confirm the second wait")
	}
}

func TestTakeCodexDurableReceiptConcurrentOccurrence(t *testing.T) {
	session := &codexInteractiveSession{ownerSessionID: "owner-occ-race"}
	msg := "yes"
	stashCodexDurableReceipt(session, msg, "/tmp/rollout.jsonl", 0, time.Now())
	stashCodexDurableReceipt(session, msg, "/tmp/rollout.jsonl", 0, time.Now())

	// Watcher goroutines take in acquisition order, not spawn order:
	// however they schedule, the two takes must bind distinct
	// occurrences — never the same receipt twice, never a gap.
	var wg sync.WaitGroup
	got := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			receipt, ok := takeCodexDurableReceipt(session, msg)
			if !ok {
				t.Errorf("take %d: expected a receipt", idx)
				return
			}
			got[idx] = receipt.occurrence
		}(i)
	}
	wg.Wait()
	if (got[0] != 1 || got[1] != 2) && (got[0] != 2 || got[1] != 1) {
		t.Fatalf("take occurrences = %v, want {1 2} in either order", got)
	}
	if _, ok := takeCodexDurableReceipt(session, msg); ok {
		t.Fatal("no receipt must remain after two takes")
	}
}
