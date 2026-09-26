// Package store holds the gateway's tenant-scoped records.
//
// M0 uses an in-memory implementation behind the Store interface. M1 swaps in
// Postgres (+ Redis) without changing callers.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"sync"
	"time"
)

// Statuses for connectors and tool snapshots.
const (
	StatusActive      = "active"
	StatusDisabled    = "disabled"
	StatusQuarantined = "quarantined"
)

type Workspace struct {
	ID   string
	Name string
}

type User struct {
	ID          string
	WorkspaceID string
	Email       string
}

// Connector is one upstream MCP server instance. A workspace may hold N
// instances of the same provider with different labels/credentials.
type Connector struct {
	ID           string
	WorkspaceID  string
	Provider     string // e.g. "linear" (catalog key)
	InstanceSlug string // e.g. "acme" (empty while single instance)
	Label        string
	UpstreamURL  string
	Status       string
}

// ToolSnapshot is one discovered upstream tool plus its gateway identity.
type ToolSnapshot struct {
	ConnectorID   string
	WorkspaceID   string
	UpstreamName  string
	PublicName    string // stable gateway-visible name
	Description   string
	InputSchema   []byte // raw JSON schema, passed through
	Fingerprint   string // sha256 over name+description+schema
	Status        string
	DiscoveredAt  time.Time
	Version       int
}

// Grant binds a user to one registered tool (M0). M1 adds groups.
type Grant struct {
	UserID     string
	PublicName string
}

// Decisions and outcomes for audit events.
const (
	DecisionAllow = "allow"
	DecisionDeny  = "deny"

	OutcomeOK            = "ok"
	OutcomeDenied        = "denied"
	OutcomeUpstreamError = "upstream_error"
)

// AuditEvent is one attempted tool call. No raw arguments, results, or
// secrets are stored.
type AuditEvent struct {
	ID           string
	CallID       string
	Timestamp    time.Time
	WorkspaceID  string
	UserID       string
	ConnectorID  string
	PublicName   string
	UpstreamName string
	Decision     string
	Outcome      string
	DurationMs   int64
	ErrorText    string
}

// Fingerprint returns a stable snapshot fingerprint for quarantine diffing.
func Fingerprint(upstreamName, description string, inputSchema []byte) string {
	h := sha256.New()
	h.Write([]byte(upstreamName))
	h.Write([]byte{0})
	h.Write([]byte(description))
	h.Write([]byte{0})
	h.Write(inputSchema)
	return hex.EncodeToString(h.Sum(nil))
}

// MemoryStore is the M0 Store implementation. All methods are safe for
// concurrent use.
type MemoryStore struct {
	mu         sync.RWMutex
	workspaces map[string]Workspace
	users      map[string]User
	connectors map[string]Connector
	tools      map[string]ToolSnapshot // by PublicName (unique per workspace in M0)
	grants     map[string]map[string]bool
	audit      []AuditEvent
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		workspaces: map[string]Workspace{},
		users:      map[string]User{},
		connectors: map[string]Connector{},
		tools:      map[string]ToolSnapshot{},
		grants:     map[string]map[string]bool{},
	}
}

func (s *MemoryStore) AddWorkspace(w Workspace) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workspaces[w.ID] = w
}

func (s *MemoryStore) AddUser(u User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[u.ID] = u
}

func (s *MemoryStore) AddConnector(c Connector) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connectors[c.ID] = c
}

func (s *MemoryStore) GetConnector(id string) (Connector, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.connectors[id]
	return c, ok
}

func (s *MemoryStore) ListConnectors(workspaceID string) []Connector {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Connector
	for _, c := range s.connectors {
		if c.WorkspaceID == workspaceID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// UpsertToolSnapshot inserts or versions a discovered tool. A changed
// fingerprint on a known tool quarantines it until admin review (M1 surfaces
// the review UI; M0 keeps the old version served and marks the new one).
func (s *MemoryStore) UpsertToolSnapshot(t ToolSnapshot) ToolSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, known := s.tools[t.PublicName]
	if !known {
		t.Version = 1
		if t.Status == "" {
			t.Status = StatusActive
		}
		s.tools[t.PublicName] = t
		return t
	}
	if prev.Fingerprint == t.Fingerprint {
		return prev
	}
	t.Version = prev.Version + 1
	t.Status = StatusQuarantined
	s.tools[t.PublicName] = t
	return t
}

func (s *MemoryStore) GetTool(publicName string) (ToolSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tools[publicName]
	return t, ok
}

func (s *MemoryStore) ListTools(workspaceID string) []ToolSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ToolSnapshot
	for _, t := range s.tools {
		if t.WorkspaceID == workspaceID {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PublicName < out[j].PublicName })
	return out
}

func (s *MemoryStore) AddGrant(g Grant) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.grants[g.UserID] == nil {
		s.grants[g.UserID] = map[string]bool{}
	}
	s.grants[g.UserID][g.PublicName] = true
}

func (s *MemoryStore) RevokeGrant(userID, publicName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.grants[userID], publicName)
}

func (s *MemoryStore) HasGrant(userID, publicName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.grants[userID][publicName]
}

func (s *MemoryStore) AppendAudit(e AuditEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audit = append(s.audit, e)
}

func (s *MemoryStore) ListAudit() []AuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]AuditEvent, len(s.audit))
	copy(out, s.audit)
	return out
}
