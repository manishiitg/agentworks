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
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
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
	// Plan: the graph or its step configuration may have changed.
	Plan Kind = "plan"
	// UIControl: an agent queued a right-panel view action for a chat
	// session. It only wakes that session's tabs to sync with the ui-control
	// broker; the action itself is claimed there. Owner-only (PublishToUser).
	UIControl Kind = "ui_control"
)

// Notice is one change. Workflow is the workspace path ("Workflow/<folder>")
// for workflow-scoped kinds and empty for global ones.
type Notice struct {
	Kind     Kind   `json:"kind"`
	Workflow string `json:"workflow,omitempty"`
	// Session names a chat session for session-scoped kinds (ui_control).
	Session string `json:"session,omitempty"`
	// User, when set, limits delivery to that user's streams. Never sent.
	User string `json:"-"`
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
	b.publish(Notice{Kind: kind, Workflow: workflow})
}

// PublishToUser publishes a session-scoped notice that only the given user's
// streams receive (the stream drops notices addressed to another user).
func (b *Bus) PublishToUser(kind Kind, user, session string) {
	if user == "" {
		return
	}
	b.publish(Notice{Kind: kind, Session: session, User: user})
}

func (b *Bus) publish(n Notice) {
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

// IsPlanPath reports files that change the shared Graph/Plan view. Keep this
// classification in one place for workspace tools and server-side writes.
func IsPlanPath(p string) bool {
	// The owner prefix is irrelevant to the shape of the path.
	ref, ok := workspaceref.Parse(p)
	if !ok || ref.Logical() == "" {
		return false
	}
	parts := strings.Split(ref.Logical(), "/")
	start := 0
	rootEnd := 0
	switch {
	case len(parts) >= start+2 && (parts[start] == "Workflow" || parts[start] == "Crew") && parts[start+1] != "":
		rootEnd = start + 2
	case len(parts) >= start+4 && parts[start] == "Chats" && (parts[start+1] == "Work" || parts[start+1] == "Code") && parts[start+2] == "projects" && parts[start+3] != "":
		rootEnd = start + 4
	default:
		return false
	}
	rest := parts[rootEnd:]
	if len(rest) == 1 {
		return rest[0] == "workflow.json" || (parts[0] == "Workflow" && rest[0] == "relay.py")
	}
	return len(rest) == 2 && rest[0] == "planning" &&
		(rest[1] == "plan.json" || rest[1] == "step_config.json")
}

// PublishPlanPath keeps Workflow/Crew notices scoped. Legacy Crew and Code
// project paths use a path-free notice; their read endpoints still authorize
// the refetch, and the feed reveals no private project path to other users.
func PublishPlanPath(p string) {
	if !IsPlanPath(p) {
		return
	}
	Default.Publish(Plan, WorkflowRoot(p))
}

// PublishWorkflow publishes a scoped notice for Workflow/ and Crew/ roots.
func PublishWorkflow(kind Kind, path string) {
	if wf := WorkflowRoot(path); wf != "" {
		Default.Publish(kind, wf)
	}
}
