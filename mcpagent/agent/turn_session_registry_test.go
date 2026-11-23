package mcpagent

import (
	"context"
	"errors"
	"testing"
)

func TestTurnSessionRegistrySurvivesAgentRunBoundary(t *testing.T) {
	sessionID := "registry-survives-run"
	closeTurnSession(sessionID)
	agent := &Agent{sessionID: sessionID}
	session, err := agent.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, ok := LookupSession(sessionID)
	if !ok || got != session {
		t.Fatalf("LookupSession() = %p, %v; want %p, true", got, ok, session)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := LookupSession(sessionID); ok {
		t.Fatal("closed session remained registered")
	}
}

func TestOlderSessionCloseDoesNotDeleteReplacement(t *testing.T) {
	sessionID := "registry-replacement"
	closeTurnSession(sessionID)
	first, _ := (&Agent{sessionID: sessionID}).Start(context.Background())
	second, _ := (&Agent{sessionID: sessionID}).Start(context.Background())
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	got, ok := LookupSession(sessionID)
	if !ok || got != second {
		t.Fatalf("replacement session lost: got %p, %v; want %p", got, ok, second)
	}
	_ = second.Close()
}

func TestSessionSendPreservesTypedDeliveryRejection(t *testing.T) {
	sessionID := "registry-typed-rejection"
	closeTurnSession(sessionID)
	session, err := (&Agent{sessionID: sessionID}).Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	_, err = session.Send(context.Background(), "   ")
	var deliveryErr *CodingAgentDeliveryError
	if !errors.As(err, &deliveryErr) {
		t.Fatalf("Send() error = %T %v, want CodingAgentDeliveryError", err, err)
	}
	if deliveryErr.Kind != DeliveryErrorKindEmptyMessage {
		t.Fatalf("error kind = %q, want %q", deliveryErr.Kind, DeliveryErrorKindEmptyMessage)
	}
}

func TestAgentRunDoesNotLeakOneTurnSession(t *testing.T) {
	sessionID := "registry-one-turn-run"
	closeTurnSession(sessionID)
	agent := &Agent{sessionID: sessionID}
	_, _ = agent.Run(context.Background(), Turn{})
	if _, ok := LookupSession(sessionID); ok {
		t.Fatal("one-turn Agent.Run left a durable session registered")
	}
}

func TestAgentCloseInvalidatesOnlyItsOwnedSession(t *testing.T) {
	sessionID := "registry-agent-close"
	closeTurnSession(sessionID)
	firstAgent := &Agent{sessionID: sessionID}
	first, _ := firstAgent.Start(context.Background())
	secondAgent := &Agent{sessionID: sessionID}
	second, _ := secondAgent.Start(context.Background())

	if err := firstAgent.Close(); err != nil {
		t.Fatal(err)
	}
	got, ok := LookupSession(sessionID)
	if !ok || got != second {
		t.Fatalf("closing replaced Agent removed newer session: got %p, %v; want %p", got, ok, second)
	}
	if err := secondAgent.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := LookupSession(sessionID); ok {
		t.Fatal("closing owning Agent left its session registered")
	}
	_ = first.Close()
}
