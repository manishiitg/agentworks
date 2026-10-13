package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
)

type costsQuery struct {
	Action, GroupBy, From, To                                                      string
	ProductKind, ProductID, ProductOwnerID, Activity, PersonID, Model, DashboardID string
	Top, Limit                                                                     int
	Cursor                                                                         string
}

type costsProduct struct {
	Kind    string `json:"kind"`
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	OwnerID string `json:"owner_id,omitempty"`
}

type costsPerson struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
}

type costsUsage struct {
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	ReasoningTokens  int     `json:"reasoning_tokens,omitempty"`
	CacheReadTokens  int     `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int     `json:"cache_write_tokens,omitempty"`
	CostUSD          float64 `json:"cost_usd"`
	ActualUSD        float64 `json:"provider_actual_usd,omitempty"`
	EstimateUSD      float64 `json:"estimated_usd,omitempty"`
	ShadowUSD        float64 `json:"subscription_shadow_usd,omitempty"`
	Calls            int     `json:"llm_calls"`
	Unpriced         int     `json:"unpriced_calls,omitempty"`
	MissingUsage     int     `json:"missing_usage_calls,omitempty"`
	GenerationMS     int64   `json:"llm_generation_duration_ms,omitempty"`
}

func costsUsageOf(a costledger.Aggregate) costsUsage {
	return costsUsage{a.InputTokens, a.CompletionTokens, a.ReasoningTokens,
		a.CacheReadTokens, a.CacheWriteTokens, a.TotalCostUSD,
		a.ProviderActualCostUSD, a.TokenEstimateCostUSD, a.SubscriptionShadowUSD,
		a.CallCount, a.UnpricedCallCount, a.MissingUsageCallCount, a.LLMGenerationDurationMS}
}

type costsRow struct {
	ID            string            `json:"id"`
	Label         string            `json:"label"`
	Product       *costsProduct     `json:"product,omitempty"`
	Person        *costsPerson      `json:"person,omitempty"`
	Activity      string            `json:"activity,omitempty"`
	AccountKind   string            `json:"account_kind,omitempty"`
	Models        []string          `json:"models,omitempty"`
	DashboardID   string            `json:"origin_dashboard_id,omitempty"`
	DashboardName string            `json:"origin_dashboard_name,omitempty"`
	FirstAt       time.Time         `json:"first_seen,omitempty"`
	LastAt        time.Time         `json:"last_seen,omitempty"`
	References    map[string]string `json:"link_ids,omitempty"`
	Usage         costsUsage        `json:"usage"`
	aggregate     costledger.Aggregate
	models        map[string]bool
}

type costsResponse struct {
	From       string      `json:"from"`
	To         string      `json:"to"`
	GroupBy    string      `json:"group_by,omitempty"`
	Total      costsUsage  `json:"total"`
	Rows       []*costsRow `json:"rows"`
	Omitted    *costsUsage `json:"omitted,omitempty"`
	NextCursor string      `json:"next_cursor,omitempty"`
	Coverage   struct {
		Source             string `json:"source"`
		UnattributedEvents int    `json:"unattributed_events"`
		MappedProducts     int    `json:"historically_mapped_events,omitempty"`
	} `json:"coverage"`
}

func costsKey(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}

func normalizeCostsQuery(q costsQuery, now time.Time) (costsQuery, error) {
	if q.Action == "" {
		q.Action = "breakdown"
	}
	if q.Action != "breakdown" && q.Action != "detail" {
		return q, fmt.Errorf("action must be breakdown or detail")
	}
	if q.GroupBy == "" {
		q.GroupBy = "product"
	}
	switch q.GroupBy {
	case "product", "activity", "person", "model", "day":
	default:
		return q, fmt.Errorf("invalid group_by")
	}
	if q.To == "" {
		q.To = now.UTC().Format("2006-01-02")
	}
	if q.From == "" {
		q.From = now.UTC().AddDate(0, 0, -29).Format("2006-01-02")
	}
	from, e1 := time.Parse("2006-01-02", q.From)
	to, e2 := time.Parse("2006-01-02", q.To)
	if e1 != nil || e2 != nil || to.Before(from) || to.Sub(from) >= 366*24*time.Hour {
		return q, fmt.Errorf("from/to must be inclusive UTC dates spanning at most 366 days")
	}
	if q.Top == 0 {
		q.Top = 10
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Top < 1 || q.Top > 100 || q.Limit < 1 || q.Limit > 100 {
		return q, fmt.Errorf("top and limit must be between 1 and 100")
	}
	if q.Action == "detail" && q.ProductID == "" && q.ProductKind == "" && q.Activity == "" && q.PersonID == "" && q.DashboardID == "" {
		return q, fmt.Errorf("detail requires a product, activity, person or dashboard filter from breakdown")
	}
	return q, nil
}

func costsScope(c *UserClaims, scope string) bool {
	return c.AccessToken == nil || c.AccessToken.Allows(scope)
}

func externalCostsAllowed(c *UserClaims) bool {
	if c == nil || c.UserID == "" || c.Scope != "" || directoryUserIsDisabled(c) {
		return false
	}
	if c.AccessToken != nil && c.AccessToken.BoundAgentID != "" {
		return false
	}
	for _, scope := range []string{"workflows:read", "crews:read", "code:run", "code:review", "knowledgebase:read", "vault:read", "dashboards:read", "llm:use", "users:manage"} {
		if costsScope(c, scope) {
			return true
		}
	}
	return false
}

// Authorize current product identity, not the actor's ownership of a past row.
// A shared product exposes every person's usage; personal activity stays with
// its recorded actor. A restricted admin connection retains its consent bounds.
func (api *StreamingAPI) costsProductVisible(ctx context.Context, c *UserClaims, p costProductBinding, e costledger.Entry) bool {
	if claimsIsAdmin(c) && costsScope(c, "users:manage") {
		return true
	}
	if p.ID == "" {
		actor := firstNonEmptyTrimmed(e.ActorID, e.UserID)
		return actor == c.UserID && costsScope(c, "llm:use")
	}
	product, scope := "", ""
	switch p.Kind {
	case "workflow":
		product, scope = "agentworks", "workflows:read"
	case "relay":
		product, scope = "relays", "workflows:read"
	case "crew":
		product, scope = "work", "crews:read"
	case "code":
		// Personal usage survives deletion of the person's private project;
		// a recorded caller on someone else's project does not grant access.
		if e.ActorID == c.UserID && p.OwnerID != "" && sanitizeUserIDForPath(p.OwnerID) == sanitizeUserIDForPath(c.UserID) && (costsScope(c, "code:run") || costsScope(c, "llm:use")) {
			return true
		}
		product, scope = "code", "code:run"
	case "vault":
		return productpolicy.Enabled("mcp-gateway") && userAllowedProduct(c, "mcp-gateway") && (costsScope(c, "vault:read") || costsScope(c, "vault:manage")) && (vaultClaimsLevel(c) == vaultRead || vaultClaimsLevel(c) == vaultManage)
	case "brain":
		return knowledgebaseMCPAllowed(c) && costsScope(c, "knowledgebase:read")
	default:
		return p.OwnerID != "" && sanitizeUserIDForPath(p.OwnerID) == sanitizeUserIDForPath(c.UserID) &&
			productpolicy.Enabled(p.Kind) && userAllowedProduct(c, p.Kind) && costsScope(c, "llm:use")
	}
	if !productpolicy.Enabled(product) || !userAllowedProduct(c, product) {
		return false
	}
	if p.Kind == "code" && claimsIsAdmin(c) && costsScope(c, "code:review") {
		return true
	}
	if !costsScope(c, scope) || p.Root == "" {
		return false
	}
	target, err := api.dashboardTarget(ctx, c, p.Root)
	return err == nil && target.Kind == p.Kind && target.ID == p.ID
}

type costsCursor struct {
	Before time.Time `json:"before"`
	Key    string    `json:"key"`
	Query  string    `json:"query"`
}

func costsQueryHash(q costsQuery, c *UserClaims) string {
	q.Cursor, q.Top, q.Limit = "", 0, 0
	b, _ := json.Marshal(q)
	grant := ""
	if c.AccessToken != nil {
		grant = c.AccessToken.ID
	}
	return costsKey(c.UserID, grant, string(b))
}

// One event contributes once to the visible total and exactly one grouping
// bucket. Origins are filters/annotations on target rows, never extra events.
func (api *StreamingAPI) queryCosts(ctx context.Context, c *UserClaims, q costsQuery) (*costsResponse, error) {
	if !externalCostsAllowed(c) {
		return nil, fmt.Errorf("cost access unavailable")
	}
	if api.costLedger == nil {
		return nil, fmt.Errorf("cost ledger unavailable")
	}
	var err error
	q, err = normalizeCostsQuery(q, time.Now())
	if err != nil {
		return nil, err
	}
	var cursor costsCursor
	hash := costsQueryHash(q, c)
	if q.Cursor != "" {
		if len(q.Cursor) > 1024 {
			return nil, fmt.Errorf("invalid cursor")
		}
		b, decodeErr := base64.RawURLEncoding.DecodeString(q.Cursor)
		if decodeErr != nil || json.Unmarshal(b, &cursor) != nil || cursor.Query != hash || cursor.Before.IsZero() || cursor.Key == "" {
			return nil, fmt.Errorf("cursor does not match this query")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out := &costsResponse{From: q.From, To: q.To, Rows: []*costsRow{}}
	out.Coverage.Source = "platform_cost_events"
	if q.Action == "breakdown" {
		out.GroupBy = q.GroupBy
	}
	bindings := map[string]costProductBinding{}
	permissions := map[string]bool{}
	people := map[string]string{}
	groups := map[string]*costsRow{}
	var total costledger.Aggregate
	visited := 0
	err = api.costLedger.VisitEntries(ctx, q.From, q.To, func(e costledger.Entry) error {
		visited++
		if visited > 200000 {
			return fmt.Errorf("cost query is too large; narrow the date range")
		}
		// Resolve reliable historical identities once per workspace. Recorded
		// snapshots survive renames, but access still needs a current binding.
		binding, seen := bindings[e.WorkflowID]
		if !seen {
			binding = costProductForWorkspace(ctx, e.WorkflowID)
			bindings[e.WorkflowID] = binding
		}
		p, mapped := binding, false
		if e.ProductID != "" {
			p = costProductBinding{e.ProductKind, e.ProductID, e.ProductName, "", e.ProductOwnerID}
			if binding.Kind == p.Kind && binding.ID == p.ID && (p.Kind != "code" || p.OwnerID == "" || binding.OwnerID == p.OwnerID) {
				p.Root = binding.Root
			}
		} else {
			mapped = p.ID != ""
		}
		pk := costsKey(p.Kind, p.ID, p.OwnerID, e.WorkflowID)
		// Personal/unmapped events require their own actor-specific decision.
		if p.ID == "" || p.Kind == "code" {
			pk = costsKey(pk, firstNonEmptyTrimmed(e.ActorID, e.UserID), e.ActorID)
		}
		allowed, checked := permissions[pk]
		if !checked {
			allowed = api.costsProductVisible(ctx, c, p, e)
			permissions[pk] = allowed
		}
		if !allowed {
			return nil
		}
		actor := e.ActorID
		activity := e.Activity
		if activity == "" {
			activity = "unattributed"
		}
		model := firstNonEmptyTrimmed(e.EffectiveModelID, e.ModelID, "unattributed")
		provider := firstNonEmptyTrimmed(e.EffectiveProvider, e.Provider)
		modelKey := model
		if provider != "" {
			modelKey = provider + ":" + model
		}
		filterKind := p.Kind
		if p.ID == "" {
			filterKind = "unattributed"
		}
		if q.ProductID != "" && p.ID != q.ProductID || q.ProductKind != "" && filterKind != q.ProductKind ||
			q.ProductOwnerID != "" && p.OwnerID != q.ProductOwnerID || q.Activity != "" && activity != q.Activity ||
			q.PersonID != "" && firstNonEmptyTrimmed(actor, "unattributed") != q.PersonID || q.Model != "" && q.Model != modelKey && q.Model != model ||
			q.DashboardID != "" && e.OriginDashboardID != q.DashboardID {
			return nil
		}
		total.AddEntry(e)
		if p.ID == "" || actor == "" || activity == "unattributed" {
			out.Coverage.UnattributedEvents++
		}
		if mapped {
			out.Coverage.MappedProducts++
		}
		personName := "Unattributed"
		if actor != "" {
			personName, seen = people[actor]
			if !seen {
				personName = actor
				if record := directoryUserFor(actor, "", ""); record != nil {
					personName = firstNonEmptyTrimmed(record.Username, actor)
				}
				people[actor] = personName
			}
		}
		product := costsProduct{p.Kind, p.ID, firstNonEmptyTrimmed(p.Name, p.ID, "Unattributed"), p.OwnerID}
		if p.ID == "" {
			product.Kind, product.Name = "unattributed", "Unattributed"
		}
		person := costsPerson{firstNonEmptyTrimmed(actor, "unattributed"), personName}
		key, label := "", ""
		if q.Action == "detail" {
			key = costsKey(p.Kind, p.ID, p.OwnerID, actor, activity, e.SessionID, firstNonEmptyTrimmed(e.ExecutionID, e.RunID, e.EventID))
			label = activity
		} else {
			switch q.GroupBy {
			case "product":
				owner := p.OwnerID
				if p.Kind == "workflow" || p.Kind == "relay" || p.Kind == "crew" || p.Kind == "brain" {
					owner = ""
				}
				key, label = costsKey(p.Kind, p.ID, owner), product.Name
			case "activity":
				key, label = activity, activity
			case "person":
				key, label = firstNonEmptyTrimmed(actor, "unattributed"), personName
			case "model":
				key, label = modelKey, modelKey
			case "day":
				key, label = e.Timestamp.UTC().Format("2006-01-02"), e.Timestamp.UTC().Format("2006-01-02")
			}
		}
		row := groups[key]
		if row == nil {
			row = &costsRow{ID: key, Label: label, FirstAt: e.Timestamp, LastAt: e.Timestamp, models: map[string]bool{}}
			if q.GroupBy == "product" || q.Action == "detail" {
				row.Product = &product
			}
			if q.GroupBy == "person" || q.Action == "detail" {
				row.Person = &person
			}
			if q.GroupBy == "activity" || q.Action == "detail" {
				row.Activity = activity
			}
			if q.Action == "detail" {
				row.DashboardID, row.DashboardName = e.OriginDashboardID, e.OriginDashboardName
				row.References = map[string]string{}
				if e.FunctionCallID != "" {
					row.References["call_id"] = e.FunctionCallID
				}
				if e.RunID != "" {
					row.References["run_id"] = e.RunID
				}
				// Numeric usage is shared product evidence; personal conversation
				// identifiers remain with their actor. Never include prompts/paths.
				if actor == c.UserID || activity != "person_chat" && activity != "builder" && activity != "unattributed" {
					if e.SessionID != "" {
						row.References["session_id"] = e.SessionID
					}
					if e.ExecutionID != "" {
						row.References["execution_id"] = e.ExecutionID
					}
				}
			}
			groups[key] = row
		}
		row.aggregate.AddEntry(e)
		if e.Timestamp.Before(row.FirstAt) {
			row.FirstAt = e.Timestamp
		}
		if e.Timestamp.After(row.LastAt) {
			row.LastAt = e.Timestamp
		}
		if q.Action == "detail" {
			row.models[modelKey] = true
			account := "unrecorded"
			if strings.HasPrefix(e.AccountID, "global:") {
				account = "server"
			} else if e.AccountID != "" {
				account = "personal"
			}
			if row.AccountKind == "" {
				row.AccountKind = account
			} else if row.AccountKind != account {
				row.AccountKind = "mixed"
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out.Total = costsUsageOf(total)
	rows := make([]*costsRow, 0, len(groups))
	for _, row := range groups {
		row.Usage = costsUsageOf(row.aggregate)
		for model := range row.models {
			row.Models = append(row.Models, model)
		}
		sort.Strings(row.Models)
		rows = append(rows, row)
	}
	if q.Action == "detail" {
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].LastAt.Equal(rows[j].LastAt) {
				return rows[i].ID < rows[j].ID
			}
			return rows[i].LastAt.After(rows[j].LastAt)
		})
		for _, row := range rows {
			if !cursor.Before.IsZero() && (row.LastAt.After(cursor.Before) || row.LastAt.Equal(cursor.Before) && row.ID <= cursor.Key) {
				continue
			}
			if len(out.Rows) == q.Limit {
				last := out.Rows[len(out.Rows)-1]
				b, _ := json.Marshal(costsCursor{last.LastAt, last.ID, hash})
				out.NextCursor = base64.RawURLEncoding.EncodeToString(b)
				break
			}
			out.Rows = append(out.Rows, row)
		}
	} else {
		sort.Slice(rows, func(i, j int) bool {
			a, b := rows[i].Usage, rows[j].Usage
			at, bt := a.InputTokens+a.OutputTokens, b.InputTokens+b.OutputTokens
			if at != bt {
				return at > bt
			}
			if a.CostUSD != b.CostUSD {
				return a.CostUSD > b.CostUSD
			}
			return rows[i].ID < rows[j].ID
		})
		var omitted costledger.Aggregate
		for i, row := range rows {
			if i < q.Top {
				out.Rows = append(out.Rows, row)
			} else {
				omitted.Merge(row.aggregate)
			}
		}
		if len(rows) > q.Top {
			usage := costsUsageOf(omitted)
			out.Omitted = &usage
		}
	}
	return out, nil
}
