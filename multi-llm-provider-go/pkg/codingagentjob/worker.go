package codingagentjob

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

type Runner interface {
	Run(ctx context.Context, job Job, progress func(ProgressUpdate)) (RunResult, error)
}

func RunWorker(ctx context.Context, store *Store, jobID string, runner Runner) error {
	if store == nil {
		return fmt.Errorf("job store is required")
	}
	if runner == nil {
		return fmt.Errorf("coding-agent runner is required")
	}
	job, err := store.Get(ctx, jobID)
	if err != nil {
		return err
	}
	if job.Status.Terminal() {
		return nil
	}
	if job.Status == StatusCancellationRequested {
		now := time.Now().UTC()
		_, err := store.Update(ctx, job.ID, func(current *Job) error {
			current.Status = StatusCancelled
			current.Progress = "Cancelled before the worker started"
			current.UpdatedAt = now
			current.FinishedAt = now
			return nil
		})
		return err
	}

	startedAt := time.Now().UTC()
	job, err = store.Update(ctx, job.ID, func(current *Job) error {
		if current.Status == StatusCancellationRequested {
			return nil
		}
		current.Status = StatusRunning
		current.Progress = "Starting " + current.Provider
		current.WorkerPID = os.Getpid()
		current.StartedAt = startedAt
		current.UpdatedAt = startedAt
		return nil
	})
	if err != nil {
		return err
	}
	if job.Status == StatusCancellationRequested {
		return RunWorker(ctx, store, jobID, runner)
	}

	timeout := time.Duration(job.TimeoutSeconds) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var progressMu sync.Mutex
	lastProgress := time.Time{}
	lastMessage := ""
	lastTmuxSession := ""
	recordProgress := func(update ProgressUpdate) {
		message := sanitizeProgress(update.Message)
		tmuxSession := sanitizeTmuxSession(update.TmuxSession)
		if message == "" && tmuxSession == "" {
			return
		}
		progressMu.Lock()
		defer progressMu.Unlock()
		now := time.Now().UTC()
		tmuxChanged := tmuxSession != "" && tmuxSession != lastTmuxSession
		messageChanged := message != "" && (message != lastMessage || now.Sub(lastProgress) >= 10*time.Second)
		if !tmuxChanged && !messageChanged {
			return
		}
		if !tmuxChanged && !lastProgress.IsZero() && now.Sub(lastProgress) < 2*time.Second {
			return
		}
		if messageChanged {
			lastProgress = now
			lastMessage = message
		}
		if tmuxChanged {
			lastTmuxSession = tmuxSession
		}
		_, _ = store.Update(context.Background(), job.ID, func(current *Job) error {
			if current.Status != StatusRunning {
				return nil
			}
			if messageChanged {
				current.Progress = message
			}
			if tmuxChanged {
				current.TmuxSession = tmuxSession
			}
			current.UpdatedAt = now
			return nil
		})
	}

	result, runErr := runner.Run(runCtx, job, recordProgress)
	finishedAt := time.Now().UTC()
	finalJob, updateErr := store.Update(context.Background(), job.ID, func(current *Job) error {
		if current.Status.Terminal() {
			return nil
		}
		current.UpdatedAt = finishedAt
		current.FinishedAt = finishedAt
		current.WorkerPID = 0
		current.TmuxSession = ""
		switch {
		case current.Status == StatusCancellationRequested || errors.Is(ctx.Err(), context.Canceled):
			current.Status = StatusCancelled
			current.Progress = "Coding-agent job cancelled"
			current.Error = ""
		case errors.Is(runCtx.Err(), context.DeadlineExceeded):
			current.Status = StatusTimedOut
			current.Progress = "Coding-agent job timed out"
			current.Error = fmt.Sprintf("job exceeded timeout of %d seconds", current.TimeoutSeconds)
		case runErr != nil:
			current.Status = StatusFailed
			current.Progress = "Coding-agent job failed"
			current.Error = runErr.Error()
		default:
			current.Status = StatusCompleted
			current.Progress = "Coding-agent job completed"
			current.Result = result.Content
			current.Usage = result.Usage
			if result.Model != "" {
				current.Model = result.Model
			}
		}
		return nil
	})
	if updateErr != nil {
		return updateErr
	}
	if finalJob.Status == StatusFailed {
		return runErr
	}
	return nil
}

func sanitizeTmuxSession(session string) string {
	session = strings.TrimSpace(session)
	if session == "" || len(session) > 256 {
		return ""
	}
	if strings.IndexFunc(session, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return ""
	}
	return session
}
