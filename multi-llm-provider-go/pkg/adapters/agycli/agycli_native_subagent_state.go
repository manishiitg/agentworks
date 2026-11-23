package agycli

import (
	"path/filepath"
	"strings"
)

// NativeSubagentIDs names only child conversations in the native invocation result.
func NativeSubagentIDs(payload []byte) []string { return agyNativeSubagentIDs(payload) }

// NativeSubagentReceiptID is the sender of a native child notification, not text inference.
func NativeSubagentReceiptID(payload []byte) string {
	notice, ok := agyProtoSubmessage(payload, 114)
	if !ok {
		return ""
	}
	message, ok := agyProtoSubmessage(notice, 4)
	if !ok {
		return ""
	}
	id, ok := agyProtoStringField(message, 3)
	if !ok || strings.ContainsAny(id, `/\`) {
		return ""
	}
	return id
}

// NativeSubagentSettled uses the same child/descendant and receipt rule as the
// native answer waiter. Missing, unreadable or interim trails never imply completion.
// The caller separately requires this child's receipt in its launching parent.
func NativeSubagentSettled(path string) bool {
	id := strings.TrimSuffix(filepath.Base(path), ".db")
	record, err := agyReadTurnRecordFromPath(id, -1, "", path)
	return err == nil && record.lastType == agyStepAssistant && record.lastStatus == 3 && record.finalAnswer != "" && !agyPendingNativeSubagents(record, 0)
}
