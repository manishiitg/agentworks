// Package store holds the gateway's tenant-scoped records.
//
// M0 uses an in-memory implementation behind the Store interface. M1 swaps in
// Postgres (+ Redis) without changing callers.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/access"
)

// Statuses for connectors and tool snapshots.
const (
	StatusAuthRequired = "authentication_required"
	StatusActive       = "active"
	StatusDisabled     = "disabled"
	StatusQuarantined  = "quarantined"
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
	ID                string
	WorkspaceID       string
	Provider          string // e.g. "linear" (catalog key)
	InstanceSlug      string // e.g. "acme" (empty while single instance)
	Label             string
	UpstreamURL       string
	OAuthServer       string // catalog provider; never an access/refresh token
	OAuthCredentialID string // connection-scoped identity; empty preserves legacy provider credentials
	AuthScheme        string // empty/Bearer or Basic; credential bytes are stored separately
	Status            string
	// VaultID is the person-owned vault this connection belongs to; empty for a platform connection (PLAT-507). Only
	// the vault's owners may change or remove it.
	VaultID string `json:",omitempty"`
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
	Description string
	BuiltIn     bool
	// Kind is "vault" for a person-owned vault (a group whose owners, not only the platform administrator, manage its
	// connections and members); empty for an ordinary platform group (PLAT-507).
	Kind   string   `json:",omitempty"`
	Owners []string `json:",omitempty"`
}

const MaxGroupDescriptionLength = 1000

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

// AuditEvent records one attempted tool call, including bounded payload snapshots.
type AuditEvent struct {
	ID              string
	CallID          string
	Timestamp       time.Time
	WorkspaceID     string
	UserID          string
	GroupIDs        []string
	ClientID        string
	ConnectorID     string
	PublicName      string
	UpstreamName    string
	Decision        string
	Outcome         string
	DurationMs      int64
	ErrorText       string
	Input           json.RawMessage `json:",omitempty"`
	Output          json.RawMessage `json:",omitempty"`
	InputTruncated  bool            `json:",omitempty"`
	OutputTruncated bool            `json:",omitempty"`
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

type PolicyEvent struct {
	At          time.Time `json:"at"`
	Actor       string    `json:"actor"`
	Action      string    `json:"action"`
	PackageID   string    `json:"package_id"`
	Version     int       `json:"version"`
	GroupID     string    `json:"group_id,omitempty"`
	ConnectorID string    `json:"connector_id,omitempty"`
	UserID      string    `json:"user_id,omitempty"`
	// Detail is what changed when the action alone does not say, e.g. the
	// SQL statements of a sql_mutation.
	Detail string `json:"detail,omitempty"`
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
	mu              sync.RWMutex
	persistence     *sqlitePersistence
	workspaces      map[string]Workspace
	users           map[string]User
	secretResources map[string]SecretResource
	secretGrants    map[string]map[string]bool
	groups          map[string]Group
	members         map[string]map[string]bool // group ID -> user IDs
	connectors      map[string]Connector
	connectorBearer map[string]string         // never returned in connector API responses
	tools           map[string]ToolSnapshot   // by PublicName (unique per workspace in M0)
	toolVersions    map[string][]ToolSnapshot // previous definitions for review
	grants          map[string]map[string]bool
	groupGrants     map[string]map[string]bool // group ID -> public names
	// groupServers attaches whole connectors to groups (AWS-style): every
	// tool of the connector, including tools discovered later.
	groupServers  map[string]map[string]bool // group ID -> connector IDs
	packageDrafts map[string]access.Package
	packageLive   map[string]access.Package
	governedTools map[string]map[string]bool // workspace -> names ever governed by a live package
	policyEvents  map[string][]PolicyEvent
	// platformRevoked records admin removals from the built-in Platform group
	// ("secret:NAME", "server:ID") so automatic granting never restores them.
	platformRevoked map[string]bool
	apiKeys         map[string]APIKey // by SHA-256 of token
	auditBinding    auditBinding
	audit           []AuditEvent
	auditStart      int // oldest event in the bounded ring after it fills
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		workspaces:      map[string]Workspace{},
		users:           map[string]User{},
		secretResources: map[string]SecretResource{},
		secretGrants:    map[string]map[string]bool{},
		groups:          map[string]Group{},
		members:         map[string]map[string]bool{},
		connectors:      map[string]Connector{},
		connectorBearer: map[string]string{},
		tools:           map[string]ToolSnapshot{},
		toolVersions:    map[string][]ToolSnapshot{},
		grants:          map[string]map[string]bool{},
		groupGrants:     map[string]map[string]bool{},
		groupServers:    map[string]map[string]bool{},
		packageDrafts:   map[string]access.Package{},
		packageLive:     map[string]access.Package{},
		governedTools:   map[string]map[string]bool{},
		policyEvents:    map[string][]PolicyEvent{},
		platformRevoked: map[string]bool{},
		apiKeys:         map[string]APIKey{},
	}
}

func (s *MemoryStore) AddWorkspace(w Workspace) {
	s.mu.Lock()
	defer s.persistUnlock()
	s.workspaces[w.ID] = w
}

func (s *MemoryStore) AddUser(u User) {
	s.mu.Lock()
	defer s.persistUnlock()
	s.users[u.ID] = u
	for gid, group := range s.groups {
		if group.BuiltIn {
			if s.members[gid] == nil {
				s.members[gid] = map[string]bool{}
			}
			if group.WorkspaceID == u.WorkspaceID {
				s.members[gid][u.ID] = true
			} else {
				delete(s.members[gid], u.ID)
			}
		}
	}
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
	defer s.persistUnlock()
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
	sort.Slice(out, func(i, j int) bool {
		if out[i].BuiltIn != out[j].BuiltIn {
			return out[i].BuiltIn
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (s *MemoryStore) AddMember(groupID, userID string) {
	s.mu.Lock()
	defer s.persistUnlock()
	if s.members[groupID] == nil {
		s.members[groupID] = map[string]bool{}
	}
	s.members[groupID][userID] = true
}

func (s *MemoryStore) RemoveMember(groupID, userID string) {
	s.mu.Lock()
	defer s.persistUnlock()
	if s.groups[groupID].BuiltIn {
		return
	}
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
	defer s.persistUnlock()
	if _, exists := s.connectors[c.ID]; !exists {
		s.autoGrantServerLocked(c)
	}
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
	defer s.persistUnlock()
	if _, exists := s.connectors[c.ID]; exists {
		return false
	}
	want := ConnectorNamespacePrefix(c.Provider, c.InstanceSlug)
	for _, existing := range s.connectors {
		if existing.WorkspaceID == c.WorkspaceID && ConnectorNamespacePrefix(existing.Provider, existing.InstanceSlug) == want {
			return false
		}
	}
	s.autoGrantServerLocked(c)
	s.connectors[c.ID] = c
	return true
}

func (s *MemoryStore) GetConnector(id string) (Connector, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.connectors[id]
	return c, ok
}

// DeleteConnector removes a connector and policy tied to its public names.
// A future connector may reuse the namespace, so dangling grants and rules
// must not become active again when that connector discovers its tools.
func (s *MemoryStore) DeleteConnector(id string) {
	s.mu.Lock()
	defer s.persistUnlock()
	workspaceID := s.connectors[id].WorkspaceID
	delete(s.connectors, id)
	delete(s.connectorBearer, id)
	names := make(map[string]bool)
	for name, t := range s.tools {
		if t.ConnectorID == id {
			names[name] = true
			delete(s.tools, name)
			delete(s.toolVersions, name)
		}
	}
	for _, grants := range s.grants {
		for name := range names {
			delete(grants, name)
		}
	}
	for _, grants := range s.groupGrants {
		for name := range names {
			delete(grants, name)
		}
	}
	for _, servers := range s.groupServers {
		delete(servers, id)
	}
	delete(s.platformRevoked, "server:"+id)
	for packageID, p := range s.packageDrafts {
		kept := make([]access.ToolRule, 0, len(p.Rules))
		for _, rule := range p.Rules {
			if !names[rule.PublicName] {
				kept = append(kept, rule)
			}
		}
		if len(kept) == 0 {
			delete(s.packageDrafts, packageID)
		} else if len(kept) != len(p.Rules) {
			p.Rules = kept
			p.Version++
			s.packageDrafts[packageID] = access.Clone(p)
		}
	}
	for packageID, p := range s.packageLive {
		kept := make([]access.ToolRule, 0, len(p.Rules))
		for _, rule := range p.Rules {
			if !names[rule.PublicName] {
				kept = append(kept, rule)
			}
		}
		if len(kept) == 0 {
			delete(s.packageLive, packageID)
		} else if len(kept) != len(p.Rules) {
			p.Rules = kept
			p.Version++
			s.packageLive[packageID] = access.Clone(p)
		}
	}
	for name := range names {
		delete(s.governedTools[workspaceID], name)
	}
}

func (s *MemoryStore) SetConnectorBearer(id, token string) {
	s.mu.Lock()
	defer s.persistUnlock()
	if token == "" {
		delete(s.connectorBearer, id)
	} else {
		s.connectorBearer[id] = token
	}
}

func (s *MemoryStore) ConnectorBearer(id string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connectorBearer[id]
}

var ErrPolicyConflict = errors.New("permissions changed; reload before editing")

// SaveAccessPackage validates and applies permissions in one configuration
// transaction. Version checks prevent overwriting concurrent administrator edits.
func (s *MemoryStore) SaveAccessPackage(p access.Package, expectedVersion int, actor string) (saved access.Package, err error) {
	s.mu.Lock()
	defer func() {
		s.persistUnlock()
		if persistenceErr := s.PersistenceError(); persistenceErr != nil {
			saved, err = access.Package{}, persistenceErr
		}
	}()
	if err = validatePackageState(p, p.WorkspaceID, s.durableState()); err != nil {
		return access.Package{}, err
	}
	version := 0
	for _, records := range []map[string]access.Package{s.packageLive, s.packageDrafts} {
		if previous, ok := records[p.ID]; ok {
			if previous.WorkspaceID != p.WorkspaceID || previous.GroupID != p.GroupID {
				return access.Package{}, ErrPolicyConflict
			}
			if previous.Version > version {
				version = previous.Version
			}
		}
	}
	if expectedVersion != version {
		return access.Package{}, ErrPolicyConflict
	}
	for _, rule := range p.Rules {
		t := s.tools[rule.PublicName]
		c, ok := s.connectors[t.ConnectorID]
		if !ok || c.WorkspaceID != p.WorkspaceID || c.Status != StatusActive {
			return access.Package{}, errors.New("permissions require an active connector")
		}
	}
	p.Version, p.Status = version+1, "published"
	if s.governedTools[p.WorkspaceID] == nil {
		s.governedTools[p.WorkspaceID] = map[string]bool{}
	}
	for _, rule := range p.Rules {
		s.governedTools[p.WorkspaceID][rule.PublicName] = true
	}
	s.packageLive[p.ID] = access.Clone(p)
	delete(s.packageDrafts, p.ID)
	s.policyEvents[p.WorkspaceID] = append(s.policyEvents[p.WorkspaceID], PolicyEvent{At: time.Now().UTC(), Actor: actor, Action: "save_permissions", PackageID: p.ID, Version: p.Version})
	return access.Clone(p), nil
}

// ListAppliedPackages excludes historical pending drafts. They are retained
// only for compatibility with saved configuration, never activated on startup.
func (s *MemoryStore) ListAppliedPackages(workspaceID string) []access.Package {
	all := s.ListPackages(workspaceID)
	out := make([]access.Package, 0, len(all))
	for _, p := range all {
		if p.Status != "draft" {
			out = append(out, p)
		}
	}
	return out
}

// Legacy draft helpers support reading/migrating saved configurations. They
// are not exposed by the management API, builder or SQL mutation tools.
// SavePackageDraft keeps edits separate from the published runtime policy.
func (s *MemoryStore) SavePackageDraft(p access.Package, expectedVersion int) (access.Package, bool) {
	s.mu.Lock()
	defer s.persistUnlock()
	current := 0
	if previous, ok := s.packageDrafts[p.ID]; ok {
		current = previous.Version
	} else if previous, ok := s.packageLive[p.ID]; ok {
		current = previous.Version
	}
	if current != expectedVersion {
		return access.Package{}, false
	}
	p.Version = current + 1
	p.Status = "draft"
	s.packageDrafts[p.ID] = access.Clone(p)
	return access.Clone(p), true
}

func (s *MemoryStore) GetPackageDraft(workspaceID, id string) (access.Package, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.packageDrafts[id]
	return access.Clone(p), ok && p.WorkspaceID == workspaceID
}

// PublishPackage requires the exact draft version that was validated.
func (s *MemoryStore) PublishPackage(workspaceID, id string, version int) (access.Package, bool) {
	s.mu.Lock()
	defer s.persistUnlock()
	p, ok := s.packageDrafts[id]
	if !ok || p.WorkspaceID != workspaceID || p.Version != version {
		return access.Package{}, false
	}
	p.Status = "published"
	if s.governedTools[workspaceID] == nil {
		s.governedTools[workspaceID] = map[string]bool{}
	}
	for _, rule := range p.Rules {
		s.governedTools[workspaceID][rule.PublicName] = true
	}
	s.packageLive[id] = access.Clone(p)
	delete(s.packageDrafts, id)
	return access.Clone(p), true
}

// RevokePackage leaves a tombstone so legacy grants cannot silently regain
// access to tools that were governed by this package.
func (s *MemoryStore) RevokePackage(workspaceID, id string) (access.Package, bool) {
	s.mu.Lock()
	defer s.persistUnlock()
	p, ok := s.packageLive[id]
	if !ok || p.WorkspaceID != workspaceID || p.Status != "published" {
		return access.Package{}, false
	}
	p.Status = "revoked"
	p.Version++
	s.packageLive[id] = access.Clone(p)
	delete(s.packageDrafts, id)
	return access.Clone(p), true
}

func (s *MemoryStore) ListPackages(workspaceID string) []access.Package {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []access.Package{}
	for _, p := range s.packageLive {
		if p.WorkspaceID == workspaceID {
			out = append(out, access.Clone(p))
		}
	}
	for _, p := range s.packageDrafts {
		if p.WorkspaceID == workspaceID {
			out = append(out, access.Clone(p))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID || (out[i].ID == out[j].ID && out[i].Status < out[j].Status)
	})
	return out
}

func (s *MemoryStore) PolicyForTool(workspaceID, publicName string) ([]access.Package, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []access.Package{}
	for _, p := range s.packageLive {
		if p.WorkspaceID != workspaceID {
			continue
		}
		for _, rule := range p.Rules {
			if rule.PublicName == publicName {
				out = append(out, access.Clone(p))
				break
			}
		}
	}
	return out, s.governedTools[workspaceID][publicName]
}

func (s *MemoryStore) AppendPolicyEvent(workspaceID string, event PolicyEvent) {
	s.mu.Lock()
	defer s.persistUnlock()
	events := append(s.policyEvents[workspaceID], event)
	if len(events) > 10000 {
		events = append([]PolicyEvent(nil), events[len(events)-10000:]...)
	}
	s.policyEvents[workspaceID] = events
}

func (s *MemoryStore) ListPolicyEvents(workspaceID string) []PolicyEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]PolicyEvent{}, s.policyEvents[workspaceID]...)
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
	defer s.persistUnlock()
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
	defer s.persistUnlock()
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
	defer s.persistUnlock()
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
	defer s.persistUnlock()
	if s.grants[g.UserID] == nil {
		s.grants[g.UserID] = map[string]bool{}
	}
	s.grants[g.UserID][g.PublicName] = true
}

func (s *MemoryStore) RevokeGrant(userID, publicName string) {
	s.mu.Lock()
	defer s.persistUnlock()
	delete(s.grants[userID], publicName)
}

func (s *MemoryStore) HasGrant(userID, publicName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.grants[userID][publicName]
}

func (s *MemoryStore) AddGroupGrant(g GroupGrant) {
	s.mu.Lock()
	defer s.persistUnlock()
	if s.groupGrants[g.GroupID] == nil {
		s.groupGrants[g.GroupID] = map[string]bool{}
	}
	s.groupGrants[g.GroupID][g.PublicName] = true
}

func (s *MemoryStore) RevokeGroupGrant(groupID, publicName string) {
	s.mu.Lock()
	defer s.persistUnlock()
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
	defer s.persistUnlock()
	if s.groupServers[groupID] == nil {
		s.groupServers[groupID] = map[string]bool{}
	}
	s.groupServers[groupID][connectorID] = true
	if s.isPlatformGroupLocked(groupID) {
		delete(s.platformRevoked, "server:"+connectorID)
	}
}

func (s *MemoryStore) RevokeGroupServerGrant(groupID, connectorID string) {
	s.mu.Lock()
	defer s.persistUnlock()
	delete(s.groupServers[groupID], connectorID)
	if s.isPlatformGroupLocked(groupID) {
		s.platformRevoked["server:"+connectorID] = true
	}
}

// RemoveGroupConnectorAccess atomically removes this group's server grant,
// individual tool grants, and matching draft/live rules. Other connectors and
// groups keep their access. Existing governance tombstones remain in place.
func (s *MemoryStore) RemoveGroupConnectorAccess(workspaceID, groupID, connectorID, actor string) bool {
	s.mu.Lock()
	defer s.persistUnlock()
	g, groupOK := s.groups[groupID]
	c, connectorOK := s.connectors[connectorID]
	if !groupOK || !connectorOK || g.WorkspaceID != workspaceID || c.WorkspaceID != workspaceID {
		return false
	}
	delete(s.groupServers[groupID], connectorID)
	if s.isPlatformGroupLocked(groupID) {
		s.platformRevoked["server:"+connectorID] = true
	}
	names := map[string]bool{}
	for name, tool := range s.tools {
		if tool.WorkspaceID == workspaceID && tool.ConnectorID == connectorID {
			names[name] = true
			delete(s.groupGrants[groupID], name)
		}
	}
	ids := map[string]bool{}
	for _, records := range []map[string]access.Package{s.packageDrafts, s.packageLive} {
		for id, p := range records {
			if p.WorkspaceID != workspaceID || p.GroupID != groupID {
				continue
			}
			for _, rule := range p.Rules {
				if names[rule.PublicName] {
					ids[id] = true
					break
				}
			}
		}
	}
	for id := range ids {
		version := s.packageLive[id].Version
		if draft := s.packageDrafts[id]; draft.Version > version {
			version = draft.Version
		}
		version++ // Invalidates any review or publish request made before removal.
		for _, records := range []map[string]access.Package{s.packageDrafts, s.packageLive} {
			p, ok := records[id]
			if !ok {
				continue
			}
			p = access.Clone(p)
			kept := []access.ToolRule{}
			for _, rule := range p.Rules {
				if !names[rule.PublicName] {
					kept = append(kept, rule)
				}
			}
			p.Rules, p.Version = kept, version
			if len(kept) == 0 {
				if p.Status == "draft" {
					delete(records, id)
					continue
				}
				p.Status = "revoked"
			}
			records[id] = p
		}
		s.policyEvents[workspaceID] = append(s.policyEvents[workspaceID], PolicyEvent{At: time.Now().UTC(), Actor: actor, Action: "remove_server_from_group", PackageID: id, Version: version, GroupID: groupID, ConnectorID: connectorID})
	}
	if len(ids) == 0 {
		s.policyEvents[workspaceID] = append(s.policyEvents[workspaceID], PolicyEvent{At: time.Now().UTC(), Actor: actor, Action: "remove_server_from_group", GroupID: groupID, ConnectorID: connectorID})
	}
	if len(s.policyEvents[workspaceID]) > 10000 {
		s.policyEvents[workspaceID] = append([]PolicyEvent(nil), s.policyEvents[workspaceID][len(s.policyEvents[workspaceID])-10000:]...)
	}
	return true
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
	s.UpdateGroup(groupID, &name, nil)
}

// UpdateGroup updates only supplied metadata; descriptions do not affect grants.
func (s *MemoryStore) UpdateGroup(groupID string, name, description *string) {
	s.mu.Lock()
	defer s.persistUnlock()
	if g, ok := s.groups[groupID]; ok {
		if name != nil && !g.BuiltIn {
			g.Name = *name
		}
		if description != nil {
			g.Description = *description
		}
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
	defer s.persistUnlock()
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
	defer s.persistUnlock()
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

func (s *MemoryStore) AppendAudit(e AuditEvent) error {
	if b := s.auditProvider(); b != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return b.Append(ctx, e)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e = cloneAuditEvent(e)
	// Keep the local alpha's in-memory history bounded under repeated calls.
	// Durable, full-history audit storage remains a separate requirement.
	const maxAuditEvents = 50_000
	if len(s.audit) >= maxAuditEvents {
		s.audit[s.auditStart] = e
		s.auditStart = (s.auditStart + 1) % maxAuditEvents
		return nil
	}
	s.audit = append(s.audit, e)
	return nil
}

// auditAt returns the ith oldest event. The caller must hold s.mu.
func (s *MemoryStore) auditAt(i int) AuditEvent {
	return s.audit[(s.auditStart+i)%len(s.audit)]
}

func cloneAuditEvent(e AuditEvent) AuditEvent {
	e.GroupIDs = append([]string(nil), e.GroupIDs...)
	e.Input = append(json.RawMessage(nil), e.Input...)
	e.Output = append(json.RawMessage(nil), e.Output...)
	return e
}

func (s *MemoryStore) ListAudit() []AuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]AuditEvent, len(s.audit))
	for i := range out {
		out[i] = cloneAuditEvent(s.auditAt(i))
	}
	return out
}

func (s *MemoryStore) AuditCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.audit)
}

// QueryAudit filters immutable event metadata inside one workspace. Results
// are newest first so a limited view shows the most recent matching calls.
func (s *MemoryStore) QueryAudit(f AuditFilter) []AuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []AuditEvent{}
	for i := len(s.audit) - 1; i >= 0; i-- {
		e := s.auditAt(i)
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
		out = append(out, cloneAuditEvent(e))
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

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
