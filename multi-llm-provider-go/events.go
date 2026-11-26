package llmproviders

import (
	"github.com/manishiitg/multi-llm-provider-go/interfaces"
)

// LLM Operation Types - Constants for operation names
const (
	OperationLLMInitialization = "llm_initialization"
	OperationLLMGeneration     = "llm_generation"
	OperationLLMToolCalling    = "llm_tool_calling"
)

// LLM Status Types - Constants for status values
const (
	StatusLLMInitialized = "initialized"
	StatusLLMFailed      = "failed"
	StatusLLMSuccess     = "success"
	StatusLLMInProgress  = "in_progress"
)

// LLM Capabilities - Constants for capability strings
const (
	CapabilityTextGeneration = "text_generation"
	CapabilityToolCalling    = "tool_calling"
	CapabilityStreaming      = "streaming"
)

// TokenUsage represents token consumption information
type TokenUsage struct {
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
	TotalTokens  int    `json:"total_tokens,omitempty"`
	Unit         string `json:"unit,omitempty"`
	Cost         string `json:"cost,omitempty"`
}

// LLMMetadata is re-exported from interfaces package for convenience
type LLMMetadata = interfaces.LLMMetadata

// EventEmitter is re-exported from interfaces package for convenience
type EventEmitter = interfaces.EventEmitter
