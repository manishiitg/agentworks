package recorder

import (
	"time"
)

// RecordedResponse represents a captured LLM response with metadata
type RecordedResponse struct {
	// Metadata
	Provider   string    `json:"provider"`
	ModelID    string    `json:"model_id"`
	TestName   string    `json:"test_name"`
	RecordedAt time.Time `json:"recorded_at"`

	// Request info (for matching)
	RequestHash string      `json:"request_hash"` // Hash of request for matching
	Request     interface{} `json:"request"`      // Full request data (messages, options)

	// Response data - stored as raw JSON (interface{} for flexible structure)
	// For streaming responses, this is an array of chunks
	// Stored as interface{} so it can be marshaled as proper JSON, not base64
	ResponseData interface{} `json:"response_data"`

	// Additional metadata
	ChunkCount int `json:"chunk_count,omitempty"` // For streaming responses
}

// RecordingConfig controls recording behavior
type RecordingConfig struct {
	Enabled  bool
	TestName string
	Provider string
	ModelID  string
	BaseDir  string // Base directory for storing recordings (default: testdata)
}
