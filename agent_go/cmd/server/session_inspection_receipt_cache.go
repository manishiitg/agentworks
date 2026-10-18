package server

import (
	"context"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

type inspectionReceiptCacheKey struct {
	Store, Workspace, Session, Execution, UpdatedAt, Status string
}

// This caches immutable receipt metadata, never access decisions or transcript
// content. Every reader still authorizes the exact project and chat first.
var inspectionReceiptCache = struct {
	sync.Mutex
	items map[inspectionReceiptCacheKey]*orchEvents.ExecutionInspectionTarget
}{items: make(map[inspectionReceiptCacheKey]*orchEvents.ExecutionInspectionTarget)}

func cachedInspectionReceipt(key inspectionReceiptCacheKey) (*orchEvents.ExecutionInspectionTarget, bool) {
	inspectionReceiptCache.Lock()
	defer inspectionReceiptCache.Unlock()
	target, found := inspectionReceiptCache.items[key]
	if target == nil {
		return nil, found
	}
	copy := *target
	return &copy, found
}

func cacheInspectionReceipt(key inspectionReceiptCacheKey, target *orchEvents.ExecutionInspectionTarget) {
	inspectionReceiptCache.Lock()
	defer inspectionReceiptCache.Unlock()
	if len(inspectionReceiptCache.items) >= 512 {
		clear(inspectionReceiptCache.items)
	}
	if target != nil {
		copy := *target
		target = &copy
	}
	inspectionReceiptCache.items[key] = target
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
