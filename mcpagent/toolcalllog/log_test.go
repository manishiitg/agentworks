package toolcalllog

import "testing"

func TestSnapshotIncludesRunningAndCompletedCalls(t *testing.T) {
	sessionID := "test-snapshot-session"
	Clear(sessionID)
	t.Cleanup(func() { Clear(sessionID) })

	runningID := RecordStart(sessionID, "execute_shell_command", `{"command":"sleep 10"}`)
	calls := Snapshot(sessionID)
	if len(calls) != 1 {
		t.Fatalf("expected one running call, got %d", len(calls))
	}
	if calls[0].ID != runningID || calls[0].Status != "running" || calls[0].SessionID != sessionID {
		t.Fatalf("unexpected running snapshot: %+v", calls[0])
	}

	RecordEnd(sessionID, runningID, "execute_shell_command", `{"command":"sleep 10"}`, "done", calls[0].StartedAt)
	calls = Snapshot(sessionID)
	if len(calls) != 1 {
		t.Fatalf("expected one completed call, got %d", len(calls))
	}
	if calls[0].ID != runningID || calls[0].Status != "done" || calls[0].Result != "done" {
		t.Fatalf("unexpected completed snapshot: %+v", calls[0])
	}
}

func TestSnapshotBySessionPrefix(t *testing.T) {
	prefix := "sub-exec-step-query-test-"
	matchingSession := prefix + "1"
	otherSession := "sub-exec-step-other-1"
	Clear(matchingSession)
	Clear(otherSession)
	t.Cleanup(func() {
		Clear(matchingSession)
		Clear(otherSession)
	})

	matchingID := RecordStart(matchingSession, "agent_browser", `{"action":"snapshot"}`)
	RecordStart(otherSession, "execute_shell_command", `{"command":"pwd"}`)

	calls := SnapshotBySessionPrefix(prefix)
	if len(calls) != 1 {
		t.Fatalf("expected one matching call, got %d: %+v", len(calls), calls)
	}
	if calls[0].ID != matchingID {
		t.Fatalf("expected matching call %q, got %+v", matchingID, calls[0])
	}
	if calls[0].SessionID == otherSession {
		t.Fatalf("unexpected non-matching call in prefix snapshot: %+v", calls[0])
	}
}
