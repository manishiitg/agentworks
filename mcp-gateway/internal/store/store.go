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
	ConnectorID  string
	WorkspaceID  string
	UpstreamName string
	PublicName   string // stable gateway-visible name
	Description  string
	InputSchema  []byte // raw JSON schema, passed through
	Fingerprint  string // sha256 over name+description+schema
	Status       string
	DiscoveredAt time.Time
	Version      int
}

// Grant binds a user to one registered tool.
type Grant struct {
	UserID     string
	PublicName string
}

// Group is a workspace group users belong to for tool grants.
type Group struct {
	ID          string
	WorkspaceID string
	Name        string
}

// GroupGrant binds a group to one registered tool.
type GroupGrant struct {
	GroupID    string
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
	mu          sync.RWMutex
	workspaces  map[string]Workspace
	users       map[string]User
	groups      map[string]Group
	members     map[string]map[string]bool // group ID -> user IDs
	connectors  map[string]Connector
	tools       map[string]ToolSnapshot // by PublicName (unique per workspace in M0)
	grants      map[string]map[string]bool
	groupGrants map[string]map[string]bool // group ID -> public names
	// groupServers attaches whole connectors to groups (AWS-style): every
	// tool of the connector, including tools discovered later.
	groupServers map[string]map[string]bool // group ID -> connector IDs
	apiKeys      map[string]APIKey          // by token
	audit        []AuditEvent
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		workspaces:   map[string]Workspace{},
		users:        map[string]User{},
		groups:       map[string]Group{},
		members:      map[string]map[string]bool{},
		connectors:   map[string]Connector{},
		tools:        map[string]ToolSnapshot{},
		grants:       map[string]map[string]bool{},
		groupGrants:  map[string]map[string]bool{},
		groupServers: map[string]map[string]bool{},
		apiKeys:      map[string]APIKey{},
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

func (s *MemoryStore) GetUser(id string) (User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	return u, ok
}

func (s *MemoryStore) ListUsers(workspaceID string) []User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []User{}
	for _, u := range s.users {
		if u.WorkspaceID == workspaceID {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *MemoryStore) AddGroup(g Group) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.groups[g.ID] = g
}

func (s *MemoryStore) GetGroup(id string) (Group, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	g, ok := s.groups[id]
	return g, ok
}

func (s *MemoryStore) ListGroups(workspaceID string) []Group {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Group{}
	for _, g := range s.groups {
		if g.WorkspaceID == workspaceID {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *MemoryStore) AddMember(groupID, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.members[groupID] == nil {
		s.members[groupID] = map[string]bool{}
	}
	s.members[groupID][userID] = true
}

func (s *MemoryStore) RemoveMember(groupID, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.members[groupID], userID)
}

// GroupsOf returns the IDs of groups the user belongs to.
func (s *MemoryStore) GroupsOf(userID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []string{}
	for gid, users := range s.members {
		if users[userID] {
			out = append(out, gid)
		}
	}
	sort.Strings(out)
	return out
}

// MembersOf returns the user IDs in a group.
func (s *MemoryStore) MembersOf(groupID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []string{}
	for uid := range s.members[groupID] {
		out = append(out, uid)
	}
	sort.Strings(out)
	return out
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

// DeleteConnector removes a connector and its tool snapshots. Grants naming
// those tools become dangling and deny (Authorize requires a live tool).
func (s *MemoryStore) DeleteConnector(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.connectors, id)
	for name, t := range s.tools {
		if t.ConnectorID == id {
			delete(s.tools, name)
		}
	}
}

// ListToolsForConnector returns snapshots for one connector.
func (s *MemoryStore) ListToolsForConnector(connectorID string) []ToolSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []ToolSnapshot{}
	for _, t := range s.tools {
		if t.ConnectorID == connectorID {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PublicName < out[j].PublicName })
	return out
}

func (s *MemoryStore) ListConnectors(workspaceID string) []Connector {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Connector{}
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
	out := []ToolSnapshot{}
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

func (s *MemoryStore) AddGroupGrant(g GroupGrant) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.groupGrants[g.GroupID] == nil {
		s.groupGrants[g.GroupID] = map[string]bool{}
	}
	s.groupGrants[g.GroupID][g.PublicName] = true
}

func (s *MemoryStore) RevokeGroupGrant(groupID, publicName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.groupGrants[groupID], publicName)
}

// HasGroupGrant reports whether any of the user's groups grants the tool.
func (s *MemoryStore) HasGroupGrant(userID, publicName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for gid, users := range s.members {
		if users[userID] && s.groupGrants[gid][publicName] {
			return true
		}
	}
	return false
}

// GroupGrantsFor returns the public names granted to a group.
func (s *MemoryStore) GroupGrantsFor(groupID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []string{}
	for name := range s.groupGrants[groupID] {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (s *MemoryStore) AddGroupServerGrant(groupID, connectorID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.groupServers[groupID] == nil {
		s.groupServers[groupID] = map[string]bool{}
	}
	s.groupServers[groupID][connectorID] = true
}

func (s *MemoryStore) RevokeGroupServerGrant(groupID, connectorID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.groupServers[groupID], connectorID)
}

// HasServerGrant reports whether any of the user's groups is attached to
// the whole connector.
func (s *MemoryStore) HasServerGrant(userID, connectorID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for gid, users := range s.members {
		if users[userID] && s.groupServers[gid][connectorID] {
			return true
		}
	}
	return false
}

// GroupHasTool reports whether the group directly grants the tool.
func (s *MemoryStore) GroupHasTool(groupID, publicName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.groupGrants[groupID][publicName]
}

// GroupHasServer reports whether the group is attached to the connector.
func (s *MemoryStore) GroupHasServer(groupID, connectorID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.groupServers[groupID][connectorID]
}

// RenameGroup changes a group's display name.
func (s *MemoryStore) RenameGroup(groupID, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.groups[groupID]; ok {
		g.Name = name
		s.groups[groupID] = g
	}
}

// APIKey is a shareable credential carrying exactly one group's grants.
type APIKey struct {
	ID          string
	WorkspaceID string
	GroupID     string
	Label       string
	Token       string
	CreatedAt   time.Time
	LastUsedAt  time.Time
}

func (s *MemoryStore) AddAPIKey(k APIKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apiKeys[k.Token] = k
}

func (s *MemoryStore) APIKeyByToken(token string) (APIKey, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k, ok := s.apiKeys[token]
	return k, ok
}

// ListAPIKeys returns the group's keys newest first, with tokens scrubbed.
func (s *MemoryStore) ListAPIKeys(groupID string) []APIKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []APIKey{}
	for _, k := range s.apiKeys {
		if k.GroupID == groupID {
			k.Token = ""
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// RevokeAPIKey deletes a key by ID. It reports whether one existed.
func (s *MemoryStore) RevokeAPIKey(groupID, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, k := range s.apiKeys {
		if k.GroupID == groupID && k.ID == id {
			delete(s.apiKeys, token)
			return true
		}
	}
	return false
}

// TouchAPIKey records a use. Missing tokens are ignored.
func (s *MemoryStore) TouchAPIKey(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.apiKeys[token]; ok {
		k.LastUsedAt = time.Now()
		s.apiKeys[token] = k
	}
}

// GroupServersFor returns the connector IDs attached to a group.
func (s *MemoryStore) GroupServersFor(groupID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []string{}
	for id := range s.groupServers[groupID] {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ToolHolders returns the user and group IDs holding a tool grant.
func (s *MemoryStore) ToolHolders(publicName string) (users, groups []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for uid, tools := range s.grants {
		if tools[publicName] {
			users = append(users, uid)
		}
	}
	for gid, tools := range s.groupGrants {
		if tools[publicName] {
			groups = append(groups, gid)
		}
	}
	sort.Strings(users)
	sort.Strings(groups)
	return users, groups
}

// UserGrantsFor returns the public names granted directly to a user.
func (s *MemoryStore) UserGrantsFor(userID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []string{}
	for name := range s.grants[userID] {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
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
