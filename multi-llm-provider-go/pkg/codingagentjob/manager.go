package codingagentjob

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
)

const (
	EnvStatePath        = "LLM_PROVIDER_MCP_STATE"
	EnvDelegationDepth  = "LLM_PROVIDER_MCP_DELEGATION_DEPTH"
	EnvWorkspaceRoots   = "LLM_PROVIDER_MCP_WORKSPACE_ROOTS"
	EnvAllowedProviders = "LLM_PROVIDER_MCP_ALLOWED_PROVIDERS"
)

type Launcher interface {
	Launch(jobID string, delegationDepth int) (int, error)
}

type SignalProcess func(pid int) error

type Manager struct {
	store            *Store
	launcher         Launcher
	signalProcess    SignalProcess
	processAlive     func(int) bool
	now              func() time.Time
	currentDepth     int
	maxDepth         int
	allowedProviders map[string]struct{}
	workspaceRoots   []string
}

type ManagerOption func(*Manager) error

func NewManager(store *Store, launcher Launcher, options ...ManagerOption) (*Manager, error) {
	if store == nil {
		return nil, fmt.Errorf("job store is required")
	}
	if launcher == nil {
		return nil, fmt.Errorf("job launcher is required")
	}
	manager := &Manager{
		store:         store,
		launcher:      launcher,
		signalProcess: signalInterrupt,
		processAlive:  isProcessAlive,
		now:           func() time.Time { return time.Now().UTC() },
		maxDepth:      1,
	}
	for _, option := range options {
		if err := option(manager); err != nil {
			return nil, err
		}
	}
	return manager, nil
}

func WithClock(now func() time.Time) ManagerOption {
	return func(manager *Manager) error {
		if now == nil {
			return fmt.Errorf("manager clock is required")
		}
		manager.now = now
		return nil
	}
}

func WithSignalProcess(signal SignalProcess) ManagerOption {
	return func(manager *Manager) error {
		if signal == nil {
			return fmt.Errorf("process signal function is required")
		}
		manager.signalProcess = signal
		return nil
	}
}

func WithProcessAlive(check func(int) bool) ManagerOption {
	return func(manager *Manager) error {
		if check == nil {
			return fmt.Errorf("process liveness function is required")
		}
		manager.processAlive = check
		return nil
	}
}

func WithDelegationDepth(current, maximum int) ManagerOption {
	return func(manager *Manager) error {
		if current < 0 || maximum < 1 {
			return fmt.Errorf("delegation depth must be non-negative with a positive maximum")
		}
		manager.currentDepth = current
		manager.maxDepth = maximum
		return nil
	}
}

func WithAllowedProviders(providers []string) ManagerOption {
	return func(manager *Manager) error {
		if len(providers) == 0 {
			manager.allowedProviders = nil
			return nil
		}
		manager.allowedProviders = make(map[string]struct{}, len(providers))
		for _, provider := range providers {
			normalized := normalizeProvider(provider)
			if normalized != "" {
				manager.allowedProviders[normalized] = struct{}{}
			}
		}
		if len(manager.allowedProviders) == 0 {
			return fmt.Errorf("allowed provider list contains no valid providers")
		}
		return nil
	}
}

func WithWorkspaceRoots(roots []string) ManagerOption {
	return func(manager *Manager) error {
		normalized := make([]string, 0, len(roots))
		for _, root := range roots {
			if strings.TrimSpace(root) == "" {
				continue
			}
			resolved, err := resolveDirectory(root)
			if err != nil {
				return fmt.Errorf("resolve workspace root %q: %w", root, err)
			}
			normalized = append(normalized, resolved)
		}
		manager.workspaceRoots = normalized
		return nil
	}
}

func (m *Manager) Start(ctx context.Context, request StartRequest) (View, error) {
	if m.currentDepth >= m.maxDepth {
		return View{}, fmt.Errorf("coding-agent delegation depth limit reached (%d)", m.maxDepth)
	}
	normalized, err := m.validateStartRequest(request)
	if err != nil {
		return View{}, err
	}
	jobID, err := newJobID()
	if err != nil {
		return View{}, err
	}
	now := m.now()
	job := Job{
		ID:             jobID,
		Provider:       normalized.Provider,
		Model:          normalized.Model,
		Task:           normalized.Task,
		WorkingDir:     normalized.WorkingDir,
		TimeoutSeconds: normalized.TimeoutSeconds,
		Status:         StatusQueued,
		Progress:       "Waiting for the coding-agent worker to start",
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        1,
	}
	if err := m.store.Create(ctx, job); err != nil {
		return View{}, err
	}

	pid, launchErr := m.launcher.Launch(job.ID, m.currentDepth+1)
	if launchErr != nil {
		failedAt := m.now()
		_, _ = m.store.Update(context.Background(), job.ID, func(current *Job) error {
			current.Status = StatusFailed
			current.Error = "failed to start coding-agent worker: " + launchErr.Error()
			current.Progress = "Worker launch failed"
			current.UpdatedAt = failedAt
			current.FinishedAt = failedAt
			return nil
		})
		return View{}, fmt.Errorf("start coding-agent worker for job %s: %w", job.ID, launchErr)
	}
	job, err = m.store.Update(ctx, job.ID, func(current *Job) error {
		if !current.Status.Terminal() {
			current.WorkerPID = pid
			current.UpdatedAt = m.now()
		}
		return nil
	})
	if err != nil {
		_ = m.signalProcess(pid)
		return View{}, err
	}
	return job.View(m.now()), nil
}

func (m *Manager) Get(ctx context.Context, jobID string) (View, error) {
	job, err := m.store.Get(ctx, strings.TrimSpace(jobID))
	if err != nil {
		return View{}, err
	}
	job, err = m.reconcile(ctx, job)
	if err != nil {
		return View{}, err
	}
	return job.View(m.now()), nil
}

func (m *Manager) Cancel(ctx context.Context, jobID string) (View, error) {
	jobID = strings.TrimSpace(jobID)
	job, err := m.store.Update(ctx, jobID, func(current *Job) error {
		if current.Status.Terminal() {
			return nil
		}
		current.Status = StatusCancellationRequested
		current.Progress = "Cancellation requested"
		current.UpdatedAt = m.now()
		return nil
	})
	if err != nil {
		return View{}, err
	}
	if !job.Status.Terminal() && job.WorkerPID > 0 {
		if err := m.signalProcess(job.WorkerPID); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return job.View(m.now()), fmt.Errorf("signal coding-agent worker for job %s: %w", jobID, err)
		}
	}
	return job.View(m.now()), nil
}

func (m *Manager) Providers() []llmproviders.CodingAgentProviderContract {
	contracts := llmproviders.CodingAgentProviderContracts()
	result := make([]llmproviders.CodingAgentProviderContract, 0, len(contracts))
	for _, contract := range contracts {
		provider := normalizeProvider(string(contract.Provider))
		if !m.providerAllowed(provider) {
			continue
		}
		result = append(result, contract)
	}
	return result
}

func (m *Manager) reconcile(ctx context.Context, job Job) (Job, error) {
	if job.Status.Terminal() {
		return job, nil
	}
	now := m.now()
	if !job.StartedAt.IsZero() && now.After(job.StartedAt.Add(time.Duration(job.TimeoutSeconds)*time.Second)) {
		updated, err := m.store.Update(ctx, job.ID, func(current *Job) error {
			if current.Status.Terminal() {
				return nil
			}
			current.Status = StatusTimedOut
			current.Progress = "Coding-agent job timed out"
			current.Error = fmt.Sprintf("job exceeded timeout of %d seconds", current.TimeoutSeconds)
			current.WorkerPID = 0
			current.UpdatedAt = now
			current.FinishedAt = now
			return nil
		})
		if err != nil {
			return Job{}, err
		}
		if updated.Status == StatusTimedOut && job.WorkerPID > 0 {
			_ = m.signalProcess(job.WorkerPID)
		}
		return updated, nil
	}
	if job.WorkerPID <= 0 || m.processAlive(job.WorkerPID) {
		return job, nil
	}
	return m.store.Update(ctx, job.ID, func(current *Job) error {
		if current.Status.Terminal() || current.WorkerPID != job.WorkerPID {
			return nil
		}
		if current.Status == StatusCancellationRequested {
			current.Status = StatusCancelled
			current.Progress = "Coding-agent job cancelled"
		} else {
			current.Status = StatusFailed
			current.Progress = "Coding-agent worker exited unexpectedly"
			current.Error = "coding-agent worker exited before recording a final result"
		}
		current.WorkerPID = 0
		current.UpdatedAt = now
		current.FinishedAt = now
		return nil
	})
}

func (m *Manager) validateStartRequest(request StartRequest) (StartRequest, error) {
	request.Provider = normalizeProvider(request.Provider)
	request.Model = strings.TrimSpace(request.Model)
	request.Task = strings.TrimSpace(request.Task)
	if request.Provider == "" {
		return StartRequest{}, fmt.Errorf("provider is required")
	}
	if request.Task == "" {
		return StartRequest{}, fmt.Errorf("task is required")
	}
	if len(request.Task) > MaxTaskBytes {
		return StartRequest{}, fmt.Errorf("task exceeds maximum size of %d bytes", MaxTaskBytes)
	}
	_, ok := llmproviders.GetCodingAgentProviderContract(llmproviders.Provider(request.Provider), request.Model)
	if !ok {
		return StartRequest{}, fmt.Errorf("provider %q is not a supported coding-agent provider", request.Provider)
	}
	if !m.providerAllowed(request.Provider) {
		return StartRequest{}, fmt.Errorf("provider %q is not allowed by this MCP server", request.Provider)
	}
	workingDir, err := resolveDirectory(request.WorkingDir)
	if err != nil {
		return StartRequest{}, fmt.Errorf("working_dir: %w", err)
	}
	if !withinAnyRoot(workingDir, m.workspaceRoots) {
		return StartRequest{}, fmt.Errorf("working_dir %q is outside the configured workspace roots", workingDir)
	}
	request.WorkingDir = workingDir
	if request.TimeoutSeconds == 0 {
		request.TimeoutSeconds = DefaultTimeoutSeconds
	}
	if request.TimeoutSeconds < 1 || request.TimeoutSeconds > MaxTimeoutSeconds {
		return StartRequest{}, fmt.Errorf("timeout_seconds must be between 1 and %d", MaxTimeoutSeconds)
	}
	return request, nil
}

func (m *Manager) providerAllowed(provider string) bool {
	if len(m.allowedProviders) == 0 {
		return true
	}
	_, ok := m.allowedProviders[provider]
	return ok
}

func DefaultStatePath() (string, error) {
	if configured := strings.TrimSpace(os.Getenv(EnvStatePath)); configured != "" {
		return filepath.Abs(configured)
	}
	base := strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory for MCP state: %w", err)
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "llm-provider-mcp", "jobs.db"), nil
}

func DelegationDepthFromEnvironment() int {
	depth, err := strconv.Atoi(strings.TrimSpace(os.Getenv(EnvDelegationDepth)))
	if err != nil || depth < 0 {
		return 0
	}
	return depth
}

func SplitEnvironmentList(name string) []string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	separator := ","
	if name == EnvWorkspaceRoots {
		separator = string(os.PathListSeparator)
	}
	parts := strings.Split(raw, separator)
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func normalizeProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

func resolveDirectory(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("directory is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", resolved)
	}
	return filepath.Clean(resolved), nil
}

func withinAnyRoot(path string, roots []string) bool {
	if len(roots) == 0 {
		return true
	}
	for _, root := range roots {
		relative, err := filepath.Rel(root, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func newJobID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate coding-agent job id: %w", err)
	}
	return "job_" + hex.EncodeToString(random), nil
}

func signalInterrupt(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(os.Interrupt)
}
