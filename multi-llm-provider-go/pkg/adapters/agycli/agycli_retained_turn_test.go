package agycli

import (
	"testing"
	"time"
)

func TestAgyRetainedStatePollingOrder(t *testing.T) {
	var state agyRetainedState
	first := time.Now()
	state.beginTurn(first) // Completion reader observes the new turn first.
	state.progressText = "progress"
	state.seenTools["tool-1"] = true
	state.beginTurn(first) // Progress reader must preserve already seen receipts.
	if !state.seenTools["tool-1"] || state.progressText != "progress" {
		t.Fatal("polling the same turn reset progress or receipt deduplication")
	}
	state.beginTurn(first.Add(time.Second))
	if len(state.seenTools) != 0 || state.progressText != "" {
		t.Fatal("a new turn retained the previous turn's progress or receipts")
	}
	state.seenTools["tool-2"] = true // New turn must also initialize the map.
}
