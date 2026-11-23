package mcpagent

import (
	"testing"
)

func TestGetCodeExecutionAPIBaseURLAddsSessionPrefix(t *testing.T) {
	agent := &Agent{
		apiBaseURL: "http://host.docker.internal:8000",
		sessionID:  "session-123",
	}

	got := agent.getCodeExecutionAPIBaseURL()
	want := "http://host.docker.internal:8000/s/session-123"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestGetCodeExecutionAPIBaseURLKeepsExistingSessionPrefix(t *testing.T) {
	agent := &Agent{
		apiBaseURL: "http://host.docker.internal:8000/s/session-123",
		sessionID:  "session-123",
	}

	got := agent.getCodeExecutionAPIBaseURL()
	want := "http://host.docker.internal:8000/s/session-123"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
