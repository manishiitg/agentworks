package recorder

import (
	"context"
	"fmt"
)

// Recorder handles recording and replaying LLM responses
type Recorder struct {
	config RecordingConfig
}

// NewRecorder creates a new recorder instance
func NewRecorder(config RecordingConfig) *Recorder {
	return &Recorder{
		config: config,
	}
}

// RecordVertexChunks records streaming chunks from Vertex/Gemini
func (r *Recorder) RecordVertexChunks(chunks []interface{}, request RequestInfo) (string, error) {
	if !r.config.Enabled {
		return "", nil
	}

	// Save chunks directly as interface{} (will be marshaled as JSON array)
	filePath, err := SaveResponse(r.config, chunks, len(chunks), request)
	if err != nil {
		return "", err
	}

	return filePath, nil
}

// LoadVertexChunks loads recorded chunks for Vertex/Gemini that match the given request
func (r *Recorder) LoadVertexChunks(request RequestInfo) ([]map[string]interface{}, error) {
	recorded, err := LoadResponseByRequest(r.config, request)
	if err != nil {
		return nil, err
	}

	// ResponseData is already unmarshaled as interface{}, convert to chunks
	chunksArray, ok := recorded.ResponseData.([]interface{})
	if !ok {
		return nil, fmt.Errorf("response_data is not an array")
	}

	// Convert []interface{} to []map[string]interface{}
	chunks := make([]map[string]interface{}, 0, len(chunksArray))
	for _, chunk := range chunksArray {
		chunkMap, ok := chunk.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("chunk is not a map")
		}
		chunks = append(chunks, chunkMap)
	}

	return chunks, nil
}

// RecordOpenAIResponse records a non-streaming OpenAI response
func (r *Recorder) RecordOpenAIResponse(response interface{}, request RequestInfo) (string, error) {
	if !r.config.Enabled {
		return "", nil
	}

	// Save response directly as interface{} (will be marshaled as JSON object)
	filePath, err := SaveResponse(r.config, response, 1, request)
	if err != nil {
		return "", err
	}

	return filePath, nil
}

// LoadOpenAIResponse loads a recorded OpenAI response that matches the given request
func (r *Recorder) LoadOpenAIResponse(request RequestInfo) (map[string]interface{}, error) {
	recorded, err := LoadResponseByRequest(r.config, request)
	if err != nil {
		return nil, err
	}

	// ResponseData is already unmarshaled as interface{}, convert to map
	responseMap, ok := recorded.ResponseData.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("response_data is not a map")
	}

	return responseMap, nil
}

// RecordResponsesAPIResponse records a response from the Responses API
func (r *Recorder) RecordResponsesAPIResponse(response interface{}, request RequestInfo) (string, error) {
	if !r.config.Enabled {
		return "", nil
	}

	// Save response directly as interface{}
	filePath, err := SaveResponse(r.config, response, 1, request)
	if err != nil {
		return "", err
	}

	return filePath, nil
}

// LoadResponsesAPIResponse loads a recorded Responses API response that matches the given request
func (r *Recorder) LoadResponsesAPIResponse(request RequestInfo) (map[string]interface{}, error) {
	recorded, err := LoadResponseByRequest(r.config, request)
	if err != nil {
		return nil, err
	}

	// ResponseData is already unmarshaled as interface{}, convert to map
	responseMap, ok := recorded.ResponseData.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("response_data is not a map")
	}

	return responseMap, nil
}

// RecordOpenAIChunks records streaming chunks from OpenAI
func (r *Recorder) RecordOpenAIChunks(chunks []interface{}, request RequestInfo) (string, error) {
	if !r.config.Enabled {
		return "", nil
	}

	// Save chunks directly as interface{} (will be marshaled as JSON array)
	filePath, err := SaveResponse(r.config, chunks, len(chunks), request)
	if err != nil {
		return "", err
	}

	return filePath, nil
}

// LoadOpenAIChunks loads recorded chunks for OpenAI that match the given request
func (r *Recorder) LoadOpenAIChunks(request RequestInfo) ([]map[string]interface{}, error) {
	recorded, err := LoadResponseByRequest(r.config, request)
	if err != nil {
		return nil, err
	}

	// ResponseData is already unmarshaled as interface{}, convert to chunks
	chunksArray, ok := recorded.ResponseData.([]interface{})
	if !ok {
		return nil, fmt.Errorf("response_data is not an array")
	}

	// Convert []interface{} to []map[string]interface{}
	chunks := make([]map[string]interface{}, 0, len(chunksArray))
	for _, chunk := range chunksArray {
		chunkMap, ok := chunk.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("chunk is not a map")
		}
		chunks = append(chunks, chunkMap)
	}

	return chunks, nil
}

// RecordBedrockEvents records streaming events from Bedrock
func (r *Recorder) RecordBedrockEvents(events []interface{}, request RequestInfo) (string, error) {
	if !r.config.Enabled {
		return "", nil
	}

	// Save events directly as interface{} (will be marshaled as JSON array)
	filePath, err := SaveResponse(r.config, events, len(events), request)
	if err != nil {
		return "", err
	}

	return filePath, nil
}

// LoadBedrockEvents loads recorded events for Bedrock that match the given request
func (r *Recorder) LoadBedrockEvents(request RequestInfo) ([]map[string]interface{}, error) {
	recorded, err := LoadResponseByRequest(r.config, request)
	if err != nil {
		return nil, err
	}

	// ResponseData is already unmarshaled as interface{}, convert to events
	eventsArray, ok := recorded.ResponseData.([]interface{})
	if !ok {
		return nil, fmt.Errorf("response_data is not an array")
	}

	// Convert []interface{} to []map[string]interface{}
	events := make([]map[string]interface{}, 0, len(eventsArray))
	for _, event := range eventsArray {
		eventMap, ok := event.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("event is not a map")
		}
		events = append(events, eventMap)
	}

	return events, nil
}

// IsRecordingEnabled returns true if recording is enabled
func (r *Recorder) IsRecordingEnabled() bool {
	return r.config.Enabled
}

// IsReplayEnabled returns true if replay is enabled (opposite of recording)
func (r *Recorder) IsReplayEnabled() bool {
	// If recording is disabled, check if we have a saved response
	if !r.config.Enabled {
		_, err := LoadResponse(r.config)
		return err == nil
	}
	return false
}

// SetReplayMode enables replay mode (disables recording)
func (r *Recorder) SetReplayMode(enabled bool) {
	r.config.Enabled = !enabled
}

// GetConfig returns the recording configuration
func (r *Recorder) GetConfig() RecordingConfig {
	return r.config
}

// ContextKey is used to pass recorder through context
type contextKey string

const RecorderContextKey contextKey = "llm_recorder"

// WithRecorder adds a recorder to the context
func WithRecorder(ctx context.Context, recorder *Recorder) context.Context {
	return context.WithValue(ctx, RecorderContextKey, recorder)
}

// FromContext extracts recorder from context
func FromContext(ctx context.Context) (*Recorder, bool) {
	recorder, ok := ctx.Value(RecorderContextKey).(*Recorder)
	return recorder, ok
}
