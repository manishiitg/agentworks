package server

import (
	"context"
	"log"
)

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
func drainTurnTextStream(ctx context.Context, chunks <-chan string, onChunk func(string)) (string, bool) {
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
