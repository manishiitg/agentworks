// Package store holds the gateway's tenant-scoped records.
//
// M0 uses an in-memory implementation behind the Store interface. M1 swaps in
// Postgres (+ Redis) without changing callers.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/pii"
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
	ConnectorID         string
	WorkspaceID         string
	UpstreamName        string
	PublicName          string // stable gateway-visible name
	Description         string
	Title               string
	InputSchema         []byte // raw JSON schema, passed through
	OutputSchema        []byte // normalized upstream output schema, if present
	Annotations         []byte // normalized tool annotations
	Fingerprint         string // sha256 over name+description+schema
	ApprovedFingerprint string // definition last approved by an admin
	Status              string
	DiscoveredAt        time.Time
	Version             int
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
	GroupIDs     []string
	ClientID     string
	ConnectorID  string
	PublicName   string
	UpstreamName string
	Decision     string
	Outcome      string
	DurationMs   int64
	ErrorText    string
	PIIAction    string
	PIIDataTypes []string
}

type AuditFilter struct {
	WorkspaceID string
	UserID      string
	GroupID     string
	ClientID    string
	ConnectorID string
	PublicName  string
	Decision    string
	Outcome     string
	After       time.Time
	Before      time.Time
	Limit       int
}

type UsageBucket struct {
	Key            string
	Count          int
	Denied         int
	UpstreamErrors int
}

type AuditSummary struct {
	Total          int
	Allowed        int
	Denied         int
	UpstreamErrors int
	AvgDurationMs  int64
	ByDay          []UsageBucket
	ByTool         []UsageBucket
}

// Fingerprint returns a stable snapshot fingerprint for quarantine diffing.
func Fingerprint(upstreamName, description string, inputSchema []byte, extra ...[]byte) string {
	h := sha256.New()
	h.Write([]byte(upstreamName))
	h.Write([]byte{0})
	h.Write([]byte(description))
	h.Write([]byte{0})
	h.Write(inputSchema)
	for _, part := range extra {
		h.Write([]byte{0})
		h.Write(part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// MemoryStore is the M0 Store implementation. All methods are safe for
// concurrent use.
type MemoryStore struct {
	mu           sync.RWMutex
	workspaces   map[string]Workspace
	users        map[string]User
	groups       map[string]Group
	members      map[string]map[string]bool // group ID -> user IDs
	connectors   map[string]Connector
	tools        map[string]ToolSnapshot   // by PublicName (unique per workspace in M0)
	toolVersions map[string][]ToolSnapshot // previous definitions for review
	grants       map[string]map[string]bool
	groupGrants  map[string]map[string]bool // group ID -> public names
	// groupServers attaches whole connectors to groups (AWS-style): every
	// tool of the connector, including tools discovered later.
	groupServers map[string]map[string]bool // group ID -> connector IDs
	apiKeys      map[string]APIKey          // by SHA-256 of token
	audit        []AuditEvent
	piiRules     map[string]pii.Rule
	piiReviews   map[string]PIIReview
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		workspaces:   map[string]Workspace{},
		users:        map[string]User{},
		groups:       map[string]Group{},
		members:      map[string]map[string]bool{},
		connectors:   map[string]Connector{},
		tools:        map[string]ToolSnapshot{},
		toolVersions: map[string][]ToolSnapshot{},
		grants:       map[string]map[string]bool{},
		groupGrants:  map[string]map[string]bool{},
		groupServers: map[string]map[string]bool{},
		apiKeys:      map[string]APIKey{},
		piiRules:     map[string]pii.Rule{},
		piiReviews:   map[string]PIIReview{},
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

// ConnectorNamespacePrefix is the shared public-tool prefix for one instance.
func ConnectorNamespacePrefix(provider, slug string) string {
	if slug == "" {
		return provider
	}
	return provider + "_" + slug
}

// AddConnectorUnique reserves a tool namespace atomically with the insert.
// A duplicate prefix can also arise from a provider containing an underscore,
// so comparing provider and slug as separate fields is insufficient.
func (s *MemoryStore) AddConnectorUnique(c Connector) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := ConnectorNamespacePrefix(c.Provider, c.InstanceSlug)
	for _, existing := range s.connectors {
		if existing.WorkspaceID == c.WorkspaceID && ConnectorNamespacePrefix(existing.Provider, existing.InstanceSlug) == want {
			return false
		}
	}
	s.connectors[c.ID] = c
	return true
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
// fingerprint on a known tool quarantines it until admin review.
func (s *MemoryStore) UpsertToolSnapshot(t ToolSnapshot) ToolSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, known := s.tools[t.PublicName]
	if !known {
		t.Version = 1
		if t.Status == "" {
			t.Status = StatusQuarantined
		} else if t.Status == StatusActive {
			t.ApprovedFingerprint = t.Fingerprint
		}
		s.tools[t.PublicName] = t
		return t
	}
	if prev.Fingerprint == t.Fingerprint {
		// A tool the last discovery dropped (DisableMissingTools) is back
		// unchanged: revive it with its grants intact. Quarantined tools
		// stay quarantined until admin review.
		if prev.Status == StatusDisabled {
			if prev.ApprovedFingerprint == prev.Fingerprint {
				prev.Status = StatusActive
			} else {
				prev.Status = StatusQuarantined
			}
			s.tools[t.PublicName] = prev
		}
		return prev
	}
	t.Version = prev.Version + 1
	t.Status = StatusQuarantined
	t.ApprovedFingerprint = prev.ApprovedFingerprint
	s.toolVersions[t.PublicName] = append(s.toolVersions[t.PublicName], prev)
	s.tools[t.PublicName] = t
	return t
}

// ApproveTool activates exactly the reviewed version. A concurrent sync that
// changes its definition makes the approval fail rather than approve new code.
func (s *MemoryStore) ApproveTool(workspaceID, publicName, fingerprint string, version int) (ToolSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tools[publicName]
	if !ok || t.WorkspaceID != workspaceID || t.Status != StatusQuarantined || t.Fingerprint != fingerprint || t.Version != version {
		return ToolSnapshot{}, false
	}
	t.Status = StatusActive
	t.ApprovedFingerprint = t.Fingerprint
	s.tools[publicName] = t
	return t, true
}

func (s *MemoryStore) ListToolVersions(workspaceID, publicName string) []ToolSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	current, ok := s.tools[publicName]
	if !ok || current.WorkspaceID != workspaceID {
		return nil
	}
	return append([]ToolSnapshot(nil), s.toolVersions[publicName]...)
}

// DisableMissingTools marks one connector's snapshots absent from a
// successful discovery as disabled, so a removed upstream tool stops being
// advertised and authorized. Grants are kept: if the tool returns unchanged,
// UpsertToolSnapshot revives it.
func (s *MemoryStore) DisableMissingTools(connectorID string, present map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, t := range s.tools {
		if t.ConnectorID == connectorID && !present[name] && t.Status != StatusDisabled {
			if t.Status == StatusActive && t.ApprovedFingerprint == "" {
				t.ApprovedFingerprint = t.Fingerprint
			}
			t.Status = StatusDisabled
			s.tools[name] = t
		}
	}
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
	hash := tokenHash(k.Token)
	k.Token = ""
	s.apiKeys[hash] = k
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *MemoryStore) APIKeyByToken(token string) (APIKey, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k, ok := s.apiKeys[tokenHash(token)]
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
	if k, ok := s.apiKeys[tokenHash(token)]; ok {
		k.LastUsedAt = time.Now()
		s.apiKeys[tokenHash(token)] = k
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
	e.GroupIDs = append([]string(nil), e.GroupIDs...)
	s.audit = append(s.audit, e)
}

func (s *MemoryStore) ListAudit() []AuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]AuditEvent, len(s.audit))
	copy(out, s.audit)
	return out
}

// QueryAudit filters immutable event metadata inside one workspace. Results
// are newest first so a limited view shows the most recent matching calls.
func (s *MemoryStore) QueryAudit(f AuditFilter) []AuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []AuditEvent{}
	for i := len(s.audit) - 1; i >= 0; i-- {
		e := s.audit[i]
		if e.WorkspaceID != f.WorkspaceID ||
			(f.UserID != "" && e.UserID != f.UserID) ||
			(f.GroupID != "" && !contains(e.GroupIDs, f.GroupID)) ||
			(f.ClientID != "" && e.ClientID != f.ClientID) ||
			(f.ConnectorID != "" && e.ConnectorID != f.ConnectorID) ||
			(f.PublicName != "" && !strings.Contains(strings.ToLower(e.PublicName), strings.ToLower(f.PublicName))) ||
			(f.Decision != "" && e.Decision != f.Decision) ||
			(f.Outcome != "" && e.Outcome != f.Outcome) ||
			(!f.After.IsZero() && e.Timestamp.Before(f.After)) ||
			(!f.Before.IsZero() && e.Timestamp.After(f.Before)) {
			continue
		}
		out = append(out, e)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out
}

// SummarizeAudit counts all matching calls, independent of the audit page
// limit. Buckets contain metadata only and are scoped by QueryAudit.
func (s *MemoryStore) SummarizeAudit(f AuditFilter) AuditSummary {
	f.Limit = 0
	events := s.QueryAudit(f)
	summary := AuditSummary{Total: len(events), ByDay: []UsageBucket{}, ByTool: []UsageBucket{}}
	days := map[string]*UsageBucket{}
	tools := map[string]*UsageBucket{}
	var duration int64
	for _, event := range events {
		if event.Decision == DecisionAllow {
			summary.Allowed++
		} else {
			summary.Denied++
		}
		if event.Outcome == OutcomeUpstreamError {
			summary.UpstreamErrors++
		}
		duration += event.DurationMs
		day := event.Timestamp.UTC().Format("2006-01-02")
		for _, pair := range []struct {
			buckets map[string]*UsageBucket
			key     string
		}{{days, day}, {tools, event.PublicName}} {
			bucket := pair.buckets[pair.key]
			if bucket == nil {
				bucket = &UsageBucket{Key: pair.key}
				pair.buckets[pair.key] = bucket
			}
			bucket.Count++
			if event.Decision != DecisionAllow {
				bucket.Denied++
			}
			if event.Outcome == OutcomeUpstreamError {
				bucket.UpstreamErrors++
			}
		}
	}
	if summary.Total > 0 {
		summary.AvgDurationMs = duration / int64(summary.Total)
	}
	for _, bucket := range days {
		summary.ByDay = append(summary.ByDay, *bucket)
	}
	for _, bucket := range tools {
		summary.ByTool = append(summary.ByTool, *bucket)
	}
	sort.Slice(summary.ByDay, func(i, j int) bool { return summary.ByDay[i].Key > summary.ByDay[j].Key })
	sort.Slice(summary.ByTool, func(i, j int) bool {
		if summary.ByTool[i].Count != summary.ByTool[j].Count {
			return summary.ByTool[i].Count > summary.ByTool[j].Count
		}
		return summary.ByTool[i].Key < summary.ByTool[j].Key
	})
	return summary
}

func (s *MemoryStore) PutPIIRule(rule pii.Rule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.piiRules[rule.ID] = rule
}

func (s *MemoryStore) DeletePIIRule(workspaceID, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rule, ok := s.piiRules[id]
	if !ok || rule.WorkspaceID != workspaceID {
		return false
	}
	delete(s.piiRules, id)
	return true
}

func (s *MemoryStore) ListPIIRules(workspaceID string) []pii.Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []pii.Rule{}
	for _, rule := range s.piiRules {
		if rule.WorkspaceID == workspaceID {
			out = append(out, rule)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// PIIReview stores only a payload hash and metadata. The original content is
// never retained, and an approval authorizes one matching client retry.
type PIIReview struct {
	ID          string
	WorkspaceID string
	UserID      string
	ConnectorID string
	PublicName  string
	Direction   string
	PayloadHash string
	DataTypes   []string
	Status      string
	CreatedAt   time.Time
}

const piiReviewLifetime = 24 * time.Hour

func reviewExpired(review PIIReview, now time.Time) bool {
	return review.CreatedAt.IsZero() || !now.Before(review.CreatedAt.Add(piiReviewLifetime))
}

func (s *MemoryStore) AddPIIReview(review PIIReview) {
	s.mu.Lock()
	defer s.mu.Unlock()
	review.DataTypes = append([]string(nil), review.DataTypes...)
	s.piiReviews[review.ID] = review
}

func (s *MemoryStore) ListPIIReviews(workspaceID string) []PIIReview {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []PIIReview{}
	for id, review := range s.piiReviews {
		if review.WorkspaceID == workspaceID {
			if review.Status != "consumed" && reviewExpired(review, time.Now()) {
				review.Status = "expired"
				s.piiReviews[id] = review
			}
			out = append(out, review)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (s *MemoryStore) ApprovePIIReview(workspaceID, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	review, ok := s.piiReviews[id]
	if !ok || review.WorkspaceID != workspaceID || review.Status != "pending" || reviewExpired(review, time.Now()) {
		return false
	}
	review.Status = "approved"
	s.piiReviews[id] = review
	return true
}

func (s *MemoryStore) ConsumePIIReview(workspaceID, userID, publicName, direction, payloadHash string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, review := range s.piiReviews {
		if review.WorkspaceID == workspaceID && review.UserID == userID && review.PublicName == publicName &&
			review.Direction == direction && review.PayloadHash == payloadHash && review.Status == "approved" && !reviewExpired(review, time.Now()) {
			review.Status = "consumed"
			s.piiReviews[id] = review
			return true
		}
	}
	return false
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
