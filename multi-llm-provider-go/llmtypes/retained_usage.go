package llmtypes

// RetainedTurnUsage is the usage a coding CLI wrote in its own transcript for one model run of a turn that was typed
// into an already-running terminal. Those turns never pass through GenerateContent, so nothing else reports their
// tokens. ID is the CLI's own run or message id: it is stable, so a consumer can record each ID once.
type RetainedTurnUsage struct {
	ID    string
	Usage Usage
	// Info carries the same token and cost fields a normal response of that provider would, so one code path can price it.
	Info GenerationInfo
}
