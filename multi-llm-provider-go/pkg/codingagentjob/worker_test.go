package codingagentjob

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type runnerFunc func(context.Context, Job, func(ProgressUpdate)) (RunResult, error)

func (f runnerFunc) Run(ctx context.Context, job Job, progress func(ProgressUpdate)) (RunResult, error) {
	return f(ctx, job, progress)
}

func TestRunWorkerCompletesJob(t *testing.T) {
	store := openTestStore(t)
	job := createWorkerTestJob(t, store, "job_complete", 30)
	runner := runnerFunc(func(_ context.Context, got Job, progress func(ProgressUpdate)) (RunResult, error) {
		if got.ID != job.ID {
			t.Fatalf("runner job ID = %q, want %q", got.ID, job.ID)
		}
		progress(ProgressUpdate{Message: "running tests"})
		return RunResult{
			Content: "review complete",
			Model:   "gpt-test",
			Usage:   &Usage{InputTokens: 4, OutputTokens: 6, TotalTokens: 10},
		}, nil
	})
	if err := RunWorker(context.Background(), store, job.ID, runner); err != nil {
		t.Fatalf("RunWorker() error = %v", err)
	}
	completed, err := store.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if completed.Status != StatusCompleted || completed.Result != "review complete" || completed.Model != "gpt-test" {
		t.Fatalf("completed job = %#v", completed)
	}
	if completed.WorkerPID != 0 || completed.Usage == nil || completed.Usage.TotalTokens != 10 {
		t.Fatalf("completed job metadata = %#v", completed)
	}
}

func TestRunWorkerMarksTimeout(t *testing.T) {
	store := openTestStore(t)
	job := createWorkerTestJob(t, store, "job_timeout", 1)
	runner := runnerFunc(func(ctx context.Context, _ Job, _ func(ProgressUpdate)) (RunResult, error) {
		<-ctx.Done()
		return RunResult{}, ctx.Err()
	})
	if err := RunWorker(context.Background(), store, job.ID, runner); err != nil {
		t.Fatalf("RunWorker() error = %v", err)
	}
	timedOut, err := store.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if timedOut.Status != StatusTimedOut || timedOut.Error == "" {
		t.Fatalf("timed out job = %#v", timedOut)
	}
}

func TestRunWorkerMarksCancellation(t *testing.T) {
	store := openTestStore(t)
	job := createWorkerTestJob(t, store, "job_cancel", 30)
	started := make(chan struct{})
	runner := runnerFunc(func(ctx context.Context, _ Job, _ func(ProgressUpdate)) (RunResult, error) {
		close(started)
		<-ctx.Done()
		return RunResult{}, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunWorker(ctx, store, job.ID, runner) }()
	<-started
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunWorker() error = %v", err)
	}
	cancelled, err := store.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if cancelled.Status != StatusCancelled {
		t.Fatalf("cancelled job = %#v", cancelled)
	}
}

func TestRunWorkerDoesNotOverwriteTerminalState(t *testing.T) {
	store := openTestStore(t)
	job := createWorkerTestJob(t, store, "job_terminal_race", 30)
	runner := runnerFunc(func(_ context.Context, _ Job, _ func(ProgressUpdate)) (RunResult, error) {
		now := time.Now().UTC()
		_, err := store.Update(context.Background(), job.ID, func(current *Job) error {
			current.Status = StatusTimedOut
			current.Error = "timed out by polling reconciliation"
			current.UpdatedAt = now
			current.FinishedAt = now
			return nil
		})
		return RunResult{}, err
	})
	if err := RunWorker(context.Background(), store, job.ID, runner); err != nil {
		t.Fatalf("RunWorker() error = %v", err)
	}
	finished, err := store.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if finished.Status != StatusTimedOut || finished.Error != "timed out by polling reconciliation" {
		t.Fatalf("finished job = %#v", finished)
	}
}

func TestRunWorkerExposesTmuxCommandsOnlyWhileRunning(t *testing.T) {
	store := openTestStore(t)
	job := createWorkerTestJob(t, store, "job_tmux", 30)
	reported := make(chan struct{})
	release := make(chan struct{})
	runner := runnerFunc(func(_ context.Context, _ Job, progress func(ProgressUpdate)) (RunResult, error) {
		progress(ProgressUpdate{Message: "agent running", TmuxSession: "session with ' quote"})
		close(reported)
		<-release
		return RunResult{Content: "done"}, nil
	})
	done := make(chan error, 1)
	go func() { done <- RunWorker(context.Background(), store, job.ID, runner) }()
	<-reported

	running, err := store.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get() running job error = %v", err)
	}
	view := running.View(time.Now().UTC())
	if view.TmuxSession != "session with ' quote" {
		t.Fatalf("tmux session = %q", view.TmuxSession)
	}
	if view.TmuxAttach != `tmux attach-session -t 'session with '"'"' quote'` {
		t.Fatalf("attach command = %q", view.TmuxAttach)
	}
	if view.TmuxCapture != `tmux capture-pane -p -t 'session with '"'"' quote' -S -200` {
		t.Fatalf("capture command = %q", view.TmuxCapture)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("RunWorker() error = %v", err)
	}
	completed, err := store.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get() completed job error = %v", err)
	}
	completedView := completed.View(time.Now().UTC())
	if completedView.TmuxSession != "" || completedView.TmuxAttach != "" || completedView.TmuxCapture != "" {
		t.Fatalf("completed job exposes stale tmux metadata: %#v", completedView)
	}
}

func TestSanitizeProgressPreservesValidUTF8(t *testing.T) {
	input := "\x1b[31mworking\x1b[0m " + strings.Repeat("界", 300)
	got := sanitizeProgress(input)
	if !utf8.ValidString(got) {
		t.Fatalf("sanitizeProgress() returned invalid UTF-8")
	}
	if len(got) == 0 {
		t.Fatal("sanitizeProgress() returned empty output")
	}
}

func createWorkerTestJob(t *testing.T, store *Store, id string, timeout int) Job {
	t.Helper()
	now := time.Now().UTC()
	job := Job{
		ID:             id,
		Provider:       "codex-cli",
		Task:           "test",
		WorkingDir:     t.TempDir(),
		TimeoutSeconds: timeout,
		Status:         StatusQueued,
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        1,
	}
	if err := store.Create(context.Background(), job); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return job
}
