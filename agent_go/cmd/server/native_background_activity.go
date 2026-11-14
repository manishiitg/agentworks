package server

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/nativeusage"
)

// Task facts are scoped to one owned native runtime, not a parent turn's totals.
func nativeBackgroundAliases(r llmtypes.NativeRecord) []string {
	if r.Provider == "" || r.SessionID == "" {
		return nil
	}
	prefix := r.Provider + "\x00" + r.SessionID + "\x00"
	var keys []string
	if r.TaskID != "" {
		keys = append(keys, prefix+"task:"+r.TaskID)
	}
	if r.ToolID != "" {
		keys = append(keys, prefix+"tool:"+r.ToolID)
	}
	return keys
}
func (api *StreamingAPI) nativeBackgroundFacts(session string, r llmtypes.NativeRecord) llmtypes.NativeRecord {
	if r.Kind != "helper" && r.Kind != "background_command" {
		return r
	}
	switch r.State {
	case "running":
		r.State = "started"
	case "started", "progress", "finished", "completed", "failed", "stopped", "interrupted":
	default:
		return r // proposals/rosters are not evidence that work is running
	}
	r.Text, r.Output = "", ""
	keys := nativeBackgroundAliases(r)
	if len(keys) == 0 {
		return r
	}
	api.nativeBackgroundMu.Lock()
	defer api.nativeBackgroundMu.Unlock()
	if api.nativeBackground == nil {
		api.nativeBackground = map[string]map[string]llmtypes.NativeRecord{}
	}
	facts := api.nativeBackground[session]
	if facts == nil {
		facts = map[string]llmtypes.NativeRecord{}
		api.nativeBackground[session] = facts
	}
	var previous llmtypes.NativeRecord
	if r.WorkID != "" {
		previous = facts["work:"+r.WorkID]
	}
	if previous.WorkID == "" && r.TaskRunID != "" {
		for _, key := range keys {
			if p, ok := facts["run:"+key+":"+r.TaskRunID]; ok {
				previous = p
				break
			}
		}
	}
	if previous.WorkID == "" {
		for _, key := range keys {
			if p, ok := facts[key]; ok {
				previous = p
				break
			}
		}
	}
	sameRun := previous.WorkID != "" && (r.TaskRunID == "" || previous.TaskRunID == "" || r.TaskRunID == previous.TaskRunID)
	if sameRun {
		if r.HelperType == "" {
			r.HelperType = previous.HelperType
		}
		if r.HelperDescription == "" {
			r.HelperDescription = previous.HelperDescription
		}
		if r.CurrentStep == "" {
			r.CurrentStep = previous.CurrentStep
		}
		if r.HelperMode == "" {
			r.HelperMode = previous.HelperMode
		}
		if r.CommandLabel == "" {
			r.CommandLabel = previous.CommandLabel
		}
		if r.BackgroundMode == "" {
			r.BackgroundMode = previous.BackgroundMode
		}
		if r.TaskRunID == "" && !previous.StartedAt.IsZero() && !r.Timestamp.IsZero() && r.Timestamp.Before(previous.StartedAt) {
			return previous
		}
		if previous.TokenSource == "task_report" && r.TokenSource == "native_usage" {
			r.Tokens = previous.Tokens
			r.TokenSource = previous.TokenSource
		}
		r.WorkID = previous.WorkID
		if r.TaskRunID == "" {
			r.TaskRunID = previous.TaskRunID
		}
		if r.StartedAt.IsZero() {
			r.StartedAt = previous.StartedAt
		}
		if r.Tokens == nil {
			r.Tokens = previous.Tokens
			r.TokenSource = previous.TokenSource
		}
		if !previous.EndedAt.IsZero() && (r.State == "started" || r.State == "progress") {
			if r.Origin != "background_usage" {
				return previous
			}
			r.State = previous.State
			r.EndedAt = previous.EndedAt
		}
	}
	if r.WorkID == "" {
		sum := sha256.Sum256([]byte(keys[0] + "\x00" + r.TaskRunID))
		r.WorkID = fmt.Sprintf("native-work-%x", sum[:20])
	}
	if r.State == "progress" {
		r.State = "started"
	}
	if r.StartedAt.IsZero() && r.State == "started" {
		r.StartedAt = r.Timestamp
	}
	switch r.State {
	case "finished", "completed", "failed", "stopped", "interrupted":
		if r.EndedAt.IsZero() {
			r.EndedAt = r.Timestamp
		}
	}
	if r.StartedAt.IsZero() && !r.EndedAt.IsZero() && r.DurationMS > 0 {
		r.StartedAt = r.EndedAt.Add(-time.Duration(r.DurationMS) * time.Millisecond)
	}
	if !r.StartedAt.IsZero() && !r.EndedAt.IsZero() {
		r.DurationMS = r.EndedAt.Sub(r.StartedAt).Milliseconds()
	}
	if r.Tokens != nil && *r.Tokens < 0 {
		r.Tokens = nil
	}
	for key, known := range facts {
		if known.WorkID == r.WorkID {
			facts[key] = r
		}
	}
	facts["work:"+r.WorkID] = r
	for _, key := range keys {
		if r.TaskRunID != "" {
			facts["run:"+key+":"+r.TaskRunID] = r
		}
		if current, ok := facts[key]; ok && current.WorkID != r.WorkID && current.TaskRunID != "" && r.TaskRunID != "" && current.TaskRunID != r.TaskRunID && r.State != "started" {
			continue
		}
		facts[key] = r
	}
	return r
}
func (api *StreamingAPI) activeNativeBackground(session string) []llmtypes.NativeRecord {
	api.nativeBackgroundMu.Lock()
	defer api.nativeBackgroundMu.Unlock()
	var result []llmtypes.NativeRecord
	seen := map[string]bool{}
	for key, r := range api.nativeBackground[session] {
		if !strings.HasPrefix(key, "work:") {
			continue
		}
		if r.State == "started" && r.EndedAt.IsZero() && !seen[r.WorkID] {
			seen[r.WorkID] = true
			result = append(result, r)
		}
	}
	return result
}

// Existing confirmed runtime-loss/Stop paths supply the end, never a timer or
// a missing roster entry. A native result drained first already closed its row.
func (api *StreamingAPI) endNativeBackground(session, provider, native string) {
	for _, r := range api.activeNativeBackground(session) {
		if provider != "" && r.Provider != provider || native != "" && r.SessionID != native {
			continue
		}
		r.ID += ":runtime-ended"
		r.State = "interrupted"
		r.Timestamp = time.Now()
		r.EndedAt = r.Timestamp
		_ = api.saveNativeCodingActivity(session, r)
	}
}
func (api *StreamingAPI) wakeNativeBackgroundAccounting(session string) {
	if api.nativeCostStopped.Load() {
		return
	}
	api.nativeCostsMu.Lock()
	a := api.nativeCosts[session]
	api.nativeCostsMu.Unlock()
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.retired {
		a.state.Active = true
		a.startLocked()
		a.save()
	}
}

// A native command becomes background work when its reply ends while the
// provider still owns the process. Ordinary foreground tools are not rows.
var nativeBackgroundRuntime = func(session string) (string, string) {
	if s, ok := mcpagent.LookupSession(session); ok {
		if h := s.Snapshot(); h != nil {
			return string(h.Provider.Provider), h.Provider.NativeSessionID
		}
	}
	return "", ""
}

func (api *StreamingAPI) observeNativeBackgroundCommands(session string, event storeevents.Event) {
	if api.eventStore == nil || event.Data == nil {
		return
	}
	switch event.Type {
	case "unified_completion":
		provider, native := nativeBackgroundRuntime(session)
		if provider != "codex-cli" || native == "" {
			return
		}
		for _, command := range api.eventStore.OpenNativeCommands(session) {
			if command.Name != "exec_command" || !strings.HasPrefix(command.ID, "exec-") {
				continue
			}
			_ = api.saveNativeCodingActivity(session, llmtypes.NativeRecord{ID: "detached:" + command.ID, Provider: provider, SessionID: native, TaskID: command.ID, ToolID: command.ID, Kind: "background_command", Origin: "reply_detached_command", State: "started", Timestamp: command.StartedAt, StartedAt: command.StartedAt})
		}
	case "tool_call_end", "tool_call_error":
		var id, state string
		at := event.Timestamp
		switch data := event.Data.Data.(type) {
		case *agentevents.ToolCallEndEvent:
			id, state = data.ToolCallID, "finished"
			if data.SyntheticSettle {
				state = "interrupted"
			}
		case *agentevents.ToolCallErrorEvent:
			id, state = data.ToolCallID, "failed"
		}
		for _, r := range api.activeNativeBackground(session) {
			if r.Origin != "reply_detached_command" || r.TaskID != id {
				continue
			}
			r.State = state
			r.Timestamp = at
			r.EndedAt = at
			_ = api.saveNativeCodingActivity(session, r)
		}
	}
}

// UI projection only. Billing remains solely in the existing native ledger.
// Native task reports win; a parent's aggregate is never apportioned.
func (api *StreamingAPI) projectNativeBackgroundUsage(session, provider, native, account string, records []nativeusage.Record) {
	api.nativeBackgroundMu.Lock()
	var tasks []llmtypes.NativeRecord
	seen := map[string]bool{}
	for key, task := range api.nativeBackground[session] {
		if !strings.HasPrefix(key, "work:") {
			continue
		}
		if task.Provider == provider && task.SessionID == native && !seen[task.WorkID] {
			seen[task.WorkID] = true
			tasks = append(tasks, task)
		}
	}
	api.nativeBackgroundMu.Unlock()
	for _, task := range tasks {
		if task.TokenSource == "task_report" {
			continue
		}
		var total int64
		measured := false
		used := map[string]bool{}
		for _, r := range records {
			if r.TaskID == "" || r.TaskID != task.TaskID || r.Timestamp.IsZero() || task.StartedAt.IsZero() || r.Timestamp.Before(task.StartedAt) || (!task.EndedAt.IsZero() && r.Timestamp.After(task.EndedAt)) {
				continue
			}
			key := r.Key(provider, account)
			if used[key] {
				continue
			}
			used[key] = true
			if r.Input < 0 || r.Output < 0 || r.CacheRead < 0 || r.CacheWrite < 0 {
				continue
			}
			measured = true
			total += r.Input + r.Output
			if !r.InputIncludesCache {
				total += r.CacheRead + r.CacheWrite
			}
		}
		if !measured || task.Tokens != nil && *task.Tokens == total {
			continue
		}
		task.ID = fmt.Sprintf("%s:usage:%d", task.WorkID, total)
		task.Origin = "background_usage"
		task.TokenSource = "native_usage"
		task.Tokens = &total
		_ = api.saveNativeCodingActivity(session, task)
	}
}

func (api *StreamingAPI) knownNativeBackground(session string) []llmtypes.NativeRecord {
	api.nativeBackgroundMu.Lock()
	defer api.nativeBackgroundMu.Unlock()
	var result []llmtypes.NativeRecord
	seen := map[string]bool{}
	for key, r := range api.nativeBackground[session] {
		if !strings.HasPrefix(key, "work:") {
			continue
		}
		if !seen[r.WorkID] {
			seen[r.WorkID] = true
			result = append(result, r)
		}
	}
	return result
}
func (api *StreamingAPI) nativeBackgroundKeepsAccounting(session, provider, native string) bool {
	for _, r := range api.activeNativeBackground(session) {
		if r.Provider == provider && (native == "" || r.SessionID == native) {
			return true
		}
	}
	return false
}

// Duration-only progress needs no new durable event: the browser formats the
// existing real start time. Native usage changes still reach the same row.
func (api *StreamingAPI) nativeBackgroundReportUnchanged(session string, r llmtypes.NativeRecord) bool {
	if r.Tokens == nil {
		return true
	}
	api.nativeBackgroundMu.Lock()
	defer api.nativeBackgroundMu.Unlock()
	for _, key := range nativeBackgroundAliases(r) {
		previous, ok := api.nativeBackground[session]["run:"+key+":"+r.TaskRunID]
		if ok && previous.TokenSource == "task_report" && previous.Tokens != nil && *previous.Tokens == *r.Tokens {
			return true
		}
	}
	return false
}
