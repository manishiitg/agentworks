package codingagentjob

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeLauncher struct {
	pid   int
	jobID string
	depth int
	err   error
}

func (f *fakeLauncher) Launch(jobID string, depth int) (int, error) {
	f.jobID = jobID
	f.depth = depth
	return f.pid, f.err
}

func TestManagerStartsGetsAndCancelsJob(t *testing.T) {
	store := openTestStore(t)
	launcher := &fakeLauncher{pid: 4242}
	now := time.Date(2026, 7, 10, 11, 0, 0, 0, time.UTC)
	var signalledPID int
	manager, err := NewManager(store, launcher,
		WithClock(func() time.Time { return now }),
		WithSignalProcess(func(pid int) error {
			signalledPID = pid
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	workingDir := t.TempDir()
	view, err := manager.Start(context.Background(), StartRequest{
		Provider:   " CODEX-CLI ",
		Task:       "review the current implementation",
		WorkingDir: workingDir,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if view.JobID == "" || view.Status != StatusQueued || view.NextTool != "get_coding_agent_job" {
		t.Fatalf("Start() = %#v", view)
	}
	if launcher.jobID != view.JobID || launcher.depth != 1 {
		t.Fatalf("launcher = job %q depth %d", launcher.jobID, launcher.depth)
	}

	stored, err := store.Get(context.Background(), view.JobID)
	if err != nil {
		t.Fatalf("store.Get() error = %v", err)
	}
	if stored.WorkerPID != 4242 || stored.TimeoutSeconds != DefaultTimeoutSeconds {
		t.Fatalf("stored job = %#v", stored)
	}

	cancelled, err := manager.Cancel(context.Background(), view.JobID)
	if err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if cancelled.Status != StatusCancellationRequested || signalledPID != 4242 {
		t.Fatalf("Cancel() = %#v, signalled PID = %d", cancelled, signalledPID)
	}
}

func TestManagerRejectsNestedDelegationAndOutsideWorkspace(t *testing.T) {
	store := openTestStore(t)
	launcher := &fakeLauncher{pid: 1}
	root := t.TempDir()
	outside := t.TempDir()
	manager, err := NewManager(store, launcher,
		WithDelegationDepth(1, 1),
		WithWorkspaceRoots([]string{root}),
	)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	_, err = manager.Start(context.Background(), StartRequest{
		Provider:   "codex-cli",
		Task:       "test",
		WorkingDir: outside,
	})
	if err == nil {
		t.Fatal("Start() error = nil, want depth error")
	}

	manager, err = NewManager(store, launcher, WithWorkspaceRoots([]string{root}))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	_, err = manager.Start(context.Background(), StartRequest{
		Provider:   "codex-cli",
		Task:       "test",
		WorkingDir: outside,
	})
	if err == nil {
		t.Fatal("Start() error = nil, want workspace root error")
	}

	inside := filepath.Join(root, "project")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	_, err = manager.Start(context.Background(), StartRequest{
		Provider:   "not-a-real-provider",
		Task:       "test",
		WorkingDir: inside,
	})
	if err == nil {
		t.Fatal("Start() error = nil, want unsupported provider error")
	}
}

func TestManagerGetReconcilesDeadWorker(t *testing.T) {
	store := openTestStore(t)
	now := time.Date(2026, 7, 10, 13, 0, 0, 0, time.UTC)
	job := Job{
		ID:             "job_dead_worker",
		Provider:       "codex-cli",
		Task:           "test",
		WorkingDir:     t.TempDir(),
		TimeoutSeconds: 60,
		Status:         StatusRunning,
		WorkerPID:      99999,
		CreatedAt:      now.Add(-time.Minute),
		StartedAt:      now.Add(-time.Minute),
		UpdatedAt:      now.Add(-time.Minute),
		Version:        1,
	}
	if err := store.Create(context.Background(), job); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	manager, err := NewManager(store, &fakeLauncher{pid: 1},
		WithClock(func() time.Time { return now }),
		WithProcessAlive(func(int) bool { return false }),
		WithSignalProcess(func(int) error { return nil }),
	)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	view, err := manager.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if view.Status != StatusFailed || view.Error == "" {
		t.Fatalf("Get() = %#v", view)
	}
}

func TestManagerGetMarksExpiredWorkerTimedOut(t *testing.T) {
	store := openTestStore(t)
	now := time.Date(2026, 7, 10, 14, 0, 0, 0, time.UTC)
	job := Job{
		ID:             "job_expired_worker",
		Provider:       "cursor-cli",
		Task:           "test",
		WorkingDir:     t.TempDir(),
		TimeoutSeconds: 30,
		Status:         StatusRunning,
		WorkerPID:      88888,
		CreatedAt:      now.Add(-time.Minute),
		StartedAt:      now.Add(-time.Minute),
		UpdatedAt:      now.Add(-time.Minute),
		Version:        1,
	}
	if err := store.Create(context.Background(), job); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	var signalledPID int
	manager, err := NewManager(store, &fakeLauncher{pid: 1},
		WithClock(func() time.Time { return now }),
		WithProcessAlive(func(int) bool { return true }),
		WithSignalProcess(func(pid int) error {
			signalledPID = pid
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	view, err := manager.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if view.Status != StatusTimedOut || signalledPID != job.WorkerPID {
		t.Fatalf("Get() = %#v, signalled PID = %d", view, signalledPID)
	}
}
