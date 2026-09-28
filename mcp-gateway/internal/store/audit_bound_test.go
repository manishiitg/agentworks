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
}
