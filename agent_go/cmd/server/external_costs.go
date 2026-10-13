package server

import "net/http"

func externalCostsDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	properties := map[string]any{
		"action":              map[string]any{"type": "string", "enum": []string{"breakdown", "detail"}},
		"group_by":            map[string]any{"type": "string", "enum": []string{"product", "activity", "person", "model", "day"}},
		"from":                externalString("Inclusive UTC date YYYY-MM-DD; default the last 30 days. Maximum window 366 days."),
		"to":                  externalString("Inclusive UTC date YYYY-MM-DD; default today."),
		"product_kind":        externalString("Product kind from a returned product row, or unattributed."),
		"product_id":          externalString("Native product ID from a returned product row; never a filesystem path."),
		"product_owner_id":    externalString("Owner from the product row, distinguishing identically named private projects."),
		"activity":            externalString("Activity from a breakdown row, including unattributed for older/missing attribution."),
		"person_id":           externalString("Originating person/bot ID from a returned person row, or unattributed."),
		"model":               externalString("Requested/effective model key from a returned model row."),
		"origin_dashboard_id": externalString("Filter target executions initiated by this dashboard; their spend is counted once."),
		"top":                 map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Breakdown top N by tokens, default 10. Full filtered total and omitted remainder are returned."},
		"limit":               map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Detail page size, default 50."},
		"cursor":              externalString("Next cursor from the same detail query; visibility is checked again on every page."),
	}
	add("costs", "Where did tokens go and why? breakdown groups recorded costs by product, activity, person, model or UTC day; detail drills a selected product/activity/person/dashboard into individual executions with tokens, pricing basis, recorded times and link IDs. Uses current product sharing and existing connection consent/bounds: people see all actors' usage within accessible products and their own personal usage; administrators with users:manage see all. No account credentials, other people's personal account names, prompts or tool payloads. Older missing attribution is explicit. Provider actual, estimated and subscription shadow costs stay distinct; model generation duration is not run wall time. Default last 30 days, maximum 366; no new permission scope.", false, false, properties, "action")
}

func (api *StreamingAPI) externalCostsCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if !externalCostsAllowed(claims) {
		externalError(w, 403, "insufficient_scope", "Cost access is unavailable for this connection.")
		return
	}
	q := costsQuery{
		Action: externalArg(args, "action"), GroupBy: externalArg(args, "group_by"),
		From: externalArg(args, "from"), To: externalArg(args, "to"),
		ProductKind: externalArg(args, "product_kind"), ProductID: externalArg(args, "product_id"),
		ProductOwnerID: externalArg(args, "product_owner_id"), Activity: externalArg(args, "activity"),
		PersonID: externalArg(args, "person_id"), Model: externalArg(args, "model"),
		DashboardID: externalArg(args, "origin_dashboard_id"), Top: externalInt(args, "top", 10),
		Limit: externalInt(args, "limit", 50), Cursor: externalArg(args, "cursor"),
	}
	result, err := api.queryCosts(r.Context(), claims, q)
	if err != nil {
		externalError(w, 400, "cost_query_failed", err.Error())
		return
	}
	externalJSON(w, result)
}
