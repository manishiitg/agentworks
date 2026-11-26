package tmuxinput

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestBrokerSerializesOneSession(t *testing.T) {
	broker := NewBroker()
	var mu sync.Mutex
	var events []string
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})

	var wg sync.WaitGroup
	for _, id := range []string{"one", "two"} {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := broker.Do(context.Background(), Request{SessionID: "same", MessageID: id}, func(context.Context) error {
				mu.Lock()
				events = append(events, id+":start")
				mu.Unlock()
				if id == "one" {
					close(firstStarted)
					<-releaseFirst
				}
				mu.Lock()
				events = append(events, id+":end")
				mu.Unlock()
				return nil
			})
			if err != nil {
				t.Errorf("Do(%s): %v", id, err)
			}
		}()
		if id == "one" {
			<-firstStarted
		}
	}

	close(releaseFirst)
	wg.Wait()
	want := []string{"one:start", "one:end", "two:start", "two:end"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestBrokerPrioritizesInterruptAfterActiveTransaction(t *testing.T) {
	broker := NewBroker()
	var mu sync.Mutex
	var order []string
	started := make(chan struct{})
	release := make(chan struct{})

	run := func(id string, priority Priority, block bool) chan error {
		done := make(chan error, 1)
		go func() {
			_, err := broker.Do(context.Background(), Request{SessionID: "same", MessageID: id, Priority: priority}, func(context.Context) error {
				if block {
					close(started)
					<-release
				}
				mu.Lock()
				order = append(order, id)
				mu.Unlock()
				return nil
			})
			done <- err
		}()
		return done
	}

	active := run("active", PriorityNormal, true)
	<-started
	normal := run("normal", PriorityNormal, false)
	interrupt := run("interrupt", PriorityInterrupt, false)
	time.Sleep(10 * time.Millisecond)
	close(release)

	for _, done := range []chan error{active, normal, interrupt} {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"active", "interrupt", "normal"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestBrokerDoesNotSerializeDifferentSessions(t *testing.T) {
	broker := NewBroker()
	started := make(chan string, 2)
	release := make(chan struct{})
	var wg sync.WaitGroup

	for _, sessionID := range []string{"a", "b"} {
		sessionID := sessionID
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = broker.Do(context.Background(), Request{SessionID: sessionID}, func(context.Context) error {
				started <- sessionID
				<-release
				return nil
			})
		}()
	}

	seen := map[string]bool{<-started: true, <-started: true}
	close(release)
	wg.Wait()
	if !seen["a"] || !seen["b"] {
		t.Fatalf("started sessions = %v", seen)
	}
}

func TestBrokerPreservesTwentyMixedSourceMessages(t *testing.T) {
	broker := NewBroker()
	worker := broker.worker("mixed")
	activeStarted := make(chan struct{})
	releaseActive := make(chan struct{})
	var mu sync.Mutex
	var order []string

	active := &queuedRequest{
		ctx:      context.Background(),
		request:  Request{SessionID: "mixed", MessageID: "active", Source: "chat"},
		response: make(chan response, 1),
		result:   Result{EnqueuedAt: time.Now()},
		operation: func(context.Context) error {
			close(activeStarted)
			<-releaseActive
			return nil
		},
	}
	worker.incoming <- active
	<-activeStarted

	queued := make([]*queuedRequest, 0, 20)
	sources := []string{"chat", "step-message", "auto-notification", "terminal"}
	for i := 0; i < 20; i++ {
		id := sources[i%len(sources)] + ":" + string(rune('A'+i))
		request := &queuedRequest{
			ctx:      context.Background(),
			request:  Request{SessionID: "mixed", MessageID: id, Source: sources[i%len(sources)]},
			response: make(chan response, 1),
			result:   Result{EnqueuedAt: time.Now()},
		}
		request.operation = func(context.Context) error {
			mu.Lock()
			order = append(order, id)
			mu.Unlock()
			return nil
		}
		queued = append(queued, request)
		worker.incoming <- request
	}

	close(releaseActive)
	if result := <-active.response; result.err != nil {
		t.Fatal(result.err)
	}
	for _, request := range queued {
		if result := <-request.response; result.err != nil {
			t.Fatal(result.err)
		}
	}

	want := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		want = append(want, sources[i%len(sources)]+":"+string(rune('A'+i)))
	}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestPriorityForKeyOnlyElevatesInterrupts(t *testing.T) {
	for _, key := range []string{"Escape", "esc", "C-c", "ctrl-c"} {
		if got := PriorityForKey(key); got != PriorityInterrupt {
			t.Errorf("PriorityForKey(%q) = %v, want interrupt", key, got)
		}
	}
	for _, key := range []string{"Enter", "Up", "Down", "Left", "Right", "C-o"} {
		if got := PriorityForKey(key); got != PriorityNormal {
			t.Errorf("PriorityForKey(%q) = %v, want normal", key, got)
		}
	}
}

func TestBrokerRetiresIdleSessionWorkerWithoutDroppingNextRequest(t *testing.T) {
	broker := newBrokerWithIdleTimeout(20 * time.Millisecond)
	run := func(id string) {
		t.Helper()
		called := false
		if _, err := broker.Do(context.Background(), Request{SessionID: "ephemeral", MessageID: id}, func(context.Context) error {
			called = true
			return nil
		}); err != nil {
			t.Fatalf("Do(%s): %v", id, err)
		}
		if !called {
			t.Fatalf("operation %s was dropped", id)
		}
	}

	run("first")
	time.Sleep(60 * time.Millisecond)
	broker.mu.Lock()
	workersAfterIdle := len(broker.workers)
	broker.mu.Unlock()
	if workersAfterIdle != 0 {
		t.Fatalf("workers after idle = %d, want 0", workersAfterIdle)
	}
	run("second")
}
