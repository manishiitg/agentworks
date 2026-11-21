package server

import (
	"fmt"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/agycli"
)

// The chat's view of an Antigravity conversation, for history and resume, read the way Claude's and Codex's journals
// are (native_history_window.go): a recent window, kept between reads, extended with only what was added. The old
// read copied the whole conversation database and parsed every step each time it was asked, so its cost grew with the
// chat. Now an unchanged conversation costs two stat calls, and a changed one parses only the steps after the last one
// held (the window is also bounded in rows and text).
type agyHistoryRow struct {
	idx     int64
	message builderConversationMessage
}

type agyHistoryCache struct {
	mu       sync.Mutex
	loaded   bool
	stamp    agycli.NativeTranscriptStamp
	lastIdx  int64 // newest step of any kind already read
	rows     []agyHistoryRow
	complete bool // every conversation row since the start of the conversation is held
}

var agyHistoryWindows = struct {
	sync.Mutex
	entries map[string]*agyHistoryCache
}{entries: map[string]*agyHistoryCache{}}

func agyHistoryWindow(path string) *agyHistoryCache {
	agyHistoryWindows.Lock()
	defer agyHistoryWindows.Unlock()
	if cached := agyHistoryWindows.entries[path]; cached != nil {
		return cached
	}
	// A small fixed cache; eviction only loses an I/O optimization.
	if len(agyHistoryWindows.entries) >= 16 {
		for old := range agyHistoryWindows.entries {
			delete(agyHistoryWindows.entries, old)
			break
		}
	}
	cached := &agyHistoryCache{}
	agyHistoryWindows.entries[path] = cached
	return cached
}

// readRecentAgyTranscript returns the window of an agy conversation. ok is false when it has no file or no messages.
func readRecentAgyTranscript(nativeSessionID string, accountHome ...string) (messages []builderConversationMessage, updatedAt time.Time, path string, ok bool, err error) {
	stamp, found := agycli.StatNativeTranscript(nativeSessionID, accountHome...)
	if !found {
		return nil, time.Time{}, "", false, nil
	}
	cache := agyHistoryWindow(stamp.Path)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if !cache.loaded || cache.stamp != stamp {
		after := int64(-1)
		if cache.loaded {
			after = cache.lastIdx
		}
		tail, readable, readErr := agycli.ReadNativeTranscriptSince(nativeSessionID, after, nativeHistoryMessages, accountHome...)
		if readErr != nil || !readable {
			return nil, time.Time{}, "", false, readErr
		}
		if cache.loaded && tail.MaxIdx < cache.lastIdx {
			// The conversation was replaced by a shorter one: start over from its newest rows.
			cache.loaded, cache.rows, cache.lastIdx, cache.complete = false, nil, 0, false
			if tail, readable, readErr = agycli.ReadNativeTranscriptSince(nativeSessionID, -1, nativeHistoryMessages, accountHome...); readErr != nil || !readable {
				return nil, time.Time{}, "", false, readErr
			}
		}
		first := !cache.loaded
		if first {
			cache.complete = !tail.More
		} else if tail.More {
			cache.complete = false
		}
		for _, row := range tail.Rows {
			converted := builderConversationMessagesFromLLMTypes([]llmtypes.MessageContent{row.Message})
			if len(converted) != 1 {
				continue
			}
			converted[0].NativeID = fmt.Sprintf("agy-cli:%d", row.Idx)
			cache.rows = append(cache.rows, agyHistoryRow{idx: row.Idx, message: converted[0]})
		}
		cache.loaded, cache.stamp, cache.lastIdx = true, stamp, tail.MaxIdx
		cache.trim()
	}
	if len(cache.rows) == 0 {
		return nil, time.Time{}, "", false, nil
	}
	out := make([]builderConversationMessage, 0, len(cache.rows))
	for _, row := range cache.rows {
		out = append(out, row.message)
	}
	return filterNativeContinuityMessagesUncapped(out), stamp.ModTime, stamp.Path, true, nil
}

// trim keeps the newest rows within the same row and text limits as the other providers' windows. A conversation row
// dropped here means a prompt can no longer be proven absent.
func (cache *agyHistoryCache) trim() {
	total := 0
	start := len(cache.rows)
	for start > 0 && len(cache.rows)-start < nativeHistoryMessages {
		size := len(builderConversationMessageText(cache.rows[start-1].message))
		if total+size > nativeHistoryTextBytes && start < len(cache.rows) {
			break
		}
		total += size
		start--
	}
	if start > 0 {
		cache.complete = false
		cache.rows = append([]agyHistoryRow(nil), cache.rows[start:]...)
	}
}

// agyHistoryComplete says whether the window holds every conversation row, so a missing prompt proves it was not
// delivered (the bounded view of a long conversation cannot).
func agyHistoryComplete(path string) bool {
	agyHistoryWindows.Lock()
	cache := agyHistoryWindows.entries[path]
	agyHistoryWindows.Unlock()
	if cache == nil {
		return false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.loaded && cache.complete
}
