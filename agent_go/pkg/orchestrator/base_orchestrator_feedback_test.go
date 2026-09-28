package orchestrator

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	baseevents "github.com/manishiitg/mcpagent/events"
)

type recordingListener struct {
	mu     sync.Mutex
	events []*baseevents.AgentEvent
}

func (l *recordingListener) HandleEvent(_ context.Context, event *baseevents.AgentEvent) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
	return nil
}

func (l *recordingListener) Name() string { return "recording" }

func (l *recordingListener) types() []baseevents.EventType {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]baseevents.EventType, 0, len(l.events))
	for _, event := range l.events {
		out = append(out, event.Type)
	}
	return out
}

// A workflow-step approval answered from any surface must leave a durable
// marker; without it a refreshed chat shows the answered prompt as pending.
func TestRequestHumanFeedbackEmitsResolutionMarkerWhenAnswered(t *testing.T) {
	listener := &recordingListener{}
	bo := &BaseOrchestrator{contextAwareBridge: listener}
	requestID := "req-orchestrator-resolved-" + time.Now().Format("150405.000000000")

	done := make(chan error, 1)
	go func() {
		_, _, err := bo.RequestHumanFeedback(context.Background(), requestID, "Approve plan?", "", "sess-1", "wf-1")
		done <- err
	}()

	store := virtualtools.GetHumanFeedbackStore()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := store.SubmitResponse(requestID, "Approve & Continue"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("feedback request never became answerable")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := <-done; err != nil {
		t.Fatalf("RequestHumanFeedback: %v", err)
	}

	types := listener.types()
	if len(types) != 2 || types[0] != events.BlockingHumanFeedback || types[1] != events.HumanFeedbackResolved {
		t.Fatalf("emitted %v, want [blocking_human_feedback human_feedback_resolved]", types)
	}
	raw, err := json.Marshal(listener.events[1].Data)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["request_id"] != requestID || payload["outcome"] != "answered" {
		t.Fatalf("marker payload must carry flat request_id/outcome, got %s", raw)
	}
}

// PLAT-365: a question asked through the real orchestrator helpers must be
// visible to its own session in the external pending-input view and
// answerable with the session-scoped reply, with its real choices, and must
// not expire before the helper stops waiting. Other sessions see nothing.
func TestOrchestratorQuestionsAreAnswerableThroughTheirSession(t *testing.T) {
	store := virtualtools.GetHumanFeedbackStore()
	bo := &BaseOrchestrator{contextAwareBridge: &recordingListener{}}
	const session = "plat365-session"

	pending := func(t *testing.T, requestID string) virtualtools.HumanFeedbackRequest {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			for _, request := range store.PendingForSession(session, time.Now()) {
				if request.UniqueID == requestID {
					return request
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("%s never appeared under its session", requestID)
		return virtualtools.HumanFeedbackRequest{}
	}
	check := func(t *testing.T, request virtualtools.HumanFeedbackRequest, options []string, allowFeedback bool) {
		t.Helper()
		if remaining := time.Until(request.ExpiresAt); remaining < humanFeedbackWait-time.Minute {
			t.Fatalf("stored request expires in %s, before the %s wait", remaining, humanFeedbackWait)
		}
		if request.AllowFeedback != allowFeedback || len(request.Options) != len(options) {
			t.Fatalf("stored policy/options = %v %v, want %v %v", request.AllowFeedback, request.Options, allowFeedback, options)
		}
		for i := range options {
			if request.Options[i] != options[i] {
				t.Fatalf("stored options %v, want %v", request.Options, options)
			}
		}
		for _, other := range store.PendingForSession("someone-else", time.Now()) {
			if other.UniqueID == request.UniqueID {
				t.Fatal("another session can see the question")
			}
		}
		if err := store.SubmitResponseForSession("someone-else", request.UniqueID, "x", time.Now()); err == nil {
			t.Fatal("another session answered the question")
		}
	}
	stamp := time.Now().Format("150405.000000000")

	t.Run("text", func(t *testing.T) {
		id := "plat365-text-" + stamp
		type result struct {
			approved bool
			feedback string
			err      error
		}
		done := make(chan result, 1)
		go func() {
			approved, feedback, err := bo.RequestHumanFeedback(context.Background(), id, "Which city?", "", session, "wf")
			done <- result{approved, feedback, err}
		}()
		check(t, pending(t, id), []string{"Approve & Continue", "Reject"}, true)
		if err := store.SubmitResponseForSession(session, id, "Mumbai", time.Now()); err != nil {
			t.Fatalf("session reply refused: %v", err)
		}
		if r := <-done; r.err != nil || r.approved || r.feedback != "Mumbai" {
			t.Fatalf("helper got %+v", r)
		}
		if err := store.SubmitResponseForSession(session, id, "Again", time.Now()); err == nil {
			t.Fatal("a second answer was accepted")
		}
	})

	t.Run("yes/no", func(t *testing.T) {
		id := "plat365-yn-" + stamp
		done := make(chan bool, 1)
		go func() {
			approved, _ := bo.RequestYesNoFeedback(context.Background(), id, "Deploy?", "Ship it", "Hold", "", session, "wf")
			done <- approved
		}()
		check(t, pending(t, id), []string{"Ship it", "Hold"}, false)
		if err := store.SubmitResponseForSession(session, id, "maybe", time.Now()); err == nil {
			t.Fatal("an invalid choice was accepted")
		}
		if err := store.SubmitResponseForSession(session, id, "Ship it", time.Now()); err != nil {
			t.Fatalf("session reply refused: %v", err)
		}
		if !<-done {
			t.Fatal("yes label did not approve")
		}
	})

	t.Run("multiple choice", func(t *testing.T) {
		id := "plat365-mc-" + stamp
		done := make(chan string, 1)
		go func() {
			choice, _ := bo.RequestMultipleChoiceFeedback(context.Background(), id, "Route?", []string{"Fix now", "Defer"}, "", session, "wf")
			done <- choice
		}()
		check(t, pending(t, id), []string{"Fix now", "Defer"}, false)
		if err := store.SubmitResponseForSession(session, id, "Defer", time.Now()); err != nil {
			t.Fatalf("session reply refused: %v", err)
		}
		if choice := <-done; choice != "option1" {
			t.Fatalf("choice = %q, want option1", choice)
		}
	})
}
