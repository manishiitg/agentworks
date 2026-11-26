package codingagentjob

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreCreateGetAndUpdate(t *testing.T) {
	store := openTestStore(t)
	createdAt := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	job := Job{
		ID:             "job_store_test",
		Provider:       "codex-cli",
		Model:          "high",
		Task:           "review the change",
		WorkingDir:     t.TempDir(),
		TimeoutSeconds: 60,
		Status:         StatusQueued,
		Progress:       "queued",
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
		Version:        1,
	}
	if err := store.Create(context.Background(), job); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := store.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Task != job.Task || got.Status != StatusQueued || got.Version != 1 {
		t.Fatalf("Get() = %#v", got)
	}

	finishedAt := createdAt.Add(30 * time.Second)
	updated, err := store.Update(context.Background(), job.ID, func(current *Job) error {
		current.Status = StatusCompleted
		current.Result = "done"
		current.Usage = &Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}
		current.UpdatedAt = finishedAt
		current.FinishedAt = finishedAt
		return nil
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Status != StatusCompleted || updated.Result != "done" || updated.Version != 2 {
		t.Fatalf("Update() = %#v", updated)
	}
	if updated.Usage == nil || updated.Usage.TotalTokens != 15 {
		t.Fatalf("Update().Usage = %#v", updated.Usage)
	}

	reloaded, err := store.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get() after update error = %v", err)
	}
	if reloaded.Usage == nil || reloaded.Usage.TotalTokens != 15 || !reloaded.FinishedAt.Equal(finishedAt) {
		t.Fatalf("Get() after update = %#v", reloaded)
	}
}

func TestStoreMissingJob(t *testing.T) {
	store := openTestStore(t)
	_, err := store.Get(context.Background(), "job_missing")
	if !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("Get() error = %v, want ErrJobNotFound", err)
	}
}

func TestStoreFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("store mode = %o, want 600", got)
	}
}

func TestOpenStoreMigratesTmuxSessionColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy store: %v", err)
	}
	legacySchema := strings.Replace(storeSchema, "    tmux_session TEXT NOT NULL,\n", "", 1)
	if _, err := db.ExecContext(context.Background(), legacySchema); err != nil {
		_ = db.Close()
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy store: %v", err)
	}

	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore() migration error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	job := Job{
		ID:             "job_migrated_store",
		Provider:       "cursor-cli",
		Task:           "test migration",
		WorkingDir:     t.TempDir(),
		TimeoutSeconds: 30,
		Status:         StatusRunning,
		Progress:       "running",
		TmuxSession:    "cursor-session-1",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
		Version:        1,
	}
	if err := store.Create(context.Background(), job); err != nil {
		t.Fatalf("Create() after migration error = %v", err)
	}
	got, err := store.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get() after migration error = %v", err)
	}
	if got.TmuxSession != job.TmuxSession {
		t.Fatalf("tmux session = %q, want %q", got.TmuxSession, job.TmuxSession)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
