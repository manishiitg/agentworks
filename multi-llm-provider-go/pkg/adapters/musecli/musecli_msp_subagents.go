package musecli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A subagent Muse starts in a `muse serve` chat is a `subagent` item on the
// parent session. Each one becomes a helper row in the chat (the same row
// Claude, Codex and Cursor helpers use), and the row opens the child's own
// conversation, which Muse writes under the parent's log folder.

// museMSPSubagentItem is the part of a `subagent` item the chat shows.
type museMSPSubagentItem struct {
	ItemID         string `json:"itemId"`
	Kind           string `json:"kind"`
	TurnID         string `json:"turnId"`
	Status         string `json:"status"`
	SubagentID     string `json:"subagentId"`
	Role           string `json:"role"`
	Objective      string `json:"objective"`
	ChildSessionID string `json:"childSessionId"`
	ControlStatus  string `json:"controlStatus"`
	DurationMS     int64  `json:"durationMs"`
}

type museMSPChild struct {
	parentSessionID string
	childSessionID  string
	dataHome        string
	name            string
	state           string
	started         time.Time
	updated         time.Time
}

var museMSPHelperListener atomic.Pointer[func(string, llmtypes.NativeRecord)]

// museMSPChildren: chat + "\x00" + item ID -> the child it started.
var museMSPChildren = struct {
	sync.Mutex
	m map[string]*museMSPChild
}{m: map[string]*museMSPChild{}}

// SetMSPHelperListener receives a helper record each time one of a chat's
// Muse subagents starts or ends, keyed by the chat.
func SetMSPHelperListener(listener func(string, llmtypes.NativeRecord)) {
	if listener == nil {
		museMSPHelperListener.Store(nil)
	} else {
		museMSPHelperListener.Store(&listener)
	}
}

// museMSPSubagentState is the helper row's state: started while it runs,
// then completed, failed or stopped. "" while Muse has not decided.
func museMSPSubagentState(item museMSPSubagentItem) string {
	// The generic item status is the terminal authority; the control status
	// says a running child already handed back its result.
	switch item.Status {
	case "completed":
		return "completed"
	case "failed":
		return "failed"
	case "cancelled", "interrupted":
		return "stopped"
	}
	switch item.ControlStatus {
	case "resultReady", "closed":
		return "completed"
	case "accepted", "starting", "running", "closing", "recoveryPending", "manualReconciliation":
		return "started"
	}
	if item.Status == "inProgress" {
		return "started"
	}
	return ""
}

// observeMuseMSPSubagent records a subagent item of this chat's session and
// publishes its helper row when its state changes.
func observeMuseMSPSubagent(s *museMSPSession, msg museRPCMessage) {
	if s == nil || s.owner == "" || (msg.Method != "item/started" && msg.Method != "item/updated" && msg.Method != "item/completed") {
		return
	}
	var p struct {
		SessionID string              `json:"sessionId"`
		Item      museMSPSubagentItem `json:"item"`
	}
	if json.Unmarshal(msg.Params, &p) != nil || p.Item.Kind != "subagent" || p.Item.ItemID == "" {
		return
	}
	s.mu.Lock()
	parent := s.sessionID
	s.mu.Unlock()
	if p.SessionID != "" && parent != "" && p.SessionID != parent {
		return
	}
	if p.SessionID != "" {
		parent = p.SessionID
	}
	item := p.Item
	state := museMSPSubagentState(item)
	now := time.Now()
	key := s.owner + "\x00" + item.ItemID
	museMSPChildren.Lock()
	child := museMSPChildren.m[key]
	if child == nil {
		child = &museMSPChild{started: now}
		museMSPChildren.m[key] = child
	}
	child.parentSessionID, child.dataHome, child.updated = parent, s.dataHome, now
	if item.ChildSessionID != "" {
		child.childSessionID = item.ChildSessionID
	}
	if name := museMSPSubagentName(item); name != "" {
		child.name = name
	}
	changed := state != "" && state != child.state && !(child.state != "" && child.state != "started" && state == "started")
	if changed {
		child.state = state
	}
	taskID := item.SubagentID
	if taskID == "" {
		taskID = item.ItemID
	}
	record := llmtypes.NativeRecord{
		ID: "msp-subagent:" + item.ItemID + ":" + state, Provider: "muse-cli", SessionID: parent, TurnID: item.TurnID,
		Kind: "helper", State: state, Origin: "msp_subagent", Name: child.name, HelperType: item.Role,
		HelperDescription: strings.TrimSpace(item.Objective), TaskID: taskID,
		ToolID: item.ItemID, StartedAt: child.started, Timestamp: now,
	}
	if state != "started" && state != "" {
		record.EndedAt = now
		record.DurationMS = item.DurationMS
	}
	museMSPChildren.Unlock()
	if !changed {
		return
	}
	if listener := museMSPHelperListener.Load(); listener != nil {
		(*listener)(s.owner, record)
	}
}

func museMSPSubagentName(item museMSPSubagentItem) string {
	if objective := strings.TrimSpace(item.Objective); objective != "" {
		return objective
	}
	return strings.TrimSpace(item.Role)
}

// MuseSubagentConversation reads, as it is now, the conversation of the
// subagent this chat's Muse started with the item itemID. Only the chat's
// own account folder is read.
func MuseSubagentConversation(owner, itemID string) (llmtypes.NativeSubagentTranscript, bool) {
	owner, itemID = strings.TrimSpace(owner), strings.TrimSpace(itemID)
	museMSPChildren.Lock()
	found := museMSPChildren.m[owner+"\x00"+itemID]
	var child museMSPChild
	if found != nil {
		child = *found
	}
	museMSPChildren.Unlock()
	if found == nil || child.childSessionID == "" || strings.ContainsAny(child.childSessionID, `/\`) {
		return llmtypes.NativeSubagentTranscript{}, false
	}
	parentLog := museSessionLogPath(child.parentSessionID, child.dataHome)
	if parentLog == "" {
		return llmtypes.NativeSubagentTranscript{}, false
	}
	path := filepath.Join(filepath.Dir(parentLog), "subagent", child.childSessionID, "session.jsonl")
	messages, ok := readMuseTranscriptMessages(path, "", true)
	if !ok {
		return llmtypes.NativeSubagentTranscript{}, false
	}
	updated := child.updated
	if info, err := os.Stat(path); err == nil {
		updated = info.ModTime()
	}
	status := "running"
	switch child.state {
	case "completed", "failed":
		status = child.state
	case "stopped":
		status = "failed"
	}
	name := child.name
	if name == "" {
		name = "Helper agent"
	}
	return llmtypes.NativeSubagentTranscript{ID: child.childSessionID, ParentID: child.parentSessionID, ToolUseID: itemID, Name: name, Provider: "muse-cli", Status: status, StartedAt: child.started, UpdatedAt: updated, Messages: messages}, true
}
