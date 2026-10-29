package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
)

// The cost observer itself lives in pkg/costobserver so the workflow
// orchestrator can attach the same implementation to the agents it creates.
// These aliases keep package server's call sites (and its cost e2e suites)
// pointing at that single implementation.
type costObserver = costobserver.Observer

func newCostObserver(ledger *costledger.Ledger, sessionID, userID, agentMode string, opts ...costobserver.Option) *costObserver {
	return costobserver.New(ledger, sessionID, userID, agentMode, opts...)
}

var (
	withCostModel                = costobserver.WithModel
	withCostAttribution          = costobserver.WithAttribution
	withCostSourcePlatform       = costobserver.WithSourcePlatform
	withCostAccount              = costobserver.WithAccount
	withCostBillingUser          = costobserver.WithBillingUser
	costAccountIDFor             = costobserver.AccountIDFor
	inferCostScope               = costobserver.InferScope
	scopeForAutomationSource     = costobserver.ScopeForAutomationSource
	costFirstNonEmpty            = costobserver.FirstNonEmpty
	extractCacheTokens           = costobserver.ExtractCacheTokens
	extractCostAndEffectiveModel = costobserver.ExtractCostAndEffectiveModel
	toInt                        = costobserver.ToInt
)

func chatTurnCostScope(req QueryRequest, agentModeForScope, phaseID string) string {

	if scope := scopeForAutomationSource(req.CostSourceID); scope != "" {
		return scope
	}
	return inferCostScope(agentModeForScope, phaseID)
}

// costSourceOption carries a product schedule turn's source onto its ledger
// entries (PLAT-702); a person's own turn names none.
func costSourceOption(req QueryRequest) costobserver.Option {
	return costobserver.WithSource(req.CostSourceID, req.CostSourceLabel, req.CostSourceRunID)
}

func costSourceContext(ctx context.Context, req QueryRequest) context.Context {
	return costobserver.ContextWithSource(ctx, costobserver.Source{
		ID: req.CostSourceID, Label: req.CostSourceLabel, RunID: req.CostSourceRunID,
	})
}

// handleCostSummary is the HTTP handler for GET /api/cost/summary.
// This unfiltered server-wide summary is admin-only; Code reviewers use the
// scoped /api/cost/overview endpoint. Optional `from` and `to` query params
// (YYYY-MM-DD, UTC) bound the date range.
func (api *StreamingAPI) handleCostSummary(w http.ResponseWriter, r *http.Request) {
	if !currentUserIsAdmin(r) {
		writeWorkflowPermissionDenied(w, "admin")
		return
	}
	if api.costLedger == nil {
		http.Error(w, `{"error":"cost ledger not initialized"}`, http.StatusServiceUnavailable)
		return
	}
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	summary, err := api.costLedger.Summarize(from, to)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summary)
}
