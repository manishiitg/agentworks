package server

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Switch after this reply (owner, 27 Nov): the switch waits for the reply, summarizes first when asked and waits for
// that, then saves the model; a newer pick replaces an older one, Cancel drops it, and a refusal is shown.
func TestChatModelSwitchWaitsForReplyThenCompactsThenSwitches(t *testing.T) {
	oldPoll := chatModelSwitchPoll
	chatModelSwitchPoll = 5 * time.Millisecond
	defer func() { chatModelSwitchPoll = oldPoll }()
	const session = "switch-test-1"
	defer cancelChatModelSwitch(session)

	var busy, compacting atomic.Bool
	var mu sync.Mutex
	var order []string
	steps0 := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), order...) }
	saved := make(chan chatModelSwitchRequest, 2)
	busy.Store(true)
	steps := chatModelSwitchSteps{
		busy: busy.Load,
		compact: func() (int, string) {
			mu.Lock()
			order = append(order, "compact")
			mu.Unlock()
			compacting.Store(true)
			return http.StatusAccepted, ""
		},
		compacting: compacting.Load,
		save: func(req chatModelSwitchRequest) (int, string) {
			mu.Lock()
			order = append(order, "save")
			mu.Unlock()
			saved <- req
			return http.StatusOK, ""
		},
	}
	job := queueChatModelSwitch(session, chatModelSwitchRequest{ProjectID: "p", Engine: "claude-code", ModelID: "sonnet", Label: "Sonnet", Compact: true})
	done := make(chan struct{})
	go func() { runChatModelSwitch(session, "owner", job, steps); close(done) }()
	time.Sleep(30 * time.Millisecond)
	if view := chatModelSwitchViewFor(session); view == nil || view.Stage != "waiting" || view.Label != "Sonnet" || len(steps0()) != 0 {
		t.Fatalf("a busy chat must keep the switch waiting, got %+v %v", view, steps0())
	}
	busy.Store(false)
	time.Sleep(30 * time.Millisecond)
	if view := chatModelSwitchViewFor(session); view == nil || view.Stage != "compacting" || len(steps0()) != 1 {
		t.Fatalf("the model must not change before the summary finishes, got %+v %v", view, steps0())
	}
	compacting.Store(false)
	select {
	case req := <-saved:
		if req.ModelID != "sonnet" {
			t.Fatalf("saved the wrong model: %+v", req)
		}
	case <-time.After(time.Second):
		t.Fatal("the switch was not saved after the summary")
	}
	<-done
	if view, got := chatModelSwitchViewFor(session), steps0(); view != nil || len(got) != 2 || got[0] != "compact" {
		t.Fatalf("a finished switch leaves nothing pending, summary first: %+v %v", view, got)
	}

	// A newer pick replaces the older one; Cancel drops it; neither saves.
	busy.Store(true)
	first := queueChatModelSwitch(session, chatModelSwitchRequest{ProjectID: "p", Engine: "codex", Label: "Codex"})
	firstDone := make(chan struct{})
	go func() { runChatModelSwitch(session, "owner", first, steps); close(firstDone) }()
	second := queueChatModelSwitch(session, chatModelSwitchRequest{ProjectID: "p", Engine: "cursor", Label: "Cursor"})
	secondDone := make(chan struct{})
	go func() { runChatModelSwitch(session, "owner", second, steps); close(secondDone) }()
	<-firstDone
	if view := chatModelSwitchViewFor(session); view == nil || view.Label != "Cursor" {
		t.Fatalf("the newer pick must be the pending one, got %+v", view)
	}
	cancelChatModelSwitch(session)
	<-secondDone
	if len(saved) != 0 || chatModelSwitchViewFor(session) != nil {
		t.Fatal("a replaced or cancelled switch must not change the model")
	}

	// A refused model is shown, not silently dropped.
	busy.Store(false)
	refused := queueChatModelSwitch(session, chatModelSwitchRequest{ProjectID: "p", Engine: "agy", Label: "Hydrogen"})
	steps.save = func(chatModelSwitchRequest) (int, string) {
		return http.StatusUnprocessableEntity, "that model is not available"
	}
	runChatModelSwitch(session, "owner", refused, steps)
	if view := chatModelSwitchViewFor(session); view == nil || view.Stage != "failed" || view.Error == "" {
		t.Fatalf("a refused switch must say why, got %+v", view)
	}
}
