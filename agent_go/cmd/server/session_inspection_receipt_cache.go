package server

import (
	"container/list"
	"context"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

type inspectionReceiptCacheKey struct {
	Store, Workspace, Session, Execution, UpdatedAt, Status string
}

type inspectionReceiptCacheEntry struct {
	key    inspectionReceiptCacheKey
	target orchEvents.ExecutionInspectionTarget
}

// Cache receipt metadata only. Positive authorization is checked before every
// use; defensive copies prevent a caller from changing cached targets.
var inspectionReceiptCache = struct {
	sync.Mutex
	items map[inspectionReceiptCacheKey]*list.Element
	order list.List
}{items: make(map[inspectionReceiptCacheKey]*list.Element)}

func cachedInspectionReceipt(key inspectionReceiptCacheKey) (*orchEvents.ExecutionInspectionTarget, bool) {
	inspectionReceiptCache.Lock()
	defer inspectionReceiptCache.Unlock()
	element, found := inspectionReceiptCache.items[key]
	if !found {
		return nil, false
	}
	inspectionReceiptCache.order.MoveToFront(element)
	copy := element.Value.(inspectionReceiptCacheEntry).target
	return &copy, true
}

func cacheInspectionReceipt(key inspectionReceiptCacheKey, target *orchEvents.ExecutionInspectionTarget) {
	if target == nil {
		return
	}
	inspectionReceiptCache.Lock()
	defer inspectionReceiptCache.Unlock()
	entry := inspectionReceiptCacheEntry{key: key, target: *target}
	if element, found := inspectionReceiptCache.items[key]; found {
		element.Value = entry
		inspectionReceiptCache.order.MoveToFront(element)
		return
	}
	inspectionReceiptCache.items[key] = inspectionReceiptCache.order.PushFront(entry)
	if len(inspectionReceiptCache.items) > 512 {
		oldest := inspectionReceiptCache.order.Back()
		delete(inspectionReceiptCache.items, oldest.Value.(inspectionReceiptCacheEntry).key)
		inspectionReceiptCache.order.Remove(oldest)
	}
}

func readDurableInspectionReceipt(ctx context.Context, key inspectionReceiptCacheKey) *orchEvents.ExecutionInspectionTarget {
	content, exists, err := readFileFromWorkspaceQueryWithLimit(ctx, orchEvents.BackgroundAgentTranscriptPath(key.Workspace, key.Session, key.Execution), "", 4<<20)
	if err != nil || !exists {
		return nil
	}
	transcript, err := orchEvents.ParseBackgroundAgentTranscript(content)
	if err != nil || transcript == nil || transcript.SessionID != key.Session || transcript.AgentID != key.Execution || transcript.Inspection == nil {
		return nil
	}
	target := *transcript.Inspection
	if target.SessionID != key.Session || target.ExecutionID != key.Execution || target.WorkspacePath != key.Workspace || target.StepID == "" || (target.WorkflowKind != "" && target.WorkflowKind != "workflow") || !workflowStepInspectionKind(target.ExecutionKind) || target.ExecutionKind == "agent_item" {
		return nil
	}
	target.Name = redaction.Text(target.Name)
	cacheInspectionReceipt(key, &target)
	return &target
}
