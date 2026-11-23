package llmtypes

const StreamChunkTypeNativeRetry StreamChunkType = "native_retry"

// NativeRetry reports a retry the CLI already owns; it never starts another retry.
type NativeRetry struct {
	Provider        string
	NativeSessionID string
	NativeTurnID    string
	Phase           string // start | end
	Attempt         int
	MaxAttempts     int
	DelayMS         int64
	Success         bool
}
