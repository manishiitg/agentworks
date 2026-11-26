package server

import (
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (api *StreamingAPI) wireCursorSubagentRows() {
	cursorcli.SetSDKHelperListener(api.observeCursorSubagentRow)
}

func (api *StreamingAPI) observeCursorSubagentRow(owner string, record llmtypes.NativeRecord) {
	if api == nil || api.eventStore == nil || owner == "" || !api.eventStore.IsDurableChatSession(owner) {
		return
	}
	if record.Provider != "cursor-cli" || record.Kind != "helper" || record.TaskID == "" || record.SessionID == "" {
		return
	}
	record.Text, record.Output = "", ""
	activity := agentevents.NewAgentEvent(agentevents.NewCodingAgentNativeActivity(record))
	activity.SessionID = owner
	api.observeTurnInactivity(owner, storeevents.Event{Type: string(agentevents.CodingAgentNativeActivity), SessionID: owner, Timestamp: time.Now(), ExecutionKind: "main_agent", Data: activity})
	if err := api.saveNativeCodingActivity(owner, record); err != nil {
		log.Printf("[SUBAGENT_ROW] provider=cursor-cli session=%s task=%q state=%s action=failed reason=durable_accept_failed", owner, record.TaskID, record.State)
	}
}

// Reuse the native observer's existing tick. A known AGY child's database can
// change after its receipt while the parent is quiet; no new poller or discovery.
func nativeSubagentFileStamp(provider, path string, tasks map[string]string) string {
	stamp := nativeTerminalFileStamp(path)
	if provider != "agy-cli" {
		return stamp
	}
	var ids []string
	for id, state := range tasks {
		if (state == "launched" || state == "notified") && id != "" && !strings.ContainsAny(id, `/\`) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		stamp += "child:" + id + ":" + nativeTerminalFileStamp(filepath.Join(filepath.Dir(path), id+".db"))
	}
	return stamp
}
