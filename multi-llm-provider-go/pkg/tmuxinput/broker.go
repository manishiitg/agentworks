package tmuxinput

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// Priority controls which queued transaction runs next. It never interrupts a
// transaction that is already writing to tmux.
type Priority uint8

const (
	PriorityNormal Priority = iota
	PriorityInterrupt
)

type Request struct {
	SessionID string
	MessageID string
	Source    string
	Priority  Priority
	// InteractiveInput is raw human input from the attached terminal. A draft
	// reservation excludes programmatic pastes, but never blocks its author.
	InteractiveInput bool
	// BypassReadiness skips the WaitUntilReady gate, so this transaction may run
	// before the provider's initial prompt has been confirmed. It is set by two
	// classes of caller:
	//
	//  1. The provider's own initial prompt transaction — the one that MAKES the
	//     session ready. Agent-driven follow-up input leaves this false and
	//     therefore cannot overtake the first user message during CLI startup.
	//
	//  2. Operator-driven input from the terminal UI (POST /input, /key, and the
	//     live-attach control frames). A human watching the pane must be able to
	//     answer a trust prompt, login flow, or crash dialog that appears BEFORE
	//     the CLI ever reaches the ready state — gating those keystrokes would
	//     deadlock the very startup that readiness is waiting on.
	//
	// Serialization is unaffected either way: bypassing readiness never lets a
	// transaction interleave with one already writing to tmux.
	BypassReadiness bool
}

type Result struct {
	EnqueuedAt  time.Time
	StartedAt   time.Time
	CompletedAt time.Time
}

type Operation func(context.Context) error

type response struct {
	result Result
	err    error
}

type queuedRequest struct {
	ctx       context.Context
	request   Request
	operation Operation
	response  chan response
	result    Result
}

type sessionWorker struct {
	broker    *Broker
	sessionID string
	incoming  chan *queuedRequest
}

// Broker serializes complete tmux input transactions by tmux session. A
// transaction may contain several tmux commands (for example load-buffer,
// paste-buffer, then Enter); no other source can interleave commands while it
// is running.
type Broker struct {
	mu                     sync.Mutex
	workers                map[string]*sessionWorker
	workerIdleTimeout      time.Duration
	interactiveDrafts      map[string]*interactiveDraft
	interactiveSubmissions map[string]uint64
}

func NewBroker() *Broker {
	return newBrokerWithIdleTimeout(time.Minute)
}

func newBrokerWithIdleTimeout(timeout time.Duration) *Broker {
	return &Broker{
		workers:                make(map[string]*sessionWorker),
		workerIdleTimeout:      timeout,
		interactiveDrafts:      make(map[string]*interactiveDraft),
		interactiveSubmissions: make(map[string]uint64),
	}
}

var Default = NewBroker()

func PriorityForKey(key string) Priority {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "escape", "esc", "c-c", "ctrl-c":
		return PriorityInterrupt
	default:
		return PriorityNormal
	}
}

func (b *Broker) Do(ctx context.Context, request Request, operation Operation) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if request.SessionID == "" {
		return Result{}, errors.New("tmux input session ID is required")
	}
	if operation == nil {
		return Result{}, errors.New("tmux input operation is required")
	}
	if request.Priority != PriorityInterrupt && !request.BypassReadiness {
		if err := WaitUntilReady(ctx, request.SessionID); err != nil {
			return Result{}, err
		}
	}

	queued := &queuedRequest{
		ctx:       ctx,
		request:   request,
		operation: operation,
		response:  make(chan response, 1),
		result:    Result{EnqueuedAt: time.Now()},
	}
	if err := b.enqueue(ctx, request.SessionID, queued); err != nil {
		return queued.result, ctx.Err()
	}

	select {
	case result := <-queued.response:
		return result.result, result.err
	case <-ctx.Done():
		return queued.result, ctx.Err()
	}
}

func (b *Broker) worker(sessionID string) *sessionWorker {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.workerLocked(sessionID)
}

func (b *Broker) workerLocked(sessionID string) *sessionWorker {
	if worker := b.workers[sessionID]; worker != nil {
		return worker
	}

	worker := &sessionWorker{
		broker:    b,
		sessionID: sessionID,
		incoming:  make(chan *queuedRequest, 256),
	}
	b.workers[sessionID] = worker
	go worker.run()
	return worker
}

// enqueue publishes the request while holding the same broker lock used by
// idle retirement. A sender can therefore never retain a pointer to a worker
// that exits before the request reaches its channel.
func (b *Broker) enqueue(ctx context.Context, sessionID string, request *queuedRequest) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	worker := b.workerLocked(sessionID)
	select {
	case worker.incoming <- request:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *sessionWorker) retireIfIdle() bool {
	w.broker.mu.Lock()
	defer w.broker.mu.Unlock()
	if w.broker.workers[w.sessionID] != w || len(w.incoming) != 0 {
		return false
	}
	delete(w.broker.workers, w.sessionID)
	return true
}

func (w *sessionWorker) run() {
	var interrupts []*queuedRequest
	var normal []*queuedRequest

	appendRequest := func(request *queuedRequest) {
		if request.request.Priority == PriorityInterrupt {
			interrupts = append(interrupts, request)
			return
		}
		normal = append(normal, request)
	}

	for {
		if len(interrupts) == 0 && len(normal) == 0 {
			idleTimeout := w.broker.workerIdleTimeout
			if idleTimeout <= 0 {
				appendRequest(<-w.incoming)
			} else {
				timer := time.NewTimer(idleTimeout)
				select {
				case request := <-w.incoming:
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					appendRequest(request)
				case <-timer.C:
					if w.retireIfIdle() {
						return
					}
					continue
				}
			}
		}

		// Collect everything that arrived while the previous transaction was
		// running so an interrupt can go ahead of queued normal input.
		for {
			select {
			case request := <-w.incoming:
				appendRequest(request)
			default:
				goto execute
			}
		}

	execute:
		var request *queuedRequest
		if len(interrupts) > 0 {
			request = interrupts[0]
			interrupts = interrupts[1:]
		} else {
			request = normal[0]
			normal = normal[1:]
		}

		if err := request.ctx.Err(); err != nil {
			request.result.CompletedAt = time.Now()
			request.response <- response{result: request.result, err: err}
			continue
		}

		request.result.StartedAt = time.Now()
		// Text typed in the CLI's own composer no longer blocks a send: only the person who owns the
		// chat can reach that terminal, and refusing produced false "finish or clear the terminal
		// draft" errors (Confida, 2026-09-30) for a case that is rare.
		err := request.operation(request.ctx)
		request.result.CompletedAt = time.Now()
		request.response <- response{result: request.result, err: err}
	}
}
