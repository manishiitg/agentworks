package agycli

import (
	"sort"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func readAgyNativeSubagents(parentID, home string) []llmtypes.NativeSubagentTranscript {
	seen := map[string]bool{parentID: true}
	var out []llmtypes.NativeSubagentTranscript
	var read func(string)
	read = func(parent string) {
		record, err := agyReadTurnRecord(parent, -1, "", home)
		if err != nil {
			return
		}
		ids := make([]string, 0, len(record.subagents))
		for id := range record.subagents {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			transcript, ok, err := readAgyNativeTranscript(id, true, home)
			if err != nil || !ok {
				continue
			}
			child, err := agyReadTurnRecord(id, -1, "", home)
			if err != nil {
				continue
			}
			status := "running"
			if child.lastType == agyStepAssistant && child.lastStatus == 3 {
				status = "completed"
			}
			if child.lastType == 17 {
				status = "failed"
			}
			out = append(out, llmtypes.NativeSubagentTranscript{ID: id, ParentID: parent, Name: "Native agent " + id, Provider: "agy-cli", Status: status, UpdatedAt: transcript.UpdatedAt, Messages: transcript.Messages})
			read(id)
		}
	}
	read(parentID)
	return out
}
