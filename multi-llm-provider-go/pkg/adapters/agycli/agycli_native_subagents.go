package agycli

import (
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
)

// invoke_subagent's result is an Any at 140.2.6. AGY 1.5 stores an
// exa.cortex.core.tools.InvokeSubagent there whose repeated field 2 holds one
// created child each (field 1 = conversation id); AGY 1.2.14 stored an
// embedded gemini_coder.Step with the children at 143.10.1. Invocation
// completion only means the children were launched; the parent can be idle.
func agyNativeSubagentIDs(payload []byte) []string {
	result := payload
	for _, field := range []protowire.Number{140, 2, 6} {
		var ok bool
		result, ok = agyProtoSubmessage(result, field)
		if !ok {
			return nil
		}
	}
	value, ok := agyProtoSubmessage(result, 2)
	if !ok {
		return nil
	}
	children, field := value, protowire.Number(2)
	if typeURL, _ := agyProtoStringField(result, 1); !strings.HasSuffix(typeURL, ".InvokeSubagent") {
		if children, ok = agyProtoSubmessage(value, 143); !ok {
			return nil
		}
		field = 10
	}
	var ids []string
	section := children
	for len(section) > 0 {
		num, typ, n := protowire.ConsumeTag(section)
		if n < 0 {
			return nil
		}
		section = section[n:]
		size := protowire.ConsumeFieldValue(num, typ, section)
		if size < 0 {
			return nil
		}
		if num == field && typ == protowire.BytesType {
			child, _ := protowire.ConsumeBytes(section)
			if id, ok := agyProtoStringField(child, 1); ok && id != "" && !strings.ContainsAny(id, `/\`) {
				ids = append(ids, id)
			}
		}
		section = section[size:]
	}
	return ids
}

// Wait for both a finished child trail and its message to reach the parent.
// That message must precede the parent's settled final assistant row. Missing
// or interrupted child records never turn an interim answer into completion.
func agyPendingNativeSubagents(record agyTurnRecord, depth int) bool {
	if depth >= 16 && len(record.subagents) > 0 {
		return true
	}
	for id, notified := range record.subagents {
		if !notified {
			return true
		}
		child, err := agyReadTurnRecordFromPath(id, -1, "", filepath.Join(record.transcriptDir, id+".db"))
		if err != nil || child.lastType != agyStepAssistant || child.lastStatus != 3 || child.finalAnswer == "" || agyPendingNativeSubagents(child, depth+1) {
			return true
		}
	}
	return false
}
