// Package productapi defines the versioned host contract for separately built server products.
package productapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

const APIVersion = 1

var ErrRevisionConflict = errors.New("product state changed; refresh and try again")

type User struct {
	ID          string
	WorkspaceID string
	Email       string
}

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

type AuditEvent struct {
	LLMCost         *LLMCost `json:",omitempty"`
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
	Phase           string `json:",omitempty"`
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
	Kind        string // "llm" or "mcp"; empty includes both.
	Decision    string
	Outcome     string
	After       time.Time
	Before      time.Time
	Limit       int
}

type AuditSummary struct {
	Costs          *LLMCostSummary `json:"costs,omitempty"`
	Total          int
	Allowed        int
	Denied         int
	UpstreamErrors int
	AvgDurationMs  int64
	ByDay          []UsageBucket
	ByTool         []UsageBucket
}

type UsageBucket struct {
	Key            string
	Count          int
	Denied         int
	UpstreamErrors int
}

type LLMCost struct {
	Status         string        `json:"status"`
	AmountNanoUSD  int64         `json:"amount_nano_usd"`
	PricingVersion string        `json:"pricing_version,omitempty"`
	Source         string        `json:"source,omitempty"`
	Reason         string        `json:"reason,omitempty"`
	Usage          LLMTokenUsage `json:"usage"`
	Rates          *LLMRates     `json:"rates,omitempty"`
	Multiplier     float64       `json:"multiplier,omitempty"`
	UsageSource    string        `json:"usage_source,omitempty"`
	EstimateMethod string        `json:"estimate_method,omitempty"`
}

type LLMTokenUsage struct {
	Input        int64 `json:"input_tokens"`  // uncached input only
	Output       int64 `json:"output_tokens"` // already includes reasoning
	CacheRead    int64 `json:"cache_read_tokens"`
	CacheWrite5m int64 `json:"cache_write_5m_tokens"`
	CacheWrite1h int64 `json:"cache_write_1h_tokens"`
}

type LLMRates struct {
	Input        float64  `json:"input_per_million"`
	Output       float64  `json:"output_per_million"`
	CacheRead    *float64 `json:"cache_read_per_million,omitempty"`
	CacheWrite5m *float64 `json:"cache_write_5m_per_million,omitempty"`
	CacheWrite1h *float64 `json:"cache_write_1h_per_million,omitempty"`
}

type LLMCostBucket struct {
	Key                      string `json:"key"`
	Requests                 int64  `json:"requests"`
	PricedRequests           int64  `json:"priced_requests"`
	UnknownRequests          int64  `json:"unknown_requests"`
	UnbilledRequests         int64  `json:"unbilled_requests"`
	AmountNanoUSD            int64  `json:"amount_nano_usd"`
	InputTokens              int64  `json:"input_tokens"`
	OutputTokens             int64  `json:"output_tokens"`
	InterruptedRequests      int64  `json:"interrupted_requests"`
	InterruptedAmountNanoUSD int64  `json:"interrupted_amount_nano_usd"`
}

type LLMCostSummary struct {
	LLMCostBucket
	ByUser  []LLMCostBucket `json:"by_user"`
	ByModel []LLMCostBucket `json:"by_model"`
}

type LLMModelAccess struct {
	ModelID  string   `json:"model_id"`
	UserIDs  []string `json:"user_ids"`
	GroupIDs []string `json:"group_ids"`
	// Explicit per-user revocation takes precedence over legacy group grants.
	DeniedUserIDs []string `json:"denied_user_ids,omitempty"`
	Revision      int      `json:"revision"`
}

type Identity struct {
	UserID      string
	WorkspaceID string
	Email       string
	ClientID    string // OAuth client or gateway API key identity
	// ViaGroup is set when the caller authenticated with a group API key:
	// only that group's grants apply, never the key's own (nonexistent) user.
	ViaGroup string
}

const (
	DecisionAllow        = "allow"
	DecisionDeny         = "deny"
	OutcomeOK            = "ok"
	OutcomeDenied        = "denied"
	OutcomeUpstreamError = "upstream_error"
	OutcomeInterrupted   = "interrupted"
)

// Storage belongs to the host. Implementations must persist admission before
// returning success; completion updates the same request ID, never retries inference.
type Storage interface {
	GetUser(string) (User, bool)
	GetGroup(string) (Group, bool)
	GroupsOf(string) []string
	ListUsers(string) []User
	GetLLMModelAccess(string, string) (LLMModelAccess, bool)
	PersistenceError() error
	BeginAudit(AuditEvent) error
	AppendAudit(AuditEvent) error
}
type AccessWriter interface {
	SetLLMModelAccessWithDenials(string, string, []string, []string, []string, int, string) (LLMModelAccess, error)
}

// AuditReader queries MCP and product records. Query applies Limit after filters
// and phase coalescing. Summary ignores Limit and aggregates all matching logical
// requests; unfinished admissions have no completed latency or cost observation.
type AuditReader interface {
	Query(context.Context, AuditFilter) ([]AuditEvent, error)
	Summary(context.Context, AuditFilter) (AuditSummary, error)
}
type Authenticate func(*http.Request) (Identity, bool)

// Host authenticates the current person on every request. IsAdmin must consult
// current platform roles; callers cannot establish identity with actor headers.
type Host struct {
	APIVersion   int
	Storage      Storage
	Audit        AuditReader
	Authenticate Authenticate
	IsAdmin      func(context.Context, Identity) bool
}
type Product struct {
	ID      string
	Label   string
	Handler http.Handler
}
type Factory func(Host) (Product, error)
