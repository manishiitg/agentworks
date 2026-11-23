package recorder

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// RequestInfo represents the input request for matching
type RequestInfo struct {
	Messages []MessageInfo `json:"messages"`
	ModelID  string        `json:"model_id,omitempty"`
	Options  OptionsInfo   `json:"options,omitempty"`
}

// MessageInfo represents a single message in the request
type MessageInfo struct {
	Role  string        `json:"role"`
	Parts []interface{} `json:"parts"` // Can be TextContent, ImageContent, etc.
}

// OptionsInfo represents call options
type OptionsInfo struct {
	Temperature float64 `json:"temperature,omitempty"`
	MaxTokens   int     `json:"max_tokens,omitempty"`
	JSONMode    bool    `json:"json_mode,omitempty"`
	ToolsCount  int     `json:"tools_count,omitempty"`
}

// ComputeRequestHash computes a hash of the request for matching
func ComputeRequestHash(request RequestInfo) (string, error) {
	// Marshal request to JSON for hashing
	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Compute SHA256 hash
	hash := sha256.Sum256(jsonData)
	return hex.EncodeToString(hash[:]), nil
}
