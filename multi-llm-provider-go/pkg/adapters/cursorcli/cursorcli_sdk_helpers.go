package cursorcli

import (
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"sync/atomic"
	"time"
)

var cursorSDKHelperListener atomic.Pointer[func(string, llmtypes.NativeRecord)]

// SetSDKHelperListener observes only this bridge's exact native Task calls.
// No prompt, tool result or parent token total is copied into helper records.
func SetSDKHelperListener(listener func(string, llmtypes.NativeRecord)) {
	if listener == nil {
		cursorSDKHelperListener.Store(nil)
	} else {
		cursorSDKHelperListener.Store(&listener)
	}
}

func (c *CursorCLIAdapter) sdkHelperObserver(owner string) func(cursorSDKEvent) {
	active := map[string]llmtypes.NativeRecord{}
	publish := func(r llmtypes.NativeRecord) {
		if listener := cursorSDKHelperListener.Load(); listener != nil {
			(*listener)(owner, r)
		}
	}
	return func(event cursorSDKEvent) {
		if event.Type == "helper_runtime_closed" {
			for id, r := range active {
				r.State = "interrupted"
				r.Timestamp = time.Now()
				r.ID = "sdk-task:" + id + ":interrupted"
				publish(r)
				delete(active, id)
			}
			return
		}
		if event.CallID == "" || event.AgentID == "" {
			return
		}
		if event.Status == "launch_ack" || event.Status == "unreported" {
			if c.logger != nil {
				c.logger.Infof("[SUBAGENT_ROW] provider=cursor-cli session=%s task=%s state=skipped reason=completion_unreported", owner, event.CallID)
			}
			return
		}
		if event.Status != "started" && event.Status != "finished" && event.Status != "failed" {
			return
		}
		r, known := active[event.CallID]
		if !known {
			if event.Status != "started" {
				return
			} // Never attribute an unknown completion to this chat's active turn.
			r = llmtypes.NativeRecord{Provider: "cursor-cli", SessionID: event.AgentID, TurnID: event.RunID, TaskRunID: event.CallID, TaskID: event.CallID, ToolID: event.CallID, Kind: "helper", Name: "Subagent", HelperType: "task", Origin: "sdk_helper", StartedAt: time.Now()}
		} else if event.Status == "started" {
			return
		}
		r.State = event.Status
		r.Timestamp = time.Now()
		r.ID = "sdk-task:" + event.CallID + ":" + event.Status
		if event.DurationMS >= 0 {
			r.DurationMS = event.DurationMS
		}
		if event.Status == "started" {
			active[event.CallID] = r
		} else {
			delete(active, event.CallID)
		}
		publish(r)
	}
}
