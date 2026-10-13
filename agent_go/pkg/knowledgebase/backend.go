package knowledgebase

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/brainapi"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
)

// Service is the stable host-facing API. Both implementations use this lifecycle
// guard; the built-in domain remains available during the migration period.
type Service struct {
	mu           sync.Mutex
	cond         *sync.Cond
	active       int
	closing      bool
	engine       brainapi.Engine
	lease        *os.File
	closed       bool
	closeErr     error
	backend      string
	shutdown     context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	closeTimeout time.Duration
}

func New(cfg Config) (*Service, error) { return NewPrepared(cfg, nil) }

// NewPrepared runs host-owned, idempotent filesystem preparation while holding
// the same writer lease as the selected engine. Private code never gets this callback.
func NewPrepared(cfg Config, prepare func() error) (*Service, error) {
	mode := cfg.Backend
	if mode == "" {
		mode = os.Getenv("AGENTWORKS_BRAIN_BACKEND")
	}
	return newServicePrepared(cfg, mode, privateBackendFactory(), prepare)
}
func newService(cfg Config, mode string, private *brainapi.Factory) (*Service, error) {
	return newServicePrepared(cfg, mode, private, nil)
}
func newServicePrepared(cfg Config, mode string, private *brainapi.Factory, prepare func() error) (*Service, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "builtin"
	}
	open := func(c Config) (brainapi.Engine, error) { return newBuiltin(c) }
	switch mode {
	case "builtin":
	case "private":
		if private == nil || private.Version != brainapi.Version || private.Open == nil {
			return nil, fmt.Errorf("private Brain backend is unavailable or incompatible; use a composed agent-server")
		}
		expected, e1 := json.Marshal(ToolDefinitions())
		actual, e2 := json.Marshal(private.Catalog)
		if e1 != nil || e2 != nil || !reflect.DeepEqual(expected, actual) {
			return nil, fmt.Errorf("private Brain tool contract differs from the host")
		}
		open = private.Open
	default:
		return nil, fmt.Errorf("unknown Brain backend %q", mode)
	}
	lease, err := acquireEngineLease(cfg.Root)
	if err != nil {
		return nil, err
	}
	if prepare != nil {
		if err := prepare(); err != nil {
			lease.Close()
			return nil, err
		}
	}
	engine, err := open(cfg)
	if err != nil {
		lease.Close()
		return nil, err
	}
	if engine == nil || reflect.ValueOf(engine).Kind() == reflect.Pointer && reflect.ValueOf(engine).IsNil() {
		lease.Close()
		return nil, fmt.Errorf("Brain factory returned no engine")
	}
	service := &Service{engine: engine, lease: lease, backend: mode}
	service.cond = sync.NewCond(&service.mu)
	service.shutdown, service.cancel = context.WithCancel(context.Background())
	service.done = make(chan struct{})
	service.closeTimeout = 45 * time.Second
	return service, nil
}

func acquireEngineLease(root string) (*os.File, error) {
	if root == "" {
		return nil, fmt.Errorf("knowledgebase root is required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	private := filepath.Join(root, "private")
	if err := os.MkdirAll(private, 0700); err != nil {
		return nil, err
	}
	for _, p := range []string{root, private} {
		if err := safeRegularDir(p); err != nil {
			return nil, err
		}
	}
	fd, err := unix.Open(filepath.Join(private, "engine.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "brain-engine-lock")
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, &Error{Code: "BACKEND_IN_USE", Message: "Another Brain engine owns this root; stop it before switching backends.", Retryable: true}
	}
	return f, nil
}

func closedError() error {
	return &Error{Code: "STORAGE_UNAVAILABLE", Message: "Brain engine is closed."}
}
func (s *Service) BackendName() string { return s.backend }
func (s *Service) begin() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	s.active++
	return true
}
func (s *Service) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
	if s.active == 0 {
		s.cond.Broadcast()
	}
}

// operationContext cancels provider/Git work when the host starts draining.
func (s *Service) operationContext(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(s.shutdown, cancel)
	return ctx, func() { stop(); cancel() }
}

// Close cancels in-flight contexts and bounds how long its caller waits. A timed
// out drain retains the writer lease; it never opens a second writer while work
// remains. Cleanup completes automatically after the final operation returns.
func (s *Service) Close() error {
	s.mu.Lock()
	if !s.closing {
		s.closing = true
		s.cancel()
		go s.finishClose()
	}
	done := s.done
	timeout := s.closeTimeout
	s.mu.Unlock()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		s.mu.Lock()
		err := s.closeErr
		s.mu.Unlock()
		return err
	case <-timer.C:
		return &Error{Code: "BACKEND_DRAINING", Message: "Brain is still draining; its writer lease remains held.", Retryable: true}
	}
}
func (s *Service) finishClose() {
	s.mu.Lock()
	for s.active > 0 {
		s.cond.Wait()
	}
	s.mu.Unlock()
	err := s.engine.Close()
	if leaseErr := s.lease.Close(); err == nil {
		err = leaseErr
	}
	s.mu.Lock()
	s.closed = true
	s.closeErr = err
	close(s.done)
	s.cond.Broadcast()
	s.mu.Unlock()
}

func (s *Service) AutoPush(ctx context.Context, quiet time.Duration, now time.Time) (bool, error) {
	if !s.begin() {
		return false, closedError()
	}
	defer s.end()
	ctx, finish := s.operationContext(ctx)
	defer finish()
	return s.engine.AutoPush(ctx, quiet, now)
}

func (s *Service) BackupConfigured() (bool, error) {
	if !s.begin() {
		return false, closedError()
	}
	defer s.end()
	return s.engine.BackupConfigured()
}

func (s *Service) Call(ctx context.Context, p Principal, tool string, args map[string]any) (any, error) {
	if !s.begin() {
		return nil, closedError()
	}
	defer s.end()
	ctx, finish := s.operationContext(ctx)
	defer finish()
	p.RequestTool, p.RequestArguments = "", nil
	return s.engine.Call(ctx, p, tool, args)
}

func (s *Service) CallTool(ctx context.Context, p Principal, tool string, args map[string]any) (any, error) {
	if !s.begin() {
		return nil, closedError()
	}
	defer s.end()
	ctx, finish := s.operationContext(ctx)
	defer finish()
	p.RequestTool, p.RequestArguments = "", nil
	return s.engine.CallTool(ctx, p, tool, args)
}

func (s *Service) CanEditWholeBrain(p Principal) bool {
	if !s.begin() {
		return false
	}
	defer s.end()
	return s.engine.CanEditWholeBrain(p)
}

func (s *Service) CanWriteSomewhere(p Principal) bool {
	if !s.begin() {
		return false
	}
	defer s.end()
	return s.engine.CanWriteSomewhere(p)
}

func (s *Service) EnsureDefaultFolder(ctx context.Context, admin Principal, name, role string, users []Identity) error {
	if !s.begin() {
		return closedError()
	}
	defer s.end()
	ctx, finish := s.operationContext(ctx)
	defer finish()
	return s.engine.EnsureDefaultFolder(ctx, admin, name, role, users)
}

func (s *Service) EnsureGitRepository(ctx context.Context) (GitFolder, error) {
	if !s.begin() {
		return GitFolder{}, closedError()
	}
	defer s.end()
	ctx, finish := s.operationContext(ctx)
	defer finish()
	return s.engine.EnsureGitRepository(ctx)
}

func (s *Service) GitFolder() (GitFolder, error) {
	if !s.begin() {
		return GitFolder{}, closedError()
	}
	defer s.end()
	return s.engine.GitFolder()
}

func (s *Service) GitToken(f GitFolder) string {
	if !s.begin() {
		return ""
	}
	defer s.end()
	return s.engine.GitToken(f)
}

func (s *Service) IdentityActive(ctx context.Context, id string) bool {
	if !s.begin() {
		return false
	}
	defer s.end()
	ctx, finish := s.operationContext(ctx)
	defer finish()
	return s.engine.IdentityActive(ctx, id)
}

func (s *Service) LastAutoPush() *AutoPushStatus {
	if !s.begin() {
		return nil
	}
	defer s.end()
	return s.engine.LastAutoPush()
}

func (s *Service) ReconcileBackup(ctx context.Context, p Principal) (any, error) {
	if !s.begin() {
		return nil, closedError()
	}
	defer s.end()
	ctx, finish := s.operationContext(ctx)
	defer finish()
	return s.engine.ReconcileBackup(ctx, p)
}

func (s *Service) RequireIdentityFolderRole(ctx context.Context, id, folderID, role string) error {
	if !s.begin() {
		return closedError()
	}
	defer s.end()
	ctx, finish := s.operationContext(ctx)
	defer finish()
	return s.engine.RequireIdentityFolderRole(ctx, id, folderID, role)
}

func (s *Service) ReserveIntegrationRequest(ctx context.Context, p Principal, tool string, args map[string]any) (string, error) {
	if !s.begin() {
		return "", closedError()
	}
	defer s.end()
	ctx, finish := s.operationContext(ctx)
	defer finish()
	p.RequestTool, p.RequestArguments = "", nil
	return s.engine.ReserveIntegrationRequest(ctx, p, tool, args)
}

func (s *Service) ResolveBinding(ctx context.Context, p Principal, b Binding, audience []string) (string, error) {
	if !s.begin() {
		return "", closedError()
	}
	defer s.end()
	ctx, finish := s.operationContext(ctx)
	defer finish()
	return s.engine.ResolveBinding(ctx, p, b, audience)
}

func (s *Service) RunGit(ctx context.Context, p Principal, a map[string]any, run func(context.Context, string) (any, error)) (any, error) {
	if !s.begin() {
		return nil, closedError()
	}
	defer s.end()
	ctx, finish := s.operationContext(ctx)
	defer finish()
	return s.engine.RunGit(ctx, p, a, run)
}

func (s *Service) SeenCommit(identity string) string {
	if !s.begin() {
		return ""
	}
	defer s.end()
	return s.engine.SeenCommit(identity)
}

func (s *Service) SetSeenCommit(identity, commit string) error {
	if !s.begin() {
		return closedError()
	}
	defer s.end()
	return s.engine.SetSeenCommit(identity, commit)
}

func (s *Service) SyncDisk(ctx context.Context, actor string) error {
	if !s.begin() {
		return closedError()
	}
	defer s.end()
	ctx, finish := s.operationContext(ctx)
	defer finish()
	return s.engine.SyncDisk(ctx, actor)
}

func (s *Service) SyncPlatformIdentities(ctx context.Context, users []Identity) error {
	if !s.begin() {
		return closedError()
	}
	defer s.end()
	ctx, finish := s.operationContext(ctx)
	defer finish()
	return s.engine.SyncPlatformIdentities(ctx, users)
}

func (s *Service) ValidateCaps(ctx context.Context, p Principal, id string, caps *[]Cap) error {
	if !s.begin() {
		return closedError()
	}
	defer s.end()
	ctx, finish := s.operationContext(ctx)
	defer finish()
	return s.engine.ValidateCaps(ctx, p, id, caps)
}

func (s *Service) ValidateServiceTokenIdentity(ctx context.Context, p Principal, id string) error {
	if !s.begin() {
		return closedError()
	}
	defer s.end()
	ctx, finish := s.operationContext(ctx)
	defer finish()
	return s.engine.ValidateServiceTokenIdentity(ctx, p, id)
}

func (s *Service) ValidateTokenIdentity(ctx context.Context, p Principal, id string) error {
	if !s.begin() {
		return closedError()
	}
	defer s.end()
	ctx, finish := s.operationContext(ctx)
	defer finish()
	return s.engine.ValidateTokenIdentity(ctx, p, id)
}
