package llm

import (
	"context"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// LLMCallFunc is a type-safe function signature for LLM calls.
type LLMCallFunc func(ctx context.Context, prompt string) (string, error)

// Re-export types from llmtypes for convenience
type Model = llmtypes.Model
type ChatMessageType = llmtypes.ChatMessageType
type ContentPart = llmtypes.ContentPart
type TextContent = llmtypes.TextContent
type ImageContent = llmtypes.ImageContent
type ToolCall = llmtypes.ToolCall
type FunctionCall = llmtypes.FunctionCall
type ToolCallResponse = llmtypes.ToolCallResponse
type MessageContent = llmtypes.MessageContent
type ContentResponse = llmtypes.ContentResponse
type ContentChoice = llmtypes.ContentChoice
type Usage = llmtypes.Usage
type Tool = llmtypes.Tool
type FunctionDefinition = llmtypes.FunctionDefinition
type ToolChoice = llmtypes.ToolChoice
type FunctionName = llmtypes.FunctionName
type CallOptions = llmtypes.CallOptions
type CallOption = llmtypes.CallOption
type WebSearchModel = llmtypes.WebSearchModel

// Re-export constants
const (
	ChatMessageTypeSystem   = llmtypes.ChatMessageTypeSystem
	ChatMessageTypeHuman    = llmtypes.ChatMessageTypeHuman
	ChatMessageTypeAI       = llmtypes.ChatMessageTypeAI
	ChatMessageTypeTool     = llmtypes.ChatMessageTypeTool
	ChatMessageTypeGeneric  = llmtypes.ChatMessageTypeGeneric
	ChatMessageTypeFunction = llmtypes.ChatMessageTypeFunction
)

// Re-export functions
var (
	WithModel         = llmtypes.WithModel
	WithTemperature   = llmtypes.WithTemperature
	WithMaxTokens     = llmtypes.WithMaxTokens
	WithJSONMode      = llmtypes.WithJSONMode
	WithTools         = llmtypes.WithTools
	WithToolChoice    = llmtypes.WithToolChoice
	WithStreamingFunc = llmtypes.WithStreamingFunc
	TextPart          = llmtypes.TextPart
	TextParts         = llmtypes.TextParts
	ImagePart         = llmtypes.ImagePart
	ImagePartBase64   = llmtypes.ImagePartBase64
	ImagePartURL      = llmtypes.ImagePartURL
)
