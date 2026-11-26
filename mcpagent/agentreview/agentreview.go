// Package agentreview exposes the shared live-test capture and agent-sign-off
// contract to products that use mcpagent.
package agentreview

import internal "github.com/manishiitg/mcpagent/internal/agentreview"

type Review = internal.Review
type Record = internal.Record

var (
	StreamingCriteria  = internal.StreamingCriteria
	Write              = internal.Write
	WriteWithCriteria  = internal.WriteWithCriteria
	RequireAllApproved = internal.RequireAllApproved
	RequireReviewed    = internal.RequireReviewed
)
