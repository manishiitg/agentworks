// Package livefeed is an in-process "something changed" bus for the header
// and right-pane live stream (GET /api/live). Writers call Publish after a
// change commits; each open stream receives coalesced notices and refetches
// the affected data through the normal read endpoints.
//
// Notices carry no data, only a kind and an optional workflow path, so the
// feed never bypasses the read endpoints' own access rules. Chat
// conversations are out of scope: they keep their per-session streams.
//
// See docs/design/live_update_feed.md.
package livefeed

import (
	"strings"
	"sync"
)

// Kind names what changed. Keep in sync with frontend/src/services/liveFeed.ts.
type Kind string

const (
	// Sessions: the set or status of running sessions/executions changed
	// (header activity monitor). Never published per event, only on status
	// transitions.
	Sessions Kind = "sessions"
	// Schedules: the workflow schedule summary changed (header).
	Schedules Kind = "schedules"
	// Notifications: org-dashboard notifications for a workflow changed.
	Notifications Kind = "notifications"
	// HumanInputs: report human inputs ("needs your input") for a workflow changed.
	HumanInputs Kind = "human_inputs"
	// Report: a workflow's Report dashboard data may have changed.
	Report Kind = "report"
)

// Notice is one change. Workflow is the workspace path ("Workflow/<folder>")
// for workflow-scoped kinds and empty for global ones.
type Notice struct {
	Kind     Kind   `json:"kind"`
	Workflow string `json:"workflow,omitempty"`
}

// Subscriber receives coalesced notices. Wake is signalled (non-blocking)
// whenever something is pending; the stream loop then calls Drain.
type Subscriber struct {
	Wake chan struct{}

	mu      sync.Mutex
	pending map[Notice]struct{}
	order   []Notice
	resync  bool
}

// maxPending bounds per-subscriber memory. Past it the subscriber is told to
// resync (refetch everything) instead of accumulating notices.
const maxPending = 256

func (s *Subscriber) add(n Notice) {
	s.mu.Lock()
	if !s.resync {
		if _, dup := s.pending[n]; !dup {
			if len(s.order) >= maxPending {
				s.resync = true
				s.pending = map[Notice]struct{}{}
				s.order = nil
			} else {
				s.pending[n] = struct{}{}
				s.order = append(s.order, n)
			}
		}
	}
	s.mu.Unlock()
	select {
	case s.Wake <- struct{}{}:
	default:
	}
}

// Drain returns the pending notices in publish order (duplicates collapsed)
// and whether the subscriber overflowed and must resync instead.
func (s *Subscriber) Drain() (notices []Notice, resync bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	notices, resync = s.order, s.resync
	s.order = nil
	s.pending = map[Notice]struct{}{}
	s.resync = false
	return notices, resync
}

// Bus fans notices out to subscribers.
type Bus struct {
	mu   sync.RWMutex
	subs map[*Subscriber]struct{}
}

func NewBus() *Bus {
	return &Bus{subs: map[*Subscriber]struct{}{}}
}

func (b *Bus) Subscribe() *Subscriber {
	s := &Subscriber{Wake: make(chan struct{}, 1), pending: map[Notice]struct{}{}}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

func (b *Bus) Unsubscribe(s *Subscriber) {
	b.mu.Lock()
	delete(b.subs, s)
	b.mu.Unlock()
}

// Subscribers returns the number of open streams.
func (b *Bus) Subscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

// Publish is non-blocking and safe from any goroutine; with no open streams
// it is a map read.
func (b *Bus) Publish(kind Kind, workflow string) {
	n := Notice{Kind: kind, Workflow: workflow}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for s := range b.subs {
		s.add(n)
	}
}

// Default is the process-wide bus. Each deployment runs one agent server, so
// an in-process bus reaches every open stream.
var Default = NewBus()

// Publish publishes on the Default bus.
func Publish(kind Kind, workflow string) { Default.Publish(kind, workflow) }

// WorkflowRoot reduces any path inside a workflow ("Workflow/<folder>") or a
// shared crew ("Crew/<id>") to that root, the key clients subscribe with.
// Empty for other paths.
func WorkflowRoot(p string) string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(p), "/"), "/")
	if len(parts) < 2 || (parts[0] != "Workflow" && parts[0] != "Crew") || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// PublishWorkflow publishes a workflow-scoped notice for any path inside a
// workflow; paths outside Workflow/ are ignored.
func PublishWorkflow(kind Kind, path string) {
	if wf := WorkflowRoot(path); wf != "" {
		Default.Publish(kind, wf)
	}
}
