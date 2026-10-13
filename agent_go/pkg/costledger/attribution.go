package costledger

// Attribution is a snapshot of the admitted work that caused a cost event.
// ActorID is the originating person/bot, independent of runtime authority and
// the existing shared-account accounting fields. Dashboard origin annotates
// the target's event; it never creates another accounting event.
type Attribution struct {
	AttributionVersion  int    `json:"attribution_version,omitempty"`
	ActorID             string `json:"actor_id,omitempty"`
	ProductKind         string `json:"product_kind,omitempty"`
	ProductID           string `json:"product_id,omitempty"`
	ProductOwnerID      string `json:"product_owner_id,omitempty"`
	ProductName         string `json:"product_name,omitempty"`
	Activity            string `json:"activity,omitempty"`
	OriginDashboardID   string `json:"origin_dashboard_id,omitempty"`
	OriginDashboardName string `json:"origin_dashboard_name,omitempty"`
	FunctionCallID      string `json:"function_call_id,omitempty"`
	ParentSessionID     string `json:"parent_session_id,omitempty"`
	ParentExecutionID   string `json:"parent_execution_id,omitempty"`
}

const AttributionVersion = 1

// AddEntry uses the same token normalization and pricing accounting as the
// existing summaries. Drill-down must not invent a second token calculation.
func (a *Aggregate) AddEntry(e Entry) {
	normalizeEntry(&e)
	a.add(e)
}
