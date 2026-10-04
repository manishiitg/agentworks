package store

import (
	"testing"
	"time"
)

func TestAuditSummaryUsesAllMatchingCalls(t *testing.T) {
	s := NewMemoryStore()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	s.AppendAudit(AuditEvent{WorkspaceID: "w", PublicName: "a", Timestamp: now, Decision: DecisionAllow, Outcome: OutcomeOK, DurationMs: 10})
	s.AppendAudit(AuditEvent{WorkspaceID: "w", PublicName: "a", Timestamp: now, Decision: DecisionDeny, Outcome: OutcomeDenied, DurationMs: 20})
	s.AppendAudit(AuditEvent{WorkspaceID: "w", PublicName: "b", Timestamp: now.Add(-24 * time.Hour), Decision: DecisionAllow, Outcome: OutcomeUpstreamError, DurationMs: 30})
	s.AppendAudit(AuditEvent{WorkspaceID: "other", PublicName: "hidden", Timestamp: now})
	got := s.SummarizeAudit(AuditFilter{WorkspaceID: "w", Limit: 1})
	if got.Total != 3 || got.Allowed != 2 || got.Denied != 1 || got.UpstreamErrors != 1 || got.AvgDurationMs != 20 {
		t.Fatalf("summary: %+v", got)
	}
	if len(got.ByDay) != 2 || got.ByDay[0].Key != "2026-09-27" || got.ByDay[0].Count != 2 {
		t.Fatalf("daily usage: %+v", got.ByDay)
	}
	if len(got.ByTool) != 2 || got.ByTool[0].Key != "a" || got.ByTool[0].Count != 2 {
		t.Fatalf("tool usage: %+v", got.ByTool)
	}
}
