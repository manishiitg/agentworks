package terminalleases

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
)

const (
	DefaultBoundedProcessGrace  = 30 * time.Second
	DefaultClosedLeaseRetention = 30 * time.Minute
)

type Policy string

const (
	PolicyPersistent Policy = "persistent"
	PolicyBounded    Policy = "bounded"
)

type ProcessState string

const (
	ProcessLive    ProcessState = "live"
	ProcessClosing ProcessState = "closing"
	ProcessClosed  ProcessState = "closed"
)

// Lease owns a live coding-agent tmux process independently from its UI snapshot.
// Snapshot retention and dismissal must never decide whether the process exists.
type Lease struct {
	TerminalID        string
	TmuxSession       string
	SessionID         string
	OwnerID           string
	ExecutionID       string
	ExecutionKind     string
	WorkflowPath      string
	Provider          string
	Policy            Policy
	ProcessState      ProcessState
	InstanceID        string
	OwnerPID          int
	OwnerStartedAt    time.Time
	LastActivity      time.Time
	ProcessDeadline   *time.Time
	SnapshotDeadline  *time.Time
	SnapshotDismissed bool
	CloseReason       string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type Registry struct {
	mu             sync.RWMutex
	instanceID     string
	ownerPID       int
	ownerStartedAt time.Time
	byTmux         map[string]Lease
	byTerminal     map[string]string
}

func NewRegistry(instanceID string, ownerPID int, ownerStartedAt time.Time) *Registry {
	if ownerStartedAt.IsZero() {
		ownerStartedAt = time.Now()
	}
	return &Registry{
		instanceID:     strings.TrimSpace(instanceID),
		ownerPID:       ownerPID,
		ownerStartedAt: ownerStartedAt,
		byTmux:         make(map[string]Lease),
		byTerminal:     make(map[string]string),
	}
}

func (r *Registry) InstanceID() string {
	if r == nil {
		return ""
	}
	return r.instanceID
}

func (r *Registry) OwnerPID() int {
	if r == nil {
		return 0
	}
	return r.ownerPID
}

func (r *Registry) OwnerStartedAt() time.Time {
	if r == nil {
		return time.Time{}
	}
	return r.ownerStartedAt
}

// Observe updates ownership from one terminal snapshot. acquired is true only
// when this backend first learns about the tmux process, allowing the caller to
// write ownership markers once instead of on every streaming token.
func (r *Registry) Observe(snapshot terminals.Snapshot, now time.Time) (lease Lease, acquired bool) {
	if r == nil {
		return Lease{}, false
	}
	tmuxSession := strings.TrimSpace(snapshot.TmuxSession)
	terminalID := strings.TrimSpace(snapshot.TerminalID)
	if tmuxSession == "" || terminalID == "" {
		return Lease{}, false
	}
	if now.IsZero() {
		now = time.Now()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	lease, exists := r.byTmux[tmuxSession]
	acquired = !exists || lease.InstanceID != r.instanceID
	if !exists {
		lease.CreatedAt = now
	}
	lease.TerminalID = terminalID
	lease.TmuxSession = tmuxSession
	lease.SessionID = strings.TrimSpace(snapshot.SessionID)
	lease.OwnerID = strings.TrimSpace(snapshot.OwnerID)
	lease.ExecutionID = strings.TrimSpace(snapshot.ExecutionID)
	lease.ExecutionKind = strings.TrimSpace(snapshot.ExecutionKind)
	lease.WorkflowPath = strings.TrimSpace(snapshot.WorkflowPath)
	lease.Provider = providerFromTmuxSession(tmuxSession)
	lease.Policy = policyForSnapshot(snapshot)
	lease.InstanceID = r.instanceID
	lease.OwnerPID = r.ownerPID
	lease.OwnerStartedAt = r.ownerStartedAt
	lease.SnapshotDeadline = cloneTime(snapshot.ClosesAt)
	lease.LastActivity = snapshot.UpdatedAt
	if lease.LastActivity.IsZero() {
		lease.LastActivity = now
	}
	lease.UpdatedAt = now

	if snapshot.Active {
		lease.ProcessState = ProcessLive
		lease.ProcessDeadline = nil
		lease.CloseReason = ""
	} else if lease.ProcessState != ProcessClosed {
		lease.ProcessState = ProcessClosing
		if lease.Policy == PolicyBounded && lease.ProcessDeadline == nil {
			deadline := now.Add(DefaultBoundedProcessGrace)
			lease.ProcessDeadline = &deadline
		}
	}

	if previousTmux := r.byTerminal[terminalID]; previousTmux != "" && previousTmux != tmuxSession {
		delete(r.byTmux, previousTmux)
	}
	r.byTmux[tmuxSession] = lease
	r.byTerminal[terminalID] = tmuxSession
	return lease, acquired
}

func (r *Registry) MarkSnapshotDismissed(terminalID string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	tmuxSession := r.byTerminal[strings.TrimSpace(terminalID)]
	lease, ok := r.byTmux[tmuxSession]
	if !ok {
		return false
	}
	lease.SnapshotDismissed = true
	lease.UpdatedAt = time.Now()
	r.byTmux[tmuxSession] = lease
	return true
}

func (r *Registry) MarkClosed(tmuxSession, reason string, now time.Time) (Lease, bool) {
	if r == nil {
		return Lease{}, false
	}
	if now.IsZero() {
		now = time.Now()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	lease, ok := r.byTmux[strings.TrimSpace(tmuxSession)]
	if !ok {
		return Lease{}, false
	}
	lease.ProcessState = ProcessClosed
	lease.ProcessDeadline = nil
	if lease.SnapshotDeadline == nil {
		deadline := now.Add(DefaultClosedLeaseRetention)
		lease.SnapshotDeadline = &deadline
	}
	lease.CloseReason = strings.TrimSpace(reason)
	lease.UpdatedAt = now
	r.byTmux[lease.TmuxSession] = lease
	return lease, true
}

func (r *Registry) List() []Lease {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Lease, 0, len(r.byTmux))
	for _, lease := range r.byTmux {
		out = append(out, cloneLease(lease))
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

func (r *Registry) GetByTerminal(terminalID string) (Lease, bool) {
	if r == nil {
		return Lease{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	lease, ok := r.byTmux[r.byTerminal[strings.TrimSpace(terminalID)]]
	return cloneLease(lease), ok
}

func (r *Registry) Prune(now time.Time) int {
	if r == nil {
		return 0
	}
	if now.IsZero() {
		now = time.Now()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	removed := 0
	for tmuxSession, lease := range r.byTmux {
		if lease.ProcessState != ProcessClosed || lease.SnapshotDeadline == nil || now.Before(*lease.SnapshotDeadline) {
			continue
		}
		delete(r.byTmux, tmuxSession)
		delete(r.byTerminal, lease.TerminalID)
		removed++
	}
	return removed
}

func policyForSnapshot(snapshot terminals.Snapshot) Policy {
	// A scheduled main agent may be waiting for an asynchronous workflow child.
	// It must retain its native coding-CLI session so that an eventual
	// AUTO-NOTIFICATION can continue the same conversation. The idle backstop
	// owns cleanup once the entire session is actually complete.
	if snapshot.ExecutionKind == "main_agent" || snapshot.Scope == "main_agent" || strings.HasPrefix(snapshot.OwnerID, "main:") {
		return PolicyPersistent
	}
	return PolicyBounded
}

func providerFromTmuxSession(tmuxSession string) string {
	switch {
	case strings.HasPrefix(tmuxSession, "mlp-claude-code"):
		return "claude-code"
	case strings.HasPrefix(tmuxSession, "mlp-codex-cli"):
		return "codex-cli"
	case strings.HasPrefix(tmuxSession, "mlp-cursor-cli"):
		return "cursor-cli"
	case strings.HasPrefix(tmuxSession, "mlp-pi-cli"):
		return "pi-cli"
	case strings.HasPrefix(tmuxSession, "mlp-muse-"):
		return "muse-cli"
	case strings.HasPrefix(tmuxSession, "agy-int-"):
		return "agy-cli"
	default:
		return "coding-cli"
	}
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneLease(lease Lease) Lease {
	lease.ProcessDeadline = cloneTime(lease.ProcessDeadline)
	lease.SnapshotDeadline = cloneTime(lease.SnapshotDeadline)
	return lease
}
