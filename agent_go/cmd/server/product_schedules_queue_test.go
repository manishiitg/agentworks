package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

func testAutomationJob(userID, profileID, projectID, scheduleID string, isolated bool) productScheduleJob {
	return productScheduleJob{
		UserID:    userID,
		Profile:   agentprofiles.Profile{ID: profileID},
		ProjectID: projectID,
		Schedule:  productschedule.Schedule{ID: scheduleID, Isolated: isolated},
	}
}

func TestConversationKeyForJob(t *testing.T) {
	mainA := testAutomationJob("u1", "work", "proj", "trigger-a", false)
	mainB := testAutomationJob("u1", "work", "proj", "trigger-b", false)
	if conversationKeyForJob(mainA) != conversationKeyForJob(mainB) {
		t.Fatal("main-chat jobs of one project must share one conversation key")
	}
	isoA := testAutomationJob("u1", "work", "proj", "trigger-a", true)
	isoB := testAutomationJob("u1", "work", "proj", "trigger-b", true)
	if conversationKeyForJob(isoA) == conversationKeyForJob(isoB) {
		t.Fatal("isolated jobs must have distinct conversation keys")
	}
	if got := conversationKeyForJob(isoA); got != isoA.ID() {
		t.Fatalf("isolated key = %q, want job id %q", got, isoA.ID())
	}
	otherProject := testAutomationJob("u1", "work", "other", "trigger-a", false)
	if conversationKeyForJob(mainA) == conversationKeyForJob(otherProject) {
		t.Fatal("main-chat jobs of different projects must not share a key")
	}
}

func TestClaimAutomationRunSerializesMainChat(t *testing.T) {
	svc := &ProjectActionDispatcher{}
	ctx := context.Background()
	first := testAutomationJob("u1", "work", "proj", "trigger-a", false)
	second := testAutomationJob("u1", "work", "proj", "trigger-b", false)

	if _, err := svc.claimAutomationRun(ctx, first, "webhook", time.Time{}, productScheduleRunOptions{}, nil); err != nil {
		t.Fatalf("first claim err = %v", err)
	}
	if _, err := svc.claimAutomationRun(ctx, second, "webhook", time.Time{}, productScheduleRunOptions{}, nil); !errors.Is(err, productschedule.ErrAlreadyRunning) {
		t.Fatalf("second claim err = %v, want ErrAlreadyRunning", err)
	}
	if _, err := svc.claimAutomationRun(ctx, second, "webhook", time.Time{}, productScheduleRunOptions{AllowQueue: true}, nil); !errors.Is(err, ErrProductRunQueued) {
		t.Fatalf("queued claim err = %v, want ErrProductRunQueued", err)
	}
	convKey := "u1\x1f" + conversationKeyForJob(first)
	svc.mu.Lock()
	queued := len(svc.queued[convKey])
	svc.mu.Unlock()
	if queued != 1 {
		t.Fatalf("queued depth = %d, want 1", queued)
	}
}

func TestClaimAutomationRunKeepsIsolatedJobsIndependent(t *testing.T) {
	svc := &ProjectActionDispatcher{}
	ctx := context.Background()
	first := testAutomationJob("u1", "work", "proj", "trigger-a", true)
	second := testAutomationJob("u1", "work", "proj", "trigger-b", true)

	if _, err := svc.claimAutomationRun(ctx, first, "webhook", time.Time{}, productScheduleRunOptions{}, nil); err != nil {
		t.Fatalf("first claim err = %v", err)
	}
	if _, err := svc.claimAutomationRun(ctx, second, "webhook", time.Time{}, productScheduleRunOptions{}, nil); err != nil {
		t.Fatalf("isolated jobs must claim independently, err = %v", err)
	}
}

func TestClaimAutomationRunRejectsFullQueue(t *testing.T) {
	svc := &ProjectActionDispatcher{}
	ctx := context.Background()
	job := testAutomationJob("u1", "work", "proj", "trigger-a", false)
	job.AutomationKind = "webhook" // Exercise the actual queued webhook lane, not scheduled live-input routing.
	convKey := "u1\x1f" + conversationKeyForJob(job)

	svc.conversations = map[string]bool{convKey: true}
	svc.queued = map[string][]productScheduleQueuedRun{convKey: make([]productScheduleQueuedRun, maxProductConversationQueue)}
	if _, err := svc.claimAutomationRun(ctx, job, "webhook", time.Time{}, productScheduleRunOptions{AllowQueue: true}, nil); !errors.Is(err, ErrProductQueueFull) {
		t.Fatalf("full queue err = %v, want ErrProductQueueFull", err)
	}
	svc.queued[convKey] = svc.queued[convKey][:maxProductConversationQueue-1]
	if _, err := svc.claimAutomationRun(ctx, job, "webhook", time.Time{}, productScheduleRunOptions{AllowQueue: true}, nil); !errors.Is(err, ErrProductRunQueued) {
		t.Fatalf("available capacity refused: %v", err)
	}
}

func TestPopProductQueueLockedIsFIFO(t *testing.T) {
	convKey := "u1\x1fconv"
	queued := map[string][]productScheduleQueuedRun{
		convKey: {
			{triggerSource: "first"},
			{triggerSource: "second"},
		},
	}
	head, ok := popProductQueueLocked(queued, convKey)
	if !ok || head.triggerSource != "first" {
		t.Fatalf("head = %+v ok=%v", head, ok)
	}
	head, ok = popProductQueueLocked(queued, convKey)
	if !ok || head.triggerSource != "second" {
		t.Fatalf("head = %+v ok=%v", head, ok)
	}
	if _, ok := popProductQueueLocked(queued, convKey); ok {
		t.Fatal("empty queue must report not-ok")
	}
	if _, exists := queued[convKey]; exists {
		t.Fatal("drained queue key must be removed")
	}
}

// PLAT-871-15: replacing a report must not erase a different waiting job or
// make the report's later occurrence overtake an older queued sync.
func TestTimedQueueReplacesOnlySameScheduleAndRunsOldestDue(t *testing.T) {
	svc := &ProjectActionDispatcher{}
	a := testAutomationJob("owner", "work", "example", "report", false)
	b := testAutomationJob("owner", "work", "example", "sync", false)
	key := "owner\x1f" + conversationKeyForJob(a)
	svc.conversations = map[string]bool{key: true}
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	for _, item := range []struct {
		job productScheduleJob
		at  time.Time
	}{{a, base}, {b, base.Add(5 * time.Minute)}, {a, base.Add(10 * time.Minute)}} {
		if _, err := svc.claimAutomationRun(context.Background(), item.job, "cron", item.at, productScheduleRunOptions{AllowQueue: true}, nil); !errors.Is(err, ErrProductRunQueued) {
			t.Fatalf("timed action was not queued: %v", err)
		}
	}
	if len(svc.queued[key]) != 2 {
		t.Fatalf("different job lost or same schedule piled up: %+v", svc.queued[key])
	}
	first, ok := popProductQueueLocked(svc.queued, key)
	if !ok || first.job.Schedule.ID != "sync" {
		t.Fatalf("newer occurrence overtook older job: %+v", first)
	}
	last, ok := popProductQueueLocked(svc.queued, key)
	if !ok || last.job.Schedule.ID != "report" || !last.scheduledFor.Equal(base.Add(10*time.Minute)) {
		t.Fatalf("wrong latest report: %+v", last)
	}
}

func TestFinishAutomationRunDrainsQueueAndReleases(t *testing.T) {
	svc := &ProjectActionDispatcher{
		running:       map[string]*productScheduleRun{},
		conversations: map[string]bool{},
		queued:        map[string][]productScheduleQueuedRun{},
	}
	job := testAutomationJob("u1", "work", "proj", "trigger-a", false)
	jobKey := "u1\x1f" + job.ID()
	convKey := "u1\x1f" + conversationKeyForJob(job)
	svc.running[jobKey] = &productScheduleRun{StartedAt: time.Now().UTC(), cancel: func() {}}
	svc.conversations[convKey] = true
	// Zero-value jobs fail resolve immediately, so the handed-off turn
	// settles without touching the network.
	svc.queued[convKey] = []productScheduleQueuedRun{{job: productScheduleJob{UserID: "u1"}, triggerSource: "webhook"}}

	svc.finishAutomationRun(jobKey, convKey)

	svc.mu.Lock()
	queued := len(svc.queued[convKey])
	svc.mu.Unlock()
	if queued != 0 {
		t.Fatalf("queued depth = %d, want 0 (head must hand off synchronously)", queued)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		svc.mu.Lock()
		held := svc.conversations[convKey]
		live := len(svc.running)
		svc.mu.Unlock()
		if !held && live == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("conversation still held (running=%d) after handoff settled", live)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
