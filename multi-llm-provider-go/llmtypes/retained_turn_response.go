package llmtypes

// RetainedTurnResponse keeps publication identity beside the exact text
// selected by one native read, so a later scan cannot commit a different root.
type RetainedTurnResponse struct {
	Messages                []MessageContent
	NativeSessionID         string
	NativeContinuationRoots []string
}
