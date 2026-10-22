package costobserver

import (
	"log"

	unifiedevents "github.com/manishiitg/mcpagent/events"
)

// These records count SDK generations/native turns, not HTTP attempts inside a
// provider or coding CLI. Stable IDs let audits deduplicate stages and join
// completed usage to the ledger without logging prompts, responses or errors.
func (o *Observer) logModelRequest(event *unifiedevents.AgentEvent, stage, provider, model, countBasis string, generations int) {
	unit := "sdk_generation"
	if isCodingCLIProvider(provider) {
		unit = "native_turn"
	}
	if countBasis == "retained_native_step" {
		unit = "native_usage_step"
	}
	log.Printf("[MODEL_REQUEST] stage=%s unit=%s session_id=%q span_id=%q correlation_id=%q turn_id=%q provider=%q model=%q scope=%q activity=%q source_id=%q source_run_id=%q workflow_id=%q product_kind=%q product_id=%q recorded_generations=%d count_basis=%s",
		stage, unit, o.sessionID, event.SpanID, event.CorrelationID, event.TurnID,
		provider, model, o.scope, o.attribution.Activity, o.sourceID, o.sourceRunID,
		o.workflowID, o.attribution.ProductKind, o.attribution.ProductID, generations, countBasis)
}
