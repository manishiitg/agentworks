package codingagentjob

import (
	"errors"
	"strings"
	"time"
)

const (
	DefaultTimeoutSeconds = 45 * 60
	MaxTimeoutSeconds     = 24 * 60 * 60
	DefaultPollSeconds    = 15
	MaxTaskBytes          = 100 * 1024
)

var ErrJobNotFound = errors.New("coding-agent job not found")

type Status string

const (
	StatusQueued                Status = "queued"
	StatusRunning               Status = "running"
	StatusCancellationRequested Status = "cancellation_requested"
	StatusCompleted             Status = "completed"
	StatusFailed                Status = "failed"
	StatusCancelled             Status = "cancelled"
	StatusTimedOut              Status = "timed_out"
)

func (s Status) Terminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCancelled, StatusTimedOut:
		return true
	default:
		return false
	}
}

type StartRequest struct {
	Provider       string `json:"provider"`
	Model          string `json:"model,omitempty"`
	Task           string `json:"task"`
	WorkingDir     string `json:"working_dir"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type ProgressUpdate struct {
	Message     string
	TmuxSession string
}

type Job struct {
	ID             string
	Provider       string
	Model          string
	Task           string
	WorkingDir     string
	TimeoutSeconds int
	Status         Status
	Progress       string
	TmuxSession    string
	Result         string
	Error          string
	WorkerPID      int
	Usage          *Usage
	CreatedAt      time.Time
	StartedAt      time.Time
	UpdatedAt      time.Time
	FinishedAt     time.Time
	Version        int64
}

type View struct {
	JobID               string     `json:"job_id"`
	Provider            string     `json:"provider"`
	Model               string     `json:"model,omitempty"`
	WorkingDir          string     `json:"working_dir"`
	Status              Status     `json:"status"`
	Progress            string     `json:"progress,omitempty"`
	TmuxSession         string     `json:"tmux_session,omitempty"`
	TmuxAttach          string     `json:"tmux_attach_command,omitempty"`
	TmuxCapture         string     `json:"tmux_capture_command,omitempty"`
	TerminalOutput      string     `json:"terminal_output,omitempty"`
	TerminalTruncated   bool       `json:"terminal_output_truncated,omitempty"`
	TerminalCapturedAt  *time.Time `json:"terminal_captured_at,omitempty"`
	TerminalOutputError string     `json:"terminal_output_error,omitempty"`
	Result              string     `json:"result,omitempty"`
	Error               string     `json:"error,omitempty"`
	Usage               *Usage     `json:"usage,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	UpdatedAt           time.Time  `json:"updated_at"`
	FinishedAt          *time.Time `json:"finished_at,omitempty"`
	ElapsedSeconds      int64      `json:"elapsed_seconds"`
	PollAfter           int        `json:"poll_after_seconds,omitempty"`
	NextTool            string     `json:"next_tool,omitempty"`
}

func (j Job) View(now time.Time) View {
	view := View{
		JobID:      j.ID,
		Provider:   j.Provider,
		Model:      j.Model,
		WorkingDir: j.WorkingDir,
		Status:     j.Status,
		Progress:   j.Progress,
		Result:     j.Result,
		Error:      j.Error,
		Usage:      j.Usage,
		CreatedAt:  j.CreatedAt,
		UpdatedAt:  j.UpdatedAt,
	}
	if !j.StartedAt.IsZero() {
		startedAt := j.StartedAt
		view.StartedAt = &startedAt
		end := now
		if !j.FinishedAt.IsZero() {
			end = j.FinishedAt
		}
		if end.After(startedAt) {
			view.ElapsedSeconds = int64(end.Sub(startedAt).Seconds())
		}
	}
	if !j.FinishedAt.IsZero() {
		finishedAt := j.FinishedAt
		view.FinishedAt = &finishedAt
	}
	if !j.Status.Terminal() {
		view.PollAfter = DefaultPollSeconds
		view.NextTool = "get_coding_agent_job"
		if j.TmuxSession != "" {
			view.TmuxSession = j.TmuxSession
			quotedSession := shellQuote(j.TmuxSession)
			view.TmuxAttach = "tmux attach-session -t " + quotedSession
			view.TmuxCapture = "tmux capture-pane -p -t " + quotedSession + " -S -200"
		}
	}
	return view
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

type RunResult struct {
	Content string
	Model   string
	Usage   *Usage
}
