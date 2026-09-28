package store

import (
	"strconv"
	"testing"
)

func TestAuditHistoryIsBoundedUnderRepeatedDeniedCalls(t *testing.T) {
	s := NewMemoryStore()
	for i := 0; i < 50_005; i++ {
		s.AppendAudit(AuditEvent{ID: strconv.Itoa(i), WorkspaceID: "w", Decision: DecisionDeny})
	}
	events := s.ListAudit()
	if len(events) != 50_000 || events[0].ID != "5" || events[len(events)-1].ID != "50004" {
		t.Fatalf("audit retention: length=%d first=%s last=%s", len(events), events[0].ID, events[len(events)-1].ID)
	}
	latest := s.QueryAudit(AuditFilter{WorkspaceID: "w", Limit: 2})
	if len(latest) != 2 || latest[0].ID != "50004" || latest[1].ID != "50003" {
		t.Fatalf("audit query order after wrap: %+v", latest)
	}
	if summary := s.SummarizeAudit(AuditFilter{WorkspaceID: "w"}); summary.Total != 50_000 {
		t.Fatalf("summary after wrap counted %d events", summary.Total)
	}
}
