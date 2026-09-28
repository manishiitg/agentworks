package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
)

// GET /api/live — the header and right-pane live stream. One connection per
// browser tab carries coalesced "X changed" notices (never data); the client
// refetches through the normal read endpoints. Chat conversations keep their
// own per-session streams. See docs/design/live_update_feed.md.

const (
	liveFeedFlushInterval     = 250 * time.Millisecond
	liveFeedHeartbeatInterval = 15 * time.Second // Cloudflare drops idle streams at 100s
)

func (api *StreamingAPI) handleLiveFeed(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		log.Printf("[LIVE_FEED] could not disable write deadline: %v", err)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx := r.Context()
	claims := GetUserFromContext(ctx)
	access := newLiveFeedAccess(claims)

	sub := livefeed.Default.Subscribe()
	defer livefeed.Default.Unsubscribe(sub)

	// Subscribe before telling the client to resync, so nothing published
	// between its refetch and our first flush is lost.
	if err := writeSSEEvent(w, "resync", -1, struct{}{}); err != nil {
		return
	}
	flusher.Flush()

	flush := time.NewTicker(liveFeedFlushInterval)
	defer flush.Stop()
	heartbeat := time.NewTicker(liveFeedHeartbeatInterval)
	defer heartbeat.Stop()
	pending := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.Wake:
			pending = true
		case <-flush.C:
			if !pending {
				continue
			}
			pending = false
			notices, resync := sub.Drain()
			if resync {
				if err := writeSSEEvent(w, "resync", -1, struct{}{}); err != nil {
					return
				}
				flusher.Flush()
				continue
			}
			wrote := false
			for _, n := range notices {
				if n.Workflow != "" && !access.visible(ctx, n.Workflow) {
					continue
				}
				if err := writeSSEEvent(w, "change", -1, n); err != nil {
					return
				}
				wrote = true
			}
			if wrote {
				flusher.Flush()
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprintf(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// liveFeedAccess caches per-connection workflow visibility. The cache is
// dropped whenever a manifest changes (access blocks live in workflow.json).
type liveFeedAccess struct {
	claims *UserClaims
	mu     sync.Mutex
	gen    uint64
	seen   map[string]bool
}

func newLiveFeedAccess(claims *UserClaims) *liveFeedAccess {
	return &liveFeedAccess{claims: claims, seen: map[string]bool{}}
}

func (a *liveFeedAccess) visible(ctx context.Context, workflow string) bool {
	workflow = strings.Trim(strings.TrimSpace(workflow), "/")
	a.mu.Lock()
	defer a.mu.Unlock()
	if gen := manifestMutationGeneration.Load(); gen != a.gen {
		a.gen = gen
		a.seen = map[string]bool{}
	}
	if ok, cached := a.seen[workflow]; cached {
		return ok
	}
	var ok bool
	callerID := ""
	if a.claims != nil {
		callerID = a.claims.UserID
	}
	if ref, isCrew := resolveCrewPath(ctx, callerID, workflow); isCrew {
		ok = crewAccessFor(a.claims, ref) != crewAccessNone
	} else {
		level, _ := workflowAccessForWorkspacePath(ctx, a.claims, workflow)
		ok = level != WorkflowAccessNone
	}
	a.seen[workflow] = ok
	return ok
}

// publishWorkflowSettled tells open streams that a workflow RUN (a workflow
// execution or a scheduled session) reached a terminal state: its dashboard data, pending inputs and
// notifications may have changed (agents often write db.sqlite directly,
// where the server cannot see individual writes).
func publishWorkflowSettled(workspacePath string) {
	workspacePath = normalizeLiveFeedWorkflow(workspacePath)
	if workspacePath == "" {
		return
	}
	livefeed.Publish(livefeed.Report, workspacePath)
	livefeed.Publish(livefeed.HumanInputs, workspacePath)
	livefeed.Publish(livefeed.Notifications, workspacePath)
}

func normalizeLiveFeedWorkflow(p string) string { return livefeed.WorkflowRoot(p) }

func liveFeedTerminalStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "error", "failed", "stopped", "cancelled", "canceled": //nolint:misspell // both spellings occur
		return true
	}
	return false
}

// publishSessionsChanged tells the header activity monitor to refetch. Call it
// on session/execution status transitions only, never per event.
func publishSessionsChanged() { livefeed.Publish(livefeed.Sessions, "") }

func publishHumanInputsChanged(workspacePath string) {
	if wf := normalizeLiveFeedWorkflow(workspacePath); wf != "" {
		livefeed.Publish(livefeed.HumanInputs, wf)
	}
}

func publishReportChanged(workspacePath string) {
	if wf := normalizeLiveFeedWorkflow(workspacePath); wf != "" {
		livefeed.Publish(livefeed.Report, wf)
	}
}

// liveFeedReportPath reports whether a workspace file feeds a workflow's
// Report dashboard (its HTML/assets or the workflow database).
func liveFeedReportPath(p string) bool {
	clean := strings.Trim(strings.TrimSpace(p), "/")
	return strings.Contains(clean, "/db/reports/") || strings.HasSuffix(clean, "/db/db.sqlite")
}

// watchScheduleSummaryForLiveFeed publishes `schedules` only when the header's
// schedule summary actually changes. Summary reads reconcile run state, so
// publishing straight from noteScheduleSummaryChange could turn a refetch
// into another notice; comparing values cannot loop. Idle without streams.
func (api *StreamingAPI) watchScheduleSummaryForLiveFeed() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var (
		lastGen   uint64
		last      WorkflowScheduleSummary
		published bool
	)
	for range ticker.C {
		if api.scheduler == nil || livefeed.Default.Subscribers() == 0 {
			continue
		}
		gen := scheduleSummaryGeneration.Load()
		if published && gen == lastGen {
			continue
		}
		summary, err := api.scheduler.CachedWorkflowScheduleSummary(context.Background())
		if err != nil {
			continue
		}
		lastGen = scheduleSummaryGeneration.Load()
		if published && summary == last {
			continue
		}
		if published {
			livefeed.Publish(livefeed.Schedules, "")
		}
		last, published = summary, true
	}
}

// liveFeedScheduledSession mirrors the frontend's isScheduledSession: only
// these sessions are runs. Interactive chat turns never refresh the right pane.
func liveFeedScheduledSession(sessionID, triggeredBy string) bool {
	trigger := strings.ToLower(strings.TrimSpace(triggeredBy))
	id := strings.ToLower(sessionID)
	return strings.Contains(trigger, "schedule") || trigger == "cron" || trigger == "webhook" ||
		strings.HasPrefix(id, "schedule-") || strings.Contains(id, "-schedule-")
}

// liveFeedSettledRunPath returns the workflow to refresh when a tracked
// execution settles: only workflow runs, not Builder chat background work.
// Caller holds trackedWorkflowExecutionsMux.
func liveFeedSettledRunPath(exec *TrackedWorkflowExecution) string {
	if exec == nil || exec.Source != trackedExecutionSourceWorkflowRun {
		return ""
	}
	return exec.WorkspacePath
}
