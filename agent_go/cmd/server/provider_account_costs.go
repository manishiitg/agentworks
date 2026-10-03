package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

// Cost per provider and account (docs/design/provider_accounts.md, "Usage
// and cost per provider and account"). Every ledger entry records the
// account its turn ran on; entries written before that show as
// "unrecorded account" under their provider.
//
// Visibility: admins see everything; the owner of a user account sees its
// full split (who used it and where); everyone else sees only their own
// share.

type providerAccountCostSplit struct {
	WorkID   string `json:"work_id"`
	WorkKind string `json:"work_kind"`
	WorkName string `json:"work_name"`
	UserID   string `json:"user_id"`
	UserName string `json:"user_name"`
	costledger.Aggregate
}

type providerAccountCost struct {
	AccountID string `json:"account_id"`
	Name      string `json:"name"`
	// Kind is server, user or unrecorded.
	Kind      string `json:"kind"`
	OwnerName string `json:"owner_name,omitempty"`
	// FullSplit reports that the caller sees everyone's use of the account;
	// false means the split is the caller's own share only.
	FullSplit bool                        `json:"full_split"`
	Total     costledger.Aggregate        `json:"total"`
	Split     []*providerAccountCostSplit `json:"split"`
}

type providerCost struct {
	Provider string                 `json:"provider"`
	Total    costledger.Aggregate   `json:"total"`
	Accounts []*providerAccountCost `json:"accounts"`
}

type providerAccountCostsResponse struct {
	From      string          `json:"from,omitempty"`
	To        string          `json:"to,omitempty"`
	Providers []*providerCost `json:"providers"`
}

// buildProviderAccountCosts folds a ledger summary into provider → account
// → (work, person) rows the viewer may see. includedWork optionally limits
// rows to the scope of an enclosing report; workVisible only masks names.
func buildProviderAccountCosts(summary *costledger.Summary, viewer string, admin bool, accounts map[string]storedProviderConnection, workVisible, includedWork func(id, kind string) bool) *providerAccountCostsResponse {
	resp := &providerAccountCostsResponse{Providers: []*providerCost{}}
	if summary == nil {
		return resp
	}
	resp.From, resp.To = summary.From, summary.To
	providers := map[string]*providerCost{}
	type accountKey struct{ provider, account string }
	rows := map[accountKey]*providerAccountCost{}
	splits := map[accountKey]map[string]*providerAccountCostSplit{}
	for key, aggregate := range summary.ByAccountSplit {
		if aggregate == nil {
			continue
		}
		workID, workKind, workName, _ := costOverviewRoot(key.WorkflowID)
		if includedWork != nil && !includedWork(workID, workKind) {
			continue
		}
		record, isUserAccount := accounts[key.AccountID]
		owner := isUserAccount && record.OwnerUserID == viewer
		fullSplit := admin || owner
		if !fullSplit && key.UserID != viewer {
			continue
		}
		pk := accountKey{key.Provider, key.AccountID}
		row := rows[pk]
		if row == nil {
			row = &providerAccountCost{AccountID: key.AccountID, FullSplit: fullSplit, Split: []*providerAccountCostSplit{}}
			switch {
			case key.AccountID == "":
				row.Kind, row.Name = "unrecorded", "Unrecorded account"
			case strings.HasPrefix(key.AccountID, "global:"):
				row.Kind, row.Name = "server", adminManagedProviderAccountName
			case isUserAccount:
				row.Kind, row.Name, row.OwnerName = "user", record.DisplayName, logUsernameForUserID(record.OwnerUserID)
				if record.Removed {
					if fullSplit {
						row.Name += " (removed)"
					} else {
						row.Name = "Removed account"
					}
				}
			default:
				row.Kind, row.Name = "user", "Removed account"
			}
			rows[pk] = row
			splits[pk] = map[string]*providerAccountCostSplit{}
		}
		row.Total.Merge(*aggregate)
		// An account owner sees who used the account, but never the name of
		// a workflow, Crew or Code they cannot open themselves.
		if !admin && workKind != costOverviewKindOther && (workVisible == nil || !workVisible(workID, workKind)) {
			workID, workName = "hidden:"+workKind, hiddenWorkName(workKind)
		}
		splitKey := workID + "\x00" + key.UserID
		split := splits[pk][splitKey]
		if split == nil {
			split = &providerAccountCostSplit{WorkID: workID, WorkKind: workKind, WorkName: workName, UserID: key.UserID, UserName: costOverviewUserName(key.UserID)}
			splits[pk][splitKey] = split
			row.Split = append(row.Split, split)
		}
		split.Aggregate.Merge(*aggregate)
		provider := providers[key.Provider]
		if provider == nil {
			provider = &providerCost{Provider: key.Provider, Accounts: []*providerAccountCost{}}
			providers[key.Provider] = provider
			resp.Providers = append(resp.Providers, provider)
		}
		provider.Total.Merge(*aggregate)
	}
	for key, row := range rows {
		sort.Slice(row.Split, func(i, j int) bool {
			if row.Split[i].TotalCostUSD != row.Split[j].TotalCostUSD {
				return row.Split[i].TotalCostUSD > row.Split[j].TotalCostUSD
			}
			return row.Split[i].WorkID+row.Split[i].UserID < row.Split[j].WorkID+row.Split[j].UserID
		})
		providers[key.provider].Accounts = append(providers[key.provider].Accounts, row)
	}
	for _, provider := range resp.Providers {
		sort.Slice(provider.Accounts, func(i, j int) bool {
			if provider.Accounts[i].Total.TotalCostUSD != provider.Accounts[j].Total.TotalCostUSD {
				return provider.Accounts[i].Total.TotalCostUSD > provider.Accounts[j].Total.TotalCostUSD
			}
			return provider.Accounts[i].AccountID < provider.Accounts[j].AccountID
		})
	}
	sort.Slice(resp.Providers, func(i, j int) bool { return resp.Providers[i].Provider < resp.Providers[j].Provider })
	return resp
}

func hiddenWorkName(kind string) string {
	switch kind {
	case costOverviewKindWorkflow:
		return "a workflow you can't see"
	case costOverviewKindCrew:
		return "a Crew you can't see"
	default:
		return "work you can't see"
	}
}

// costWorkVisibleTo reports whether the caller may open a cost row's work:
// a workflow they have access to, a Crew they can read, or their own (or,
// for reviewers, any) Code / product project.
func costWorkVisibleTo(r *http.Request) func(id, kind string) bool {
	claims := GetUserFromContext(r.Context())
	userID := GetUserIDFromContext(r.Context())
	admin, reviewer := currentUserIsAdmin(r), currentUserCanReviewCode(r)
	return func(id, kind string) bool {
		switch kind {
		case costOverviewKindCrew:
			ref, ok := resolveCrewPath(r.Context(), userID, id)
			return ok && crewAccessFor(claims, ref) != crewAccessNone
		case costOverviewKindProduct:
			return costOverviewProductVisibleTo(id, userID, admin, reviewer)
		case costOverviewKindWorkflow:
			return currentUserWorkflowAccess(r, id) != WorkflowAccessNone
		}
		return admin
	}
}

func providerAccountsByID(ctx context.Context) (map[string]storedProviderConnection, error) {
	providerConnectionsMu.Lock()
	defer providerConnectionsMu.Unlock()
	records, err := loadProviderConnections(ctx)
	if err != nil {
		return nil, err
	}
	history, err := loadProviderConnectionHistory(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]storedProviderConnection, len(records)+len(history))
	for _, record := range history {
		byID[record.ID] = storedProviderConnection{ProviderConnection: ProviderConnection{
			ID: record.ID, Provider: record.Provider, DisplayName: record.DisplayName,
			OwnerUserID: record.OwnerUserID,
		}, Removed: true}
	}
	for _, record := range records {
		record.Credential = ""
		byID[record.ID] = record
	}
	return byID, nil
}

// GET /api/provider-accounts/costs?from=&to= — cost and tokens per provider
// and account, split by workflow, Crew, Code and person.
func (api *StreamingAPI) handleProviderAccountCosts(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	userID, ok := requireSignedIn(w, r)
	if !ok {
		return
	}
	if !currentUserCanReviewCode(r) {
		writeWorkflowPermissionDenied(w, "admin or Code reviewer")
		return
	}
	if api.costLedger == nil {
		http.Error(w, `{"error":"cost ledger not initialized"}`, http.StatusServiceUnavailable)
		return
	}
	summary, err := api.costLedger.Summarize(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	accounts, err := providerAccountsByID(r.Context())
	if err != nil {
		http.Error(w, "cannot load provider accounts", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(buildProviderAccountCosts(summary, userID, currentUserIsAdmin(r), accounts, costWorkVisibleTo(r), nil))
}
