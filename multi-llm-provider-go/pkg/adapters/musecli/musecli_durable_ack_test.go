package musecli

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

func writeMuseDurableAckFixture(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func museIntakeRow(seq int64, recordedAt int64, text string) string {
	return fmt.Sprintf(`{"sequence":%d,"recorded_at":%d,"record_type":"event","payload_type":"runtime.user_intent.accepted","payload":{"surface":"main","semantic_kind":{"kind":"chat"},"refill_blocks":[{"kind":"text","text":%q}]}}`,
		seq, recordedAt, text)
}

func museQueuedRow(seq int64, recordedAt int64, text string) string {
	return fmt.Sprintf(`{"sequence":%d,"recorded_at":%d,"record_type":"event","payload_type":"runtime.session","payload":{"kind":"run","event":{"kind":"inbox_item_queued","summary":%q}}}`,
		seq, recordedAt, "steer: "+text)
}

func TestMuseUserAckRow(t *testing.T) {
	msg := "New instruction: end with MUSEFLUSH_8K."
	at := time.Now().UnixMicro()

	t.Run("intake row above baseline confirms", func(t *testing.T) {
		path := writeMuseDurableAckFixture(t,
			museIntakeRow(100, at-1000, "older prompt"),
			museIntakeRow(101, at, msg),
		)
		seq, rowTime, ok := museUserAckRow(path, msg, 100)
		if !ok || seq != 101 {
			t.Fatalf("ack = (%d %v), want seq 101 confirmed", seq, ok)
		}
		if rowTime.UnixMicro() != at {
			t.Fatalf("row time = %v, want recorded_at %d", rowTime, at)
		}
	})

	t.Run("queue event alone does not confirm accepted intent", func(t *testing.T) {
		path := writeMuseDurableAckFixture(t, museQueuedRow(103, at, msg))
		if _, _, ok := museUserAckRow(path, msg, 102); ok {
			t.Fatal("queue text alone must not confirm an accepted intent")
		}
	})

	t.Run("assistant echo does not confirm", func(t *testing.T) {
		path := writeMuseDurableAckFixture(t,
			fmt.Sprintf(`{"sequence":103,"recorded_at":%d,"payload_type":"runtime.session","payload":{"event":{"kind":"assistant_message_committed","text":%q}}}`, at, msg),
		)
		if _, _, ok := museUserAckRow(path, msg, 102); ok {
			t.Fatal("assistant echo must not confirm user delivery")
		}
	})

	t.Run("queue plus intake counts once for repeated sends", func(t *testing.T) {
		path := writeMuseDurableAckFixture(t,
			museIntakeRow(103, at, msg),
			museQueuedRow(104, at+1, msg),
		)
		if _, _, ok := museUserAckRowOccurrence(path, msg, 102, 2); ok {
			t.Fatal("one send's queue and intake rows must not confirm a second send")
		}
	})

	t.Run("identical row at or below baseline does not confirm", func(t *testing.T) {
		path := writeMuseDurableAckFixture(t, museIntakeRow(100, at, msg))
		if _, _, ok := museUserAckRow(path, msg, 100); ok {
			t.Fatal("a row at the pre-send baseline must not confirm a new send")
		}
	})

	t.Run("unrelated rows do not match", func(t *testing.T) {
		path := writeMuseDurableAckFixture(t,
			`{"sequence":101,"recorded_at":1,"record_type":"event","payload_type":"runtime.session","payload":{"kind":"run","event":{"kind":"assistant_message_committed","text":"done"}}}`,
			museIntakeRow(102, at, "some other prompt"),
		)
		if _, _, ok := museUserAckRow(path, msg, 100); ok {
			t.Fatal("rows without the snippet must not confirm")
		}
	})

	t.Run("malformed lines are skipped", func(t *testing.T) {
		path := writeMuseDurableAckFixture(t,
			`{"sequence":101,`,
			`not json at all`,
			museIntakeRow(102, at, msg),
		)
		if _, _, ok := museUserAckRow(path, msg, 101); !ok {
			t.Fatal("expected the valid intake row to confirm despite malformed lines")
		}
	})

	t.Run("missing file has no proof", func(t *testing.T) {
		if _, _, ok := museUserAckRow(filepath.Join(t.TempDir(), "absent.jsonl"), msg, 0); ok {
			t.Fatal("a missing transcript must not confirm")
		}
	})
}

func TestMuseDurableAckBudget(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("MUSE_DURABLE_ACK_SECONDS", "")
		if got := museDurableAckBudget(); got != 60*time.Second {
			t.Fatalf("budget = %v, want 60s", got)
		}
	})
	t.Run("override", func(t *testing.T) {
		t.Setenv("MUSE_DURABLE_ACK_SECONDS", "90")
		if got := museDurableAckBudget(); got != 90*time.Second {
			t.Fatalf("budget = %v, want 90s", got)
		}
	})
	t.Run("clamped", func(t *testing.T) {
		t.Setenv("MUSE_DURABLE_ACK_SECONDS", "1")
		if got := museDurableAckBudget(); got != 5*time.Second {
			t.Fatalf("budget = %v, want 5s floor", got)
		}
		t.Setenv("MUSE_DURABLE_ACK_SECONDS", "9999")
		if got := museDurableAckBudget(); got != 300*time.Second {
			t.Fatalf("budget = %v, want 300s ceiling", got)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		t.Setenv("MUSE_DURABLE_ACK_SECONDS", "soon")
		if got := museDurableAckBudget(); got != 60*time.Second {
			t.Fatalf("budget = %v, want 60s default", got)
		}
	})
}

func TestMusePaneShowsQueuedMessage(t *testing.T) {
	msg := "New instruction: end with MUSEFLUSH_8K."
	// Exact live layout from the 1.3.0 probe: the native queue banner
	// plus the queued text on the continuation line.
	live := strings.Join([]string{
		"◇ Thinking (17s · esc to interrupt)",
		"• Queued input",
		"  ↳ " + msg,
		"❯",
		"  muse-spark-1.3-contributor · max · /tmp/work · Auto-review",
	}, "\n")
	if !musePaneShowsQueuedMessage(live, msg) {
		t.Fatal("expected the live busy-steer layout to read as queued")
	}
	if musePaneShowsQueuedMessage(live, "some other message") {
		t.Fatal("a different message must not match the queued text")
	}

	idle := strings.Join([]string{
		"◆ MUSEPROBE_ACK_7F3A",
		"❯",
		"  muse-spark-1.3-contributor · max · /tmp/work · Auto-review",
	}, "\n")
	if musePaneShowsQueuedMessage(idle, msg) {
		t.Fatal("an idle pane must not read as queued")
	}
}

func TestMusePaneLooksActive(t *testing.T) {
	if !musePaneLooksActive("◇ Thinking (17s · esc to interrupt)\n❯\n") {
		t.Fatal("a live thinking indicator means a turn is in flight")
	}
	// Muse keeps the composer painted while streaming, so at-prompt
	// alone must not read as idle-or-busy either way here: without an
	// activity signal the pane is not provably active.
	if musePaneLooksActive("◆ MUSEPROBE_ACK_7F3A\n❯\n  muse-spark · max · /tmp/work\n") {
		t.Fatal("a quiet pane must not read as active")
	}
}

func TestPollMuseDurableAck(t *testing.T) {
	msg := "New instruction: reply exactly MUSE_POLL_ACK_9Z8Y."
	base := time.Now()
	at := base.UnixMicro()

	t.Run("row landing mid-poll confirms", func(t *testing.T) {
		path := writeMuseDurableAckFixture(t, museIntakeRow(100, at-1000, "older prompt"))
		go func() {
			time.Sleep(100 * time.Millisecond)
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
			if err != nil {
				return
			}
			defer f.Close()
			_, _ = fmt.Fprintln(f, museIntakeRow(101, time.Now().UnixMicro(), msg))
		}()
		ack, err := pollMuseDurableAck(context.Background(), msg, base, 100, 5*time.Second, museDurableAckPoll{
			resolve:  func() string { return path },
			interval: 10 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("poll error = %v", err)
		}
		if ack.Outcome != MuseDurableAckConfirmed || ack.ProofPath != path || ack.RowTimestamp.IsZero() {
			t.Fatalf("ack = %+v, want confirmed with proof and row time", ack)
		}
	})

	t.Run("occurrence 2 waits past the first row", func(t *testing.T) {
		path := writeMuseDurableAckFixture(t,
			museIntakeRow(100, at-1000, "older prompt"),
			museIntakeRow(101, at, msg),
		)
		_, err := pollMuseDurableAck(context.Background(), msg, base, 100, 150*time.Millisecond, museDurableAckPoll{
			resolve:    func() string { return path },
			interval:   10 * time.Millisecond,
			occurrence: 2,
		})
		if err == nil || !strings.Contains(err.Error(), "not durably acknowledged") {
			t.Fatalf("err = %v, want the second wait to time out on one row", err)
		}
	})

	t.Run("still queued at expiry is unflushed, not failed", func(t *testing.T) {
		path := writeMuseDurableAckFixture(t, museIntakeRow(100, at-1000, "older prompt"))
		pane := "• Queued input\n  ↳ " + msg + "\n❯\n"
		ack, err := pollMuseDurableAck(context.Background(), msg, base, 100, 150*time.Millisecond, museDurableAckPoll{
			resolve:     func() string { return path },
			capture:     func(context.Context, string) (string, error) { return pane, nil },
			sessionName: "probe-session",
			interval:    10 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("unflushed must not error, got %v", err)
		}
		if ack.Outcome != MuseDurableAckUnflushed {
			t.Fatalf("ack = %+v, want accepted_but_unflushed", ack)
		}
	})

	t.Run("taken by an active turn without rows is unflushed", func(t *testing.T) {
		path := writeMuseDurableAckFixture(t, museIntakeRow(100, at-1000, "older prompt"))
		pane := "◇ Thinking (3s · esc to interrupt)\n❯\n"
		ack, err := pollMuseDurableAck(context.Background(), msg, base, 100, 150*time.Millisecond, museDurableAckPoll{
			resolve:     func() string { return path },
			capture:     func(context.Context, string) (string, error) { return pane, nil },
			sessionName: "probe-session",
			interval:    10 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("unflushed must not error, got %v", err)
		}
		if ack.Outcome != MuseDurableAckUnflushed {
			t.Fatalf("ack = %+v, want accepted_but_unflushed", ack)
		}
	})

	t.Run("expiry capture runs under a live context", func(t *testing.T) {
		path := writeMuseDurableAckFixture(t, museIntakeRow(100, at-1000, "older prompt"))
		pane := "• Queued input\n  ↳ " + msg + "\n❯\n"
		ack, err := pollMuseDurableAck(context.Background(), msg, base, 100, 150*time.Millisecond, museDurableAckPoll{
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
		if ack.Outcome != MuseDurableAckUnflushed {
			t.Fatalf("ack = %+v, want accepted_but_unflushed", ack)
		}
	})

	t.Run("parent cancel fails fast even when queued", func(t *testing.T) {
		path := writeMuseDurableAckFixture(t, museIntakeRow(100, at-1000, "older prompt"))
		pane := "• Queued input\n  ↳ " + msg + "\n❯\n"
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := pollMuseDurableAck(ctx, msg, base, 100, 5*time.Second, museDurableAckPoll{
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

	t.Run("draft sitting on a quiet pane fails", func(t *testing.T) {
		path := writeMuseDurableAckFixture(t, museIntakeRow(100, at-1000, "older prompt"))
		pane := "❯ " + msg + "\n  muse-spark · max · /tmp/work\n"
		_, err := pollMuseDurableAck(context.Background(), msg, base, 100, 120*time.Millisecond, museDurableAckPoll{
			resolve:     func() string { return path },
			capture:     func(context.Context, string) (string, error) { return pane, nil },
			sessionName: "probe-session",
			interval:    10 * time.Millisecond,
		})
		if err == nil || !strings.Contains(err.Error(), "not durably acknowledged") {
			t.Fatalf("err = %v, want a durable-ack failure", err)
		}
	})

	t.Run("context cancel aborts", func(t *testing.T) {
		path := writeMuseDurableAckFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := pollMuseDurableAck(ctx, msg, base, 0, 5*time.Second, museDurableAckPoll{
			resolve:  func() string { return path },
			interval: 10 * time.Millisecond,
		})
		if err == nil {
			t.Fatal("expected cancellation to abort the poll")
		}
	})
}

func TestStashAndPeekMuseDurableReceipt(t *testing.T) {
	owner := "muse-durable-owner-1"
	musePersistentPool.Lock()
	musePersistentPool.m[owner] = &musePersistentSession{tmuxName: "mlp-muse-test"}
	musePersistentPool.Unlock()
	t.Cleanup(func() {
		musePersistentPool.Lock()
		delete(musePersistentPool.m, owner)
		musePersistentPool.Unlock()
	})

	since := time.Now()
	stashMuseDurableReceipt(owner, "  hello world  ", "/tmp/session.jsonl", 4242, since)
	receipt, ok := peekMuseDurableReceipt(owner, "hello world")
	if !ok {
		t.Fatal("expected the stashed receipt to match the trimmed message")
	}
	if receipt.baselineSeq != 4242 || receipt.logPath != "/tmp/session.jsonl" || !receipt.since.Equal(since) {
		t.Fatalf("receipt = %+v, want the stashed snapshot", receipt)
	}
	if _, ok := peekMuseDurableReceipt(owner, "other"); ok {
		t.Fatal("a different message must not match the receipt")
	}
	if _, ok := peekMuseDurableReceipt("muse-durable-absent", "hello world"); ok {
		t.Fatal("an unknown owner must not match")
	}
}

func TestTakeMuseDurableReceiptFIFORepeat(t *testing.T) {
	owner := "muse-durable-owner-fifo"
	musePersistentPool.Lock()
	musePersistentPool.m[owner] = &musePersistentSession{tmuxName: "mlp-muse-test"}
	musePersistentPool.Unlock()
	t.Cleanup(func() {
		musePersistentPool.Lock()
		delete(musePersistentPool.m, owner)
		musePersistentPool.Unlock()
	})

	at := time.Now().UnixMicro()
	msg := "yes"
	// First send lands a row; the identical second send snapshots after it.
	path := writeMuseDurableAckFixture(t,
		museIntakeRow(100, at-1000, "older prompt"),
		museIntakeRow(101, at, msg),
	)
	stashMuseDurableReceipt(owner, msg, path, 100, time.Now())
	stashMuseDurableReceipt(owner, msg, path, 101, time.Now())

	first, ok := takeMuseDurableReceipt(owner, msg)
	if !ok || first.baselineSeq != 100 {
		t.Fatalf("first take = (%+v %v), want the first send's baseline", first, ok)
	}
	second, ok := takeMuseDurableReceipt(owner, msg)
	if !ok || second.baselineSeq != 101 {
		t.Fatalf("second take = (%+v %v), want the second send's baseline", second, ok)
	}
	if _, ok := takeMuseDurableReceipt(owner, msg); ok {
		t.Fatal("no receipt must remain after two takes")
	}
	// The first wait is satisfied by the first row...
	if _, _, ok := museUserAckRow(path, msg, first.baselineSeq); !ok {
		t.Fatal("expected the first row to confirm the first wait")
	}
	// ...but the second wait cannot be satisfied by the first row.
	if _, _, ok := museUserAckRow(path, msg, second.baselineSeq); ok {
		t.Fatal("the first row must not confirm the repeated send")
	}
}

func TestTakeMuseDurableReceiptOccurrenceRepeat(t *testing.T) {
	owner := "muse-durable-owner-occ"
	musePersistentPool.Lock()
	musePersistentPool.m[owner] = &musePersistentSession{tmuxName: "mlp-muse-test"}
	musePersistentPool.Unlock()
	t.Cleanup(func() {
		musePersistentPool.Lock()
		delete(musePersistentPool.m, owner)
		musePersistentPool.Unlock()
	})

	at := time.Now().UnixMicro()
	msg := "yes"
	// Both identical sends snapshot before either row lands: same
	// baseline on both receipts, so only occurrence disambiguates them.
	stashMuseDurableReceipt(owner, msg, "/tmp/session.jsonl", 100, time.Now())
	stashMuseDurableReceipt(owner, msg, "/tmp/session.jsonl", 100, time.Now())

	first, ok := takeMuseDurableReceipt(owner, msg)
	if !ok || first.occurrence != 1 {
		t.Fatalf("first take = (%+v %v), want occurrence 1", first, ok)
	}
	second, ok := takeMuseDurableReceipt(owner, msg)
	if !ok || second.occurrence != 2 {
		t.Fatalf("second take = (%+v %v), want occurrence 2", second, ok)
	}

	path := writeMuseDurableAckFixture(t,
		museIntakeRow(100, at-1000, "older prompt"),
		museIntakeRow(101, at, msg),
	)
	// The first wait is satisfied by the first row...
	if _, _, ok := museUserAckRowOccurrence(path, msg, first.baselineSeq, first.occurrence); !ok {
		t.Fatal("expected the first row to confirm the first wait")
	}
	// ...but the second wait needs a second row.
	if _, _, ok := museUserAckRowOccurrence(path, msg, second.baselineSeq, second.occurrence); ok {
		t.Fatal("the first row must not confirm the repeated send")
	}
}

func TestTakeMuseDurableReceiptConcurrentOccurrence(t *testing.T) {
	owner := "muse-durable-owner-occ-race"
	musePersistentPool.Lock()
	musePersistentPool.m[owner] = &musePersistentSession{tmuxName: "mlp-muse-test"}
	musePersistentPool.Unlock()
	t.Cleanup(func() {
		musePersistentPool.Lock()
		delete(musePersistentPool.m, owner)
		musePersistentPool.Unlock()
	})

	msg := "yes"
	stashMuseDurableReceipt(owner, msg, "/tmp/session.jsonl", 100, time.Now())
	stashMuseDurableReceipt(owner, msg, "/tmp/session.jsonl", 100, time.Now())

	// Watcher goroutines take in acquisition order, not spawn order:
	// however they schedule, the two takes must bind distinct
	// occurrences — never the same receipt twice, never a gap.
	var wg sync.WaitGroup
	got := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			receipt, ok := takeMuseDurableReceipt(owner, msg)
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
	if _, ok := takeMuseDurableReceipt(owner, msg); ok {
		t.Fatal("no receipt must remain after two takes")
	}
}

func TestInteractiveSessionRegistered(t *testing.T) {
	owner := "muse-registered-owner"
	if InteractiveSessionRegistered(owner) {
		t.Fatal("an unregistered owner must not be steer-ready")
	}
	musePersistentPool.Lock()
	musePersistentPool.m[owner] = &musePersistentSession{tmuxName: "mlp-muse-test"}
	musePersistentPool.Unlock()
	t.Cleanup(func() {
		musePersistentPool.Lock()
		delete(musePersistentPool.m, owner)
		musePersistentPool.Unlock()
	})
	if !InteractiveSessionRegistered(owner) {
		t.Fatal("a registered owner must be steer-ready")
	}
}
