package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// mergeChatConversationSnapshots merges two snapshots, not a provider's text
// transcript. Keep raw message identities: projecting tool rows to role/text
// turns every tool-only row into the same key and corrupts later alignments.
// The result contains both input sequences in order, so replaying either
// snapshot is idempotent. Repeated equal rows keep their occurrence count.
func mergeChatConversationSnapshots(canonical, incoming map[string]interface{}) ([]json.RawMessage, error) {
	old, ok := builderConversationRawHistory(canonical)
	if !ok {
		return nil, fmt.Errorf("invalid canonical conversation history")
	}
	next, ok := builderConversationRawHistory(incoming)
	if !ok {
		return nil, fmt.Errorf("invalid incoming conversation history")
	}
	return alignChatHistories(old, next)
}

// alignChatHistories is the one merge every conversation writer uses. It walks
// next in order, matching each row to the next equal row of old at or after
// the previous match; rows only old has stay where they are, and rows only
// next has are inserted at their place in that walk. So a writer that saves
// its cumulative history, or only a tail window, never duplicates rows old
// already holds.
//
// A system prompt is regenerated every turn (it carries turn-local data such
// as the time), so it never matches: the newest one replaces old's leading
// system prompts instead of being inserted next to them. (server A 2026-09-25: a
// builder chat had 18 system prompts and 37,882 rows, 1,407 of them unique,
// after a concatenating merge re-appended the whole history every turn.)
func alignChatHistories(old, next []json.RawMessage) ([]json.RawMessage, error) {
	var system json.RawMessage
	if len(next) > 0 && chatSnapshotIsSystem(next[0]) {
		system = next[0]
		next = next[1:]
		for len(old) > 0 && chatSnapshotIsSystem(old[0]) {
			old = old[1:]
		}
	}
	positions := make(map[string][]int, len(old))
	for i, raw := range old {
		key, err := chatSnapshotMessageKey(raw)
		if err != nil {
			return nil, err
		}
		positions[key] = append(positions[key], i)
	}
	merged := make([]json.RawMessage, 0, len(old)+len(next)+1)
	if system != nil {
		merged = append(merged, system)
	}
	pending := make([]json.RawMessage, 0)
	cursor := 0
	for _, raw := range next {
		key, err := chatSnapshotMessageKey(raw)
		if err != nil {
			return nil, err
		}
		candidates := positions[key]
		index := sort.SearchInts(candidates, cursor)
		if index == len(candidates) {
			pending = append(pending, raw)
			continue
		}
		match := candidates[index]
		// Canonical-only answers precede newly submitted rows in the same gap.
		merged = append(merged, old[cursor:match]...)
		merged = append(merged, pending...)
		pending = pending[:0]
		merged = append(merged, old[match])
		cursor = match + 1
	}
	merged = append(merged, old[cursor:]...)
	merged = append(merged, pending...)
	return merged, nil
}

func chatSnapshotIsSystem(raw json.RawMessage) bool {
	var row struct {
		Role string `json:"Role"`
	}
	return json.Unmarshal(raw, &row) == nil && row.Role == string(llmtypes.ChatMessageTypeSystem)
}

func chatSnapshotMessageKey(raw json.RawMessage) (string, error) {
	// Match structured message content, keeping full tool IDs/arguments/results.
	// Top-level trace enrichment is not a new message; retain the canonical row
	// including that enrichment when the runtime snapshot omits it.
	var value map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	if role, ok := value["Role"]; ok {
		if parts, exists := value["Parts"]; exists {
			value = map[string]interface{}{"Role": role, "Parts": parts}
		}
	}
	encoded, err := json.Marshal(value)
	return string(encoded), err
}
