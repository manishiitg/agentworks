package llmtypes

import (
	"crypto/sha256"
	"encoding/hex"
)

const codingAgentReleaseSession = "coding_agent_release_session"

// WithCodingAgentReleaseSession pins a managed CLI release for one application
// chat across turns and backend restarts. It is independent of secret scoping.
func WithCodingAgentReleaseSession(sessionID string) CallOption {
	return func(opts *CallOptions) {
		if sessionID == "" {
			return
		}
		if opts.Metadata == nil {
			opts.Metadata = &Metadata{}
		}
		if opts.Metadata.Custom == nil {
			opts.Metadata.Custom = make(map[string]interface{})
		}
		sum := sha256.Sum256([]byte(sessionID))
		opts.Metadata.Custom[codingAgentReleaseSession] = hex.EncodeToString(sum[:])
	}
}

func codingAgentReleaseSessionKey(opts *CallOptions) string {
	if opts == nil || opts.Metadata == nil {
		return ""
	}
	key, _ := opts.Metadata.Custom[codingAgentReleaseSession].(string)
	return key
}
