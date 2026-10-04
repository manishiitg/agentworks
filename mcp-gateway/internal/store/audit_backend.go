package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AuditBackend stores immutable tool-call metadata independently of editable
// permission configuration. Append acknowledges persistence in durable mode,
// or admission to a bounded in-memory queue in async mode.
type AuditBackend interface {
	Append(context.Context, AuditEvent) error
	Query(context.Context, AuditFilter) ([]AuditEvent, error)
	Summary(context.Context, AuditFilter) (AuditSummary, error)
	Info() AuditInfo
	Close() error
}

type AuditInfo struct {
	Provider         string `json:"provider"`
	Enabled          bool   `json:"enabled"`
	RetentionSeconds int64  `json:"retention_seconds"`
	WriteMode        string `json:"write_mode,omitempty"`
	PendingWrites    int64  `json:"pending_writes"`
	WriteFailures    int64  `json:"write_failures"`
	WriteHealthy     bool   `json:"write_healthy"`
}

type AuditOptions struct {
	Provider  string
	WriteMode string
	Local     bool
	StateDir  string
	Retention time.Duration
	MaxBytes  int64
}

// MVP audit storage is SQLite on both local and server installations.
// Collection may be disabled; LOCAL_MODE bounds local retention.
func AuditOptionsFromEnv(stateDir string) (AuditOptions, error) {
	o := AuditOptions{Local: os.Getenv("LOCAL_MODE") == "true", StateDir: stateDir,
		Provider: strings.ToLower(strings.TrimSpace(os.Getenv("VAULT_AUDIT_PROVIDER"))), MaxBytes: 256 << 20}
	if o.Provider == "" {
		o.Provider = "sqlite"
	}
	if o.Provider != "off" && o.Provider != "sqlite" {
		return o, errors.New("VAULT_AUDIT_PROVIDER must be sqlite or off")
	}
	if o.Provider == "off" {
		return o, nil
	}
	o.WriteMode = strings.ToLower(strings.TrimSpace(os.Getenv("VAULT_AUDIT_WRITE_MODE")))
	if o.WriteMode == "" {
		o.WriteMode = "durable"
	}
	if o.WriteMode != "durable" && o.WriteMode != "async" {
		return o, errors.New("VAULT_AUDIT_WRITE_MODE must be durable or async")
	}
	o.Retention = 24 * time.Hour
	if v := os.Getenv("VAULT_AUDIT_RETENTION"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Minute || d > 365*24*time.Hour {
			return o, errors.New("VAULT_AUDIT_RETENTION must be between 1m and 8760h")
		}
		o.Retention = d
	}
	if o.Local && o.Retention > 24*time.Hour {
		return o, errors.New("local audit retention cannot exceed 24h")
	}
	if v := os.Getenv("VAULT_AUDIT_MAX_MB"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 16 || n > 65536 {
			return o, errors.New("VAULT_AUDIT_MAX_MB must be between 16 and 65536")
		}
		o.MaxBytes = n << 20
	}
	return o, nil
}

func OpenAuditBackend(o AuditOptions) (AuditBackend, error) {
	switch o.Provider {
	case "off":
		return disabledAudit{}, nil
	case "sqlite":
		return openSQLiteAuditMode(filepath.Join(o.StateDir, "audit.sqlite"), o.Retention, o.MaxBytes, true, o.WriteMode)
	default:
		return nil, errors.New("unknown audit provider")
	}
}

type disabledAudit struct{}

func (disabledAudit) Append(context.Context, AuditEvent) error { return nil }
func (disabledAudit) Query(context.Context, AuditFilter) ([]AuditEvent, error) {
	return []AuditEvent{}, nil
}
func (disabledAudit) Summary(context.Context, AuditFilter) (AuditSummary, error) {
	return emptyAuditSummary(), nil
}
func (disabledAudit) Info() AuditInfo { return AuditInfo{Provider: "off"} }
func (disabledAudit) Close() error    { return nil }
func emptyAuditSummary() AuditSummary {
	return AuditSummary{ByDay: []UsageBucket{}, ByTool: []UsageBucket{}}
}

// Separate lock: audit disk/network I/O must not block permission mutations.
type auditBinding struct {
	sync.RWMutex
	backend AuditBackend
}

func (s *MemoryStore) SetAuditBackend(b AuditBackend) {
	s.auditBinding.Lock()
	defer s.auditBinding.Unlock()
	s.auditBinding.backend = b
}
func (s *MemoryStore) auditProvider() AuditBackend {
	s.auditBinding.RLock()
	defer s.auditBinding.RUnlock()
	return s.auditBinding.backend
}
func (s *MemoryStore) AuditInfo() AuditInfo {
	if b := s.auditProvider(); b != nil {
		return b.Info()
	}
	return AuditInfo{Provider: "memory", Enabled: true, WriteHealthy: true}
}
func (s *MemoryStore) ReadAudit(f AuditFilter) ([]AuditEvent, error) {
	if b := s.auditProvider(); b != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return b.Query(ctx, f)
	}
	return s.QueryAudit(f), nil
}
func (s *MemoryStore) ReadAuditSummary(f AuditFilter) (AuditSummary, error) {
	if b := s.auditProvider(); b != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return b.Summary(ctx, f)
	}
	return s.SummarizeAudit(f), nil
}
