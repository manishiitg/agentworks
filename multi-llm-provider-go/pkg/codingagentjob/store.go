package codingagentjob

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const storeSchema = `
CREATE TABLE IF NOT EXISTS coding_agent_jobs (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    task TEXT NOT NULL,
    working_dir TEXT NOT NULL,
    timeout_seconds INTEGER NOT NULL,
    status TEXT NOT NULL,
    progress TEXT NOT NULL,
    tmux_session TEXT NOT NULL,
    result TEXT NOT NULL,
    error TEXT NOT NULL,
    worker_pid INTEGER NOT NULL,
    has_usage INTEGER NOT NULL,
    input_tokens INTEGER NOT NULL,
    output_tokens INTEGER NOT NULL,
    total_tokens INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    started_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    finished_at INTEGER NOT NULL,
    version INTEGER NOT NULL
);`

type Store struct {
	db   *sql.DB
	path string
}

func OpenStore(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("job store path is required")
	}
	if path != ":memory:" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve job store path: %w", err)
		}
		path = absolute
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create job store directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open job store: %w", err)
	}
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, statement := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		storeSchema,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("initialize job store: %w", err)
		}
	}
	if err := ensureStoreColumn(ctx, db, "coding_agent_jobs", "tmux_session", "TEXT NOT NULL DEFAULT ''"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate job store: %w", err)
	}
	if path != ":memory:" {
		for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
			if err := os.Chmod(candidate, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
				_ = db.Close()
				return nil, fmt.Errorf("secure job store file: %w", err)
			}
		}
	}
	return &Store{db: db, path: path}, nil
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Create(ctx context.Context, job Job) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("job store is not initialized")
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO coding_agent_jobs (
    id, provider, model, task, working_dir, timeout_seconds, status,
    progress, tmux_session, result, error, worker_pid, has_usage, input_tokens,
    output_tokens, total_tokens, created_at, started_at, updated_at,
    finished_at, version
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, jobValues(job)...)
	if err != nil {
		return fmt.Errorf("create coding-agent job: %w", err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, id string) (Job, error) {
	if s == nil || s.db == nil {
		return Job{}, fmt.Errorf("job store is not initialized")
	}
	row := s.db.QueryRowContext(ctx, `
SELECT id, provider, model, task, working_dir, timeout_seconds, status,
       progress, tmux_session, result, error, worker_pid, has_usage, input_tokens,
       output_tokens, total_tokens, created_at, started_at, updated_at,
       finished_at, version
FROM coding_agent_jobs WHERE id = ?`, id)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrJobNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("read coding-agent job: %w", err)
	}
	return job, nil
}

func (s *Store) Update(ctx context.Context, id string, mutate func(*Job) error) (Job, error) {
	if mutate == nil {
		return Job{}, fmt.Errorf("job mutation is required")
	}
	for attempt := 0; attempt < 8; attempt++ {
		job, err := s.Get(ctx, id)
		if err != nil {
			return Job{}, err
		}
		previousVersion := job.Version
		if err := mutate(&job); err != nil {
			return Job{}, err
		}
		job.Version = previousVersion + 1
		values := jobValues(job)
		values = append(values, id, previousVersion)
		result, err := s.db.ExecContext(ctx, `
UPDATE coding_agent_jobs SET
    id = ?, provider = ?, model = ?, task = ?, working_dir = ?,
    timeout_seconds = ?, status = ?, progress = ?, tmux_session = ?, result = ?, error = ?,
    worker_pid = ?, has_usage = ?, input_tokens = ?, output_tokens = ?,
    total_tokens = ?, created_at = ?, started_at = ?, updated_at = ?,
    finished_at = ?, version = ?
WHERE id = ? AND version = ?`, values...)
		if err != nil {
			return Job{}, fmt.Errorf("update coding-agent job: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return Job{}, fmt.Errorf("check coding-agent job update: %w", err)
		}
		if rows == 1 {
			return job, nil
		}
	}
	return Job{}, fmt.Errorf("update coding-agent job %s: concurrent update limit exceeded", id)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (Job, error) {
	var job Job
	var status string
	var hasUsage int
	var inputTokens, outputTokens, totalTokens int
	var createdAt, startedAt, updatedAt, finishedAt int64
	err := row.Scan(
		&job.ID, &job.Provider, &job.Model, &job.Task, &job.WorkingDir,
		&job.TimeoutSeconds, &status, &job.Progress, &job.TmuxSession, &job.Result, &job.Error,
		&job.WorkerPID, &hasUsage, &inputTokens, &outputTokens, &totalTokens,
		&createdAt, &startedAt, &updatedAt, &finishedAt, &job.Version,
	)
	if err != nil {
		return Job{}, err
	}
	job.Status = Status(status)
	job.CreatedAt = timeFromDatabase(createdAt)
	job.StartedAt = timeFromDatabase(startedAt)
	job.UpdatedAt = timeFromDatabase(updatedAt)
	job.FinishedAt = timeFromDatabase(finishedAt)
	if hasUsage != 0 {
		job.Usage = &Usage{InputTokens: inputTokens, OutputTokens: outputTokens, TotalTokens: totalTokens}
	}
	return job, nil
}

func jobValues(job Job) []any {
	hasUsage := 0
	usage := Usage{}
	if job.Usage != nil {
		hasUsage = 1
		usage = *job.Usage
	}
	return []any{
		job.ID, job.Provider, job.Model, job.Task, job.WorkingDir,
		job.TimeoutSeconds, string(job.Status), job.Progress, job.TmuxSession, job.Result,
		job.Error, job.WorkerPID, hasUsage, usage.InputTokens,
		usage.OutputTokens, usage.TotalTokens, timeToDatabase(job.CreatedAt),
		timeToDatabase(job.StartedAt), timeToDatabase(job.UpdatedAt),
		timeToDatabase(job.FinishedAt), job.Version,
	}
}

func ensureStoreColumn(ctx context.Context, db *sql.DB, table, column, declaration string) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = db.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+column+" "+declaration)
	return err
}

func timeToDatabase(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UnixNano()
}

func timeFromDatabase(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}
