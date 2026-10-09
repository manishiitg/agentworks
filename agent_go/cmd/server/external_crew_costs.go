package server

import (
	"net/http"
	"sort"
	"time"
)

func externalCrewCostsDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("get_crew_costs", "What a Crew you own has spent, from the same ledger as the app's Costs and usage tab: total cost, tokens and calls, per day, by what did the work (main chat, schedules, function calls, bots) and by model, for the last `days` days (default 30, at most 90; pass `before` YYYY-MM-DD to page back). Omit crew_id for one row per Crew you own, most expensive first. Only a Crew's owner reads its costs.", false, false, map[string]any{
		"crew_id": externalString("Crew ID from crew action=list. Omit for every Crew you own."),
		"days":    externalInteger(1, 90),
		"before":  externalString("Only spend before this day (YYYY-MM-DD, UTC), to page back from a previous window's next_before."),
	})
}

// externalCrewCosts serves get_crew_costs. It reads the per-workspace summary
// the app's Costs tab reads, and only for Crews the caller owns: a shared
// Crew's spend includes other people's use of it.
func (api *StreamingAPI) externalCrewCosts(w http.ResponseWriter, r *http.Request, args map[string]any) {
	ctx := r.Context()
	claims := GetUserFromContext(ctx)
	days := externalInt(args, "days", 30)
	before := externalArg(args, "before")
	crewID := externalArg(args, "crew_id")
	bounded := func(id string) bool { return claims.AccessToken == nil || claims.AccessToken.AllowsCrew(id) }

	load := func(id string) (map[string]any, *workflowCostsResponse, bool) {
		crew, _, summary, ok := api.externalCrewResolve(ctx, claims, id)
		if !ok || !bounded(id) {
			return nil, nil, false
		}
		if !crew.OwnedByCaller {
			return map[string]any{"owned": false}, nil, true
		}
		response, err := loadWorkflowCostSummary(costWorkspacePathForUser(crew.Binding.WorkspacePath, crew.OwnerID), days, before, time.Now())
		if err != nil {
			return map[string]any{"error": err.Error()}, nil, true
		}
		return map[string]any{"crew_id": id, "name": summary["name"], "owned": true}, &response, true
	}

	if crewID != "" {
		head, response, found := load(crewID)
		switch {
		case !found:
			externalError(w, 404, "not_found", "Crew not found or not allowed for this connection.")
		case head["error"] != nil:
			externalError(w, 400, "invalid_arguments", head["error"].(string))
		case head["owned"] == false:
			externalError(w, 403, "forbidden", "Only a Crew's owner reads its costs.")
		default:
			out := head
			if summary := response.ScopedCosts; summary != nil {
				out["total"], out["by_date"], out["by_model"] = summary.Total, summary.ByDate, summary.ByModel
				out["by_scope"], out["by_source"] = summary.ByScope, summary.BySource
			}
			if response.History != nil {
				out["window"] = map[string]any{"from": response.History.WindowFrom, "to": response.History.WindowTo, "has_more": response.History.HasMore, "next_before": response.History.NextBefore}
			}
			externalJSON(w, out)
		}
		return
	}

	crews, err := api.externalCrewsVisible(ctx, claims, "")
	if err != nil {
		externalError(w, 502, "workspace_unavailable", "Cannot list Crews.")
		return
	}
	rows := []map[string]any{}
	var total float64
	var window any
	for _, item := range crews {
		id, _ := item["id"].(string)
		if id == "" {
			continue
		}
		head, response, found := load(id)
		if !found || head["owned"] != true || response == nil {
			continue
		}
		row := map[string]any{"crew_id": id, "name": head["name"], "total_cost_usd": 0.0, "call_count": 0, "input_tokens": 0, "completion_tokens": 0}
		if summary := response.ScopedCosts; summary != nil {
			row["total_cost_usd"], row["call_count"] = summary.Total.TotalCostUSD, summary.Total.CallCount
			row["input_tokens"], row["completion_tokens"] = summary.Total.InputTokens, summary.Total.CompletionTokens
			total += summary.Total.TotalCostUSD
		}
		if response.History != nil && window == nil {
			window = map[string]any{"from": response.History.WindowFrom, "to": response.History.WindowTo}
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i]["total_cost_usd"].(float64) > rows[j]["total_cost_usd"].(float64) })
	externalJSON(w, map[string]any{"crews": rows, "total_cost_usd": total, "window": window})
}
