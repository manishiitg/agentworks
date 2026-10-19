package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultSyntheticTurnIdleTimeout bounds how long a synthetic turn may
	// produce nothing at all before the turn is treated as stuck.
	//
	// It matches the tmux orphan ceiling deliberately: both answer the same
	// question — how long may a coding-agent turn show no sign of life before
	// the process holding it is presumed lost — and two different answers to
	// one question is how a session ends up reaped at one layer while another
	// still believes it is running.
	defaultSyntheticTurnIdleTimeout = time.Hour
	envSyntheticTurnIdleSeconds     = "MCP_SYNTHETIC_TURN_IDLE_TIMEOUT_SECONDS"
)

// syntheticTurnIdleTimeout returns the configured idle bound.
//
// Configuration may shorten the bound but never extend it, for the same reason
// codingAgentTmuxOrphanIdleTimeout clamps: this is a backstop against a turn
// that can never finish, and a backstop that can be configured away stops
// being one.
func syntheticTurnIdleTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv(envSyntheticTurnIdleSeconds))
	if raw == "" {
		return defaultSyntheticTurnIdleTimeout
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return defaultSyntheticTurnIdleTimeout
	}
	if timeout := time.Duration(seconds) * time.Second; timeout < defaultSyntheticTurnIdleTimeout {
		return timeout
	}
	return defaultSyntheticTurnIdleTimeout
}

// drainSyntheticTurnStream consumes a synthetic turn's text channel, applying
// onChunk for each chunk. It reports why the turn stopped producing — an empty
// reason when the stream closed normally — and whether it stopped because the
// producer went silent rather than because the turn was canceled.
//
// The two are distinguished because they mean opposite things to a reader of
// the run history. A canceled turn is a turn somebody stopped; a stalled one
// is a defect or a capacity wall. Collapsing both into "context canceled" is
// how a stuck turn gets filed as a user action and never investigated.
//
// PLAT-101. This replaces a bare `for range textChan`, which had exactly one
// exit: the producer closing the channel. When a coding-agent CLI parks on a
// usage-limit wall it stops producing without ever closing, and the loop's
// context was derived from context.Background(), so nothing could interrupt
// it. The consuming goroutine blocked forever, and with it the deferred
// cleanup that clears session-busy, releases the input lane, and records the
// tracked execution — leaving a session that reports "running" indefinitely
// with nothing executing behind it. Canceling the session did not help
// either: cancellation reaches the producer, but the loop was waiting on the
// channel, not on the context.
//
// Both bounds are needed. The context covers a deliberate stop; the idle timer
// covers a producer that has silently stopped without erroring, which is the
// case a stalled tmux pane actually presents. Every rate-limit behavior built
// on top of this — typed quota errors, suspend-and-resume — is unreachable
// while a turn can hang here instead of returning.
func drainSyntheticTurnStream(ctx context.Context, textChan <-chan string, onChunk func(string)) (reason string, stalled bool) {
	timeout := syntheticTurnIdleTimeout()
	idle := time.NewTimer(timeout)
	defer idle.Stop()

	for {
		select {
		case _, open := <-textChan:
			if !open {
				return "", false
			}
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(timeout)
			if onChunk != nil {
				onChunk("")
			}
		case <-ctx.Done():
			return ctx.Err().Error(), false
		case <-idle.C:
			return fmt.Sprintf("no stream activity for %s", timeout), true
		}
	}
}

// discardAbandonedSyntheticStream drains a channel we have stopped consuming.
//
// Abandoning a stream mid-flight leaves the producer potentially blocked on a
// send that nobody will receive, which converts one stuck turn into a leaked
// goroutine holding whatever the producer holds. Cancellation alone does not
// settle this: a producer already blocked in a send is not selecting on its
// context. Reading to close is the only thing that releases it, so this runs
// detached and does nothing but discard.
func discardAbandonedSyntheticStream(sessionID string, textChan <-chan string) {
	go func() {
		discarded := 0
		for range textChan {
			discarded++
		}
		if discarded > 0 {
			log.Printf("[BG AGENT] Discarded %d chunk(s) from abandoned synthetic stream on session %s", discarded, sessionID)
		}
	}()
}

// The common activity watch sees tools as well as text. This drain only owns
// channel consumption and prompt cancellation; it must not add a second clock.
func drainSyntheticTurnStreamWithoutIdle(ctx context.Context, chunks <-chan string, onChunk func(string)) (string, bool) {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err().Error(), false
		case chunk, open := <-chunks:
			if !open {
				return "", false
			}
			if onChunk != nil {
				onChunk(chunk)
			}
		}
	}
}
