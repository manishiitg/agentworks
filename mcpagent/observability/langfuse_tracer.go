//go:build !langfuse_disabled

package observability

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"

	"github.com/joho/godotenv"

	"github.com/manishiitg/mcpagent/events"
)

// EventData interfaces for type-safe event data handling
type EventDataWithQuestion interface {
	GetQuestion() string
}

type EventDataWithModelID interface {
	GetModelID() string
}

type EventDataWithAvailableTools interface {
	GetAvailableTools() int
}

type EventDataWithResult interface {
	GetResult() string
}

type EventDataWithOutput interface {
	GetOutput() string
}

type EventDataWithTokens interface {
	GetPromptTokens() int
	GetCompletionTokens() int
	GetTotalTokens() int
}

// Event type constants for type safety
const (
	EventTypeAgentStart         = "agent_start"
	EventTypeAgentEnd           = "agent_end"
	EventTypeAgentError         = "agent_error"
	EventTypeConversationStart  = "conversation_start"
	EventTypeConversationEnd    = "conversation_end"
	EventTypeUnifiedCompletion  = "unified_completion"
	EventTypeLLMGenerationStart = "llm_generation_start"
	EventTypeLLMGenerationEnd   = "llm_generation_end"
	EventTypeLLMGenerationError = "llm_generation_error"
	EventTypeToolCallStart      = "tool_call_start"
	EventTypeToolCallEnd        = "tool_call_end"
	EventTypeToolCallError      = "tool_call_error"
	EventTypeTokenUsage         = "token_usage"

	// MCP Server connection events
	EventTypeMCPServerConnectionStart = "mcp_server_connection_start"
	EventTypeMCPServerConnectionEnd   = "mcp_server_connection_end"
	EventTypeMCPServerSelection       = "mcp_server_selection"

	// Error & Retry events
	EventTypeRetryAttempt    = "retry_attempt"
	EventTypeMaxTurnsReached = "max_turns_reached"

	// Streaming events
	EventTypeStreamingStart = "streaming_start"
	EventTypeStreamingEnd   = "streaming_end"
)

// LangfuseTracer implements the Tracer interface using Langfuse v2 API patterns.
// Implements shared state pattern similar to Python implementation with proper
// authentication, error handling, and debug logging.
type LangfuseTracer struct {
	client    *http.Client
	host      string
	publicKey string
	secretKey string
	debug     bool

	// Shared state for all instances (similar to Python class-level state)
	traces map[string]*langfuseTrace
	spans  map[string]*langfuseObservation

	// Hierarchy tracking: traceID -> spanID mappings
	agentSpans         map[string]string // traceID -> agent span ID
	conversationSpans  map[string]string // traceID -> conversation span ID
	llmGenerationSpans map[string]string // traceID -> current LLM generation span ID
	toolCallSpans      map[string]string // {traceID}_{turn}_{toolName} -> tool span ID
	mcpConnectionSpans map[string]string // serverName -> mcp connection span ID

	mu sync.RWMutex

	// Background processing
	eventQueue chan *langfuseEvent
	flushCh    chan chan struct{} // Channel to signal flush and wait for completion
	stopCh     chan struct{}
	wg         sync.WaitGroup

	logger loggerv2.Logger
}

// Shared state across all instances (similar to Python class-level variables)
var (
	sharedLangfuseClient *LangfuseTracer
	sharedInitialized    bool
	sharedMutex          sync.Mutex
)

// langfuseTrace represents a trace in Langfuse v2 API format
type langfuseTrace struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Input     interface{}            `json:"input,omitempty"`
	Output    interface{}            `json:"output,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	UserID    string                 `json:"userId,omitempty"`
	SessionID string                 `json:"sessionId,omitempty"`
	Tags      []string               `json:"tags,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	Public    bool                   `json:"public,omitempty"`
	Release   string                 `json:"release,omitempty"`
	Version   string                 `json:"version,omitempty"`
}

// langfuseObservation represents a Langfuse observation following the proper data model
type langfuseObservation struct {
	ID                  string                 `json:"id"`
	TraceID             string                 `json:"traceId"`
	ParentObservationID string                 `json:"parentObservationId,omitempty"`
	Name                string                 `json:"name"`
	Type                string                 `json:"type"` // "SPAN", "GENERATION", "AGENT", "TOOL", etc.
	Input               interface{}            `json:"input,omitempty"`
	Output              interface{}            `json:"output,omitempty"`
	Metadata            map[string]interface{} `json:"metadata,omitempty"`
	StartTime           time.Time              `json:"startTime"`
	EndTime             *time.Time             `json:"endTime,omitempty"`
	Level               string                 `json:"level,omitempty"`
	StatusMessage       string                 `json:"statusMessage,omitempty"`
	Version             string                 `json:"version,omitempty"`

	// Generation-specific fields
	Model               string                 `json:"model,omitempty"`
	ModelParameters     map[string]interface{} `json:"modelParameters,omitempty"`
	Usage               *LangfuseUsage         `json:"usage,omitempty"`
	CompletionStartTime *time.Time             `json:"completionStartTime,omitempty"`
	PromptTokens        int                    `json:"promptTokens,omitempty"`
	CompletionTokens    int                    `json:"completionTokens,omitempty"`
	TotalTokens         int                    `json:"totalTokens,omitempty"`

	// Tool-specific fields
	ToolName        string `json:"toolName,omitempty"`
	ToolDescription string `json:"toolDescription,omitempty"`

	// Agent-specific fields
	AgentName string `json:"agentName,omitempty"`
}

// LangfuseUsage represents usage metrics in Langfuse format
type LangfuseUsage struct {
	Input      int     `json:"input,omitempty"`
	Output     int     `json:"output,omitempty"`
	Total      int     `json:"total,omitempty"`
	Unit       string  `json:"unit,omitempty"`
	Model      string  `json:"model,omitempty"`
	InputCost  float64 `json:"inputCost,omitempty"`
	OutputCost float64 `json:"outputCost,omitempty"`
	TotalCost  float64 `json:"totalCost,omitempty"`
}

// langfuseEvent represents an event for the ingestion API
type langfuseEvent struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"` // "trace-create", "span-create", "span-update", "generation-create", "generation-update"
	Timestamp time.Time              `json:"timestamp"`
	Body      interface{}            `json:"body"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// langfuseIngestionPayload represents the batch ingestion payload
type langfuseIngestionPayload struct {
	Batch []langfuseEvent `json:"batch"`
}

// newLangfuseTracerWithLogger creates a new Langfuse tracer with an injected logger
func newLangfuseTracerWithLogger(logger loggerv2.Logger) (Tracer, error) {
	sharedMutex.Lock()
	defer sharedMutex.Unlock()

	if !sharedInitialized {
		if err := initializeSharedLangfuseClientWithLogger(logger); err != nil {
			sharedInitialized = true // Mark as initialized even on failure to prevent retry loops
			return nil, err
		}
		sharedInitialized = true
	}

	if sharedLangfuseClient == nil {
		return nil, errors.New("failed to initialize shared Langfuse client")
	}

	return sharedLangfuseClient, nil
}

// NewLangfuseTracerWithLogger creates a new Langfuse tracer with an injected logger
func NewLangfuseTracerWithLogger(logger loggerv2.Logger) (Tracer, error) {
	return newLangfuseTracerWithLogger(logger)
}

// initializeSharedLangfuseClientWithLogger initializes the shared Langfuse client with an injected logger
func initializeSharedLangfuseClientWithLogger(logger loggerv2.Logger) error {
	// Auto-load .env file if present (similar to Python dotenv)
	if _, err := os.Stat(".env"); err == nil {
		if err := godotenv.Load(); err != nil {
			// Don't fail if .env can't be loaded, just log
			log.Printf("Warning: Could not load .env file: %v", err)
		}
	}

	// Load credentials from environment
	publicKey := os.Getenv("LANGFUSE_PUBLIC_KEY")
	secretKey := os.Getenv("LANGFUSE_SECRET_KEY")
	host := os.Getenv("LANGFUSE_BASE_URL")

	if host == "" {
		host = "https://cloud.langfuse.com"
	}

	if publicKey == "" || secretKey == "" {
		return fmt.Errorf("langfuse credentials missing. Required environment variables:\n"+
			"- LANGFUSE_PUBLIC_KEY\n"+
			"- LANGFUSE_SECRET_KEY\n"+
			"- LANGFUSE_BASE_URL (optional, default: %s)", host)
	}

	// Always enable debug for comprehensive observability (similar to Python)
	debug := true

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	tracer := &LangfuseTracer{
		client:             client,
		host:               host,
		publicKey:          publicKey,
		secretKey:          secretKey,
		debug:              debug,
		traces:             make(map[string]*langfuseTrace),
		spans:              make(map[string]*langfuseObservation),
		agentSpans:         make(map[string]string),
		conversationSpans:  make(map[string]string),
		llmGenerationSpans: make(map[string]string),
		toolCallSpans:      make(map[string]string),
		mcpConnectionSpans: make(map[string]string),
		eventQueue:         make(chan *langfuseEvent, 1000),
		flushCh:            make(chan chan struct{}),
		stopCh:             make(chan struct{}),
		logger:             logger, // Use injected logger instead of default
	}

	// Test authentication (similar to Python auth_check)
	if err := tracer.authCheck(); err != nil {
		return fmt.Errorf("langfuse authentication failed for %s...: %w", publicKey[:10], err)
	}

	// Start background event processor
	tracer.wg.Add(1)
	go tracer.eventProcessor()

	sharedLangfuseClient = tracer

	if tracer.debug {
		tracer.logger.Info("Langfuse: Authentication successful",
			loggerv2.String("public_key_prefix", publicKey[:10]))
	}

	return nil
}

// getV2Logger returns the v2 logger for this tracer
func (l *LangfuseTracer) getV2Logger() loggerv2.Logger {
	return l.logger
}

// authCheck verifies authentication with Langfuse API using health endpoint
func (l *LangfuseTracer) authCheck() error {
	req, err := http.NewRequest("GET", l.host+"/api/public/health", nil)
	if err != nil {
		return err
	}

	req.SetBasicAuth(l.publicKey, l.secretKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() // Ignore errors during cleanup

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("authentication failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// generateID generates a unique ID for traces and spans
func generateID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to time-based ID if random read fails
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}

// StartTrace starts a new trace using Langfuse v2 API pattern
func (l *LangfuseTracer) StartTrace(name string, input interface{}) TraceID {
	id := generateID()
	trace := &langfuseTrace{
		ID:        id,
		Name:      name,
		Input:     input,
		Timestamp: time.Now(),
		Metadata:  make(map[string]interface{}),
	}

	l.mu.Lock()
	l.traces[id] = trace
	l.mu.Unlock()

	// Queue trace creation event
	event := &langfuseEvent{
		ID:        generateID(),
		Type:      "trace-create",
		Timestamp: time.Now(),
		Body:      trace,
	}

	select {
	case l.eventQueue <- event:
	default:
		v2Logger := l.getV2Logger()
		v2Logger.Error("Langfuse: Event queue full, dropping trace-create event", nil)
	}

	v2Logger := l.getV2Logger()
	v2Logger.Info("Langfuse: Started trace",
		loggerv2.String("name", name),
		loggerv2.String("id", id))

	return TraceID(id)
}

// StartSpan starts a new observation using Langfuse v2 API pattern
func (l *LangfuseTracer) StartSpan(parentID string, name string, input interface{}) SpanID {
	return l.StartObservation(parentID, "SPAN", name, input)
}

// StartObservation starts a new observation with the specified type
func (l *LangfuseTracer) StartObservation(parentID string, obsType string, name string, input interface{}) SpanID {
	id := generateID()
	observation := &langfuseObservation{
		ID:                  id,
		TraceID:             parentID, // For root observations, this is the trace ID
		ParentObservationID: "",       // Will be set if this is a child observation
		Name:                name,
		Type:                obsType, // Use the specified observation type
		Input:               input,
		StartTime:           time.Now(),
		Metadata:            make(map[string]interface{}),
	}

	// Check if parentID is actually a span ID (child observation)
	l.mu.RLock()
	if parentObservation, exists := l.spans[parentID]; exists {
		observation.TraceID = parentObservation.TraceID
		observation.ParentObservationID = parentID
	}
	l.mu.RUnlock()

	l.mu.Lock()
	l.spans[id] = observation
	l.mu.Unlock()

	// Queue observation creation event
	event := &langfuseEvent{
		ID:        generateID(),
		Type:      "observation-create",
		Timestamp: time.Now(),
		Body:      observation,
	}

	v2Logger := l.getV2Logger()
	select {
	case l.eventQueue <- event:
	default:
		v2Logger.Error("Langfuse: Event queue full, dropping span-create event", nil)
	}

	v2Logger.Info("Langfuse: Started span",
		loggerv2.String("name", name),
		loggerv2.String("id", id),
		loggerv2.String("parent", parentID))

	return SpanID(id)
}

// EndSpan ends a span with optional output and error
func (l *LangfuseTracer) EndSpan(spanID SpanID, output interface{}, err error) {
	l.mu.Lock()
	span, exists := l.spans[string(spanID)]
	v2Logger := l.getV2Logger()
	if !exists {
		l.mu.Unlock()
		v2Logger.Error("Langfuse: Span not found for end", nil, loggerv2.String("span_id", string(spanID)))
		return
	}

	endTime := time.Now()
	span.EndTime = &endTime
	span.Output = output

	if err != nil {
		span.Level = "ERROR"
		span.StatusMessage = err.Error()
	} else {
		span.Level = "DEFAULT"
	}
	l.mu.Unlock()

	// Queue observation update event
	event := &langfuseEvent{
		ID:        generateID(),
		Type:      "observation-update",
		Timestamp: time.Now(),
		Body:      span,
	}

	select {
	case l.eventQueue <- event:
	default:
		v2Logger.Error("Langfuse: Event queue full, dropping span-update event", nil)
	}

	v2Logger.Info("Langfuse: Ended span",
		loggerv2.String("name", span.Name),
		loggerv2.String("id", string(spanID)),
		loggerv2.Any("has_error", err != nil))
}

// EndTrace ends a trace with optional output
func (l *LangfuseTracer) EndTrace(traceID TraceID, output interface{}) {
	l.mu.Lock()
	trace, exists := l.traces[string(traceID)]
	v2Logger := l.getV2Logger()
	if !exists {
		l.mu.Unlock()
		v2Logger.Error("Langfuse: Trace not found for end", nil, loggerv2.String("trace_id", string(traceID)))
		return
	}

	trace.Output = output
	l.mu.Unlock()

	// Queue trace update event (using trace-create with updated data)
	event := &langfuseEvent{
		ID:        generateID(),
		Type:      "trace-create",
		Timestamp: time.Now(),
		Body:      trace,
	}

	select {
	case l.eventQueue <- event:
	default:
		v2Logger.Error("Langfuse: Event queue full, dropping trace-update event", nil)
	}

	v2Logger.Info("Langfuse: Ended trace",
		loggerv2.String("name", trace.Name),
		loggerv2.String("id", string(traceID)))

	// Explicitly cleanup trace data to prevent memory leaks
	l.cleanupTrace(traceID)
}

// cleanupTrace removes trace-related data from memory
func (l *LangfuseTracer) cleanupTrace(traceID TraceID) {
	id := string(traceID)
	l.mu.Lock()
	defer l.mu.Unlock()

	// Remove from traces map
	delete(l.traces, id)

	// Remove from hierarchy mappings
	delete(l.agentSpans, id)
	delete(l.conversationSpans, id)
	delete(l.llmGenerationSpans, id)

	// Note: We don't iterate spans or toolCallSpans here to avoid O(N) operations.
	// The background cleanupOldEntries will eventually catch orphaned spans
	// based on their timestamps.
}

// CreateGenerationSpan creates a generation span for LLM calls
func (l *LangfuseTracer) CreateGenerationSpan(traceID TraceID, parentID SpanID, name, model string, input interface{}) SpanID {
	id := generateID()
	span := &langfuseObservation{
		ID:                  id,
		TraceID:             string(traceID),
		ParentObservationID: string(parentID),
		Name:                name,
		Type:                "GENERATION",
		Input:               input,
		StartTime:           time.Now(),
		Model:               model,
		Metadata:            make(map[string]interface{}),
	}

	// If parentID is empty, this is a root generation
	if parentID == "" {
		span.ParentObservationID = ""
	}

	l.mu.Lock()
	l.spans[id] = span
	l.mu.Unlock()

	// Queue generation creation event
	event := &langfuseEvent{
		ID:        generateID(),
		Type:      "generation-create",
		Timestamp: time.Now(),
		Body:      span,
	}

	v2Logger := l.getV2Logger()
	select {
	case l.eventQueue <- event:
	default:
		v2Logger.Error("Langfuse: Event queue full, dropping generation-create event", nil)
	}

	v2Logger.Info("Langfuse: Started generation",
		loggerv2.String("name", name),
		loggerv2.String("id", id),
		loggerv2.String("model", model))

	return SpanID(id)
}

// EndGenerationSpan ends a generation span with metadata, usage metrics, and optional error
func (l *LangfuseTracer) EndGenerationSpan(spanID SpanID, metadata map[string]interface{}, usage UsageMetrics, err error) {
	l.mu.Lock()
	span, exists := l.spans[string(spanID)]
	v2Logger := l.getV2Logger()
	if !exists {
		l.mu.Unlock()
		v2Logger.Error("Langfuse: Generation span not found for end", nil, loggerv2.String("span_id", string(spanID)))
		return
	}

	endTime := time.Now()
	span.EndTime = &endTime

	// Extract content from metadata for output, store rest as metadata
	if content, ok := metadata["content"]; ok && content != nil && content != "" {
		span.Output = content
		delete(metadata, "content") // Remove from metadata since it's now in output
		v2Logger.Debug("Langfuse: Set generation output",
			loggerv2.String("span_id", string(spanID)),
			loggerv2.Int("content_length", len(fmt.Sprintf("%v", content))))
	}
	span.Metadata = metadata

	// Convert usage metrics to Langfuse format
	// Langfuse will automatically calculate costs based on model name and token usage
	if usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.TotalTokens > 0 {
		span.Usage = &LangfuseUsage{
			Input:  usage.InputTokens,
			Output: usage.OutputTokens,
			Total:  usage.TotalTokens,
			Unit:   usage.Unit,
			Model:  span.Model, // Include model name in usage for Langfuse cost calculation
			// Cost fields are omitted - Langfuse will calculate costs automatically
		}
		span.PromptTokens = usage.InputTokens
		span.CompletionTokens = usage.OutputTokens
		span.TotalTokens = usage.TotalTokens
	}

	if err != nil {
		span.Level = "ERROR"
		span.StatusMessage = err.Error()
	} else {
		span.Level = "DEFAULT"
	}
	l.mu.Unlock()

	// Queue generation update event
	event := &langfuseEvent{
		ID:        generateID(),
		Type:      "generation-update",
		Timestamp: time.Now(),
		Body:      span,
	}

	select {
	case l.eventQueue <- event:
	default:
		v2Logger.Error("Langfuse: Event queue full, dropping generation-update event", nil)
	}

	v2Logger.Info("Langfuse: Ended generation",
		loggerv2.String("name", span.Name),
		loggerv2.String("id", string(spanID)),
		loggerv2.Int("input_tokens", usage.InputTokens),
		loggerv2.Int("output_tokens", usage.OutputTokens),
		loggerv2.Int("total_tokens", usage.TotalTokens),
		loggerv2.Any("has_error", err != nil))
}

// eventProcessor processes events in the background and sends them to Langfuse
func (l *LangfuseTracer) eventProcessor() {
	defer l.wg.Done()

	ticker := time.NewTicker(2 * time.Second) // Batch events every 2 seconds
	defer ticker.Stop()

	cleanupTicker := time.NewTicker(5 * time.Minute) // Cleanup every 5 minutes
	defer cleanupTicker.Stop()

	var batch []*langfuseEvent

	for {
		select {
		case event := <-l.eventQueue:
			batch = append(batch, event)

			// Send batch when it reaches size limit
			if len(batch) >= 50 {
				l.sendBatch(batch)
				batch = nil
			}

		case <-ticker.C:
			// Send batch on timer if there are events
			if len(batch) > 0 {
				l.sendBatch(batch)
				batch = nil
			}

		case <-cleanupTicker.C:
			// Cleanup old entries from memory
			l.cleanupOldEntries()

		case doneCh := <-l.flushCh:
			// Flush signal received - send any pending batch immediately
			if len(batch) > 0 {
				l.sendBatch(batch)
				batch = nil
			}
			// Signal completion
			close(doneCh)

		case <-l.stopCh:
			// Send final batch and exit
			if len(batch) > 0 {
				l.sendBatch(batch)
			}
			return
		}
	}
}

// cleanupOldEntries removes old traces and spans from memory to prevent leaks
func (l *LangfuseTracer) cleanupOldEntries() {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Retention period: 1 hour
	threshold := time.Now().Add(-1 * time.Hour)
	spansRemoved := 0
	tracesRemoved := 0

	// 1. Cleanup spans
	for id, span := range l.spans {
		// If span finished long ago OR started long ago (if never ended/zombie)
		if (span.EndTime != nil && span.EndTime.Before(threshold)) || span.StartTime.Before(threshold) {
			delete(l.spans, id)
			spansRemoved++
		}
	}

	// 2. Cleanup traces
	for id, trace := range l.traces {
		if trace.Timestamp.Before(threshold) {
			delete(l.traces, id)
			tracesRemoved++
		}
	}

	// 3. Cleanup mappings pointing to deleted spans
	for key, spanID := range l.toolCallSpans {
		if _, exists := l.spans[spanID]; !exists {
			delete(l.toolCallSpans, key)
		}
	}
	for key, spanID := range l.mcpConnectionSpans {
		if _, exists := l.spans[spanID]; !exists {
			delete(l.mcpConnectionSpans, key)
		}
	}

	// 4. Cleanup mappings pointing to deleted traces
	for traceID := range l.agentSpans {
		if _, exists := l.traces[traceID]; !exists {
			delete(l.agentSpans, traceID)
		}
	}
	for traceID := range l.conversationSpans {
		if _, exists := l.traces[traceID]; !exists {
			delete(l.conversationSpans, traceID)
		}
	}
	for traceID := range l.llmGenerationSpans {
		if _, exists := l.traces[traceID]; !exists {
			delete(l.llmGenerationSpans, traceID)
		}
	}

	if spansRemoved > 0 || tracesRemoved > 0 {
		l.getV2Logger().Debug("Langfuse: Cleaned up old entries",
			loggerv2.Int("spans_removed", spansRemoved),
			loggerv2.Int("traces_removed", tracesRemoved))
	}
}

// sendBatch sends a batch of events to Langfuse ingestion API
func (l *LangfuseTracer) sendBatch(events []*langfuseEvent) {
	if len(events) == 0 {
		return
	}

	// Convert to slice of values instead of pointers
	batch := make([]langfuseEvent, len(events))
	for i, event := range events {
		batch[i] = *event
	}

	payload := langfuseIngestionPayload{
		Batch: batch,
	}

	v2Logger := l.getV2Logger()
	jsonData, err := json.Marshal(payload)
	if err != nil {
		v2Logger.Error("Langfuse: Failed to marshal batch", err)
		return
	}

	req, err := http.NewRequest("POST", l.host+"/api/public/ingestion", bytes.NewBuffer(jsonData))
	if err != nil {
		v2Logger.Error("Langfuse: Failed to create request", err)
		return
	}

	req.SetBasicAuth(l.publicKey, l.secretKey)
	req.Header.Set("Content-Type", "application/json")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := l.client.Do(req)
	if err != nil {
		v2Logger.Error("Langfuse: Failed to send batch", err)
		return
	}
	defer func() { _ = resp.Body.Close() }() // Ignore errors during cleanup

	// Read response body once
	body, _ := io.ReadAll(resp.Body)

	// Handle response - accept 200 (OK), 201 (Created), and 207 (Multi-Status for batch operations)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != 207 {
		v2Logger.Error("Langfuse: Batch failed", nil,
			loggerv2.Int("status_code", resp.StatusCode),
			loggerv2.String("body", string(body)))
		return
	}

	// For status 207 (Multi-Status), check if there are any actual errors
	if resp.StatusCode == 207 {
		var batchResult map[string]interface{}
		if err := json.Unmarshal(body, &batchResult); err == nil {
			if errors, ok := batchResult["errors"].([]interface{}); ok && len(errors) > 0 {
				v2Logger.Error("Langfuse: Batch had errors", nil, loggerv2.String("body", string(body)))
				return
			}
		}
		// If no errors or can't parse, treat as success
	}

	v2Logger.Info("Langfuse: Sent batch successfully", loggerv2.Int("events_count", len(events)))
}

// Flush sends any pending events immediately and waits for them to be sent
func (l *LangfuseTracer) Flush() {
	v2Logger := l.getV2Logger()
	v2Logger.Debug("Langfuse: Flush started")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// First, wait for queue to drain (events moved to batch by eventProcessor)
	for {
		select {
		case <-ctx.Done():
			v2Logger.Warn("Langfuse: Flush timeout waiting for queue to drain")
			return
		default:
			if len(l.eventQueue) == 0 {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if len(l.eventQueue) == 0 {
			break
		}
	}

	v2Logger.Debug("Langfuse: Queue drained, sending flush signal")

	// Send flush signal and wait for completion
	doneCh := make(chan struct{})
	select {
	case l.flushCh <- doneCh:
		// Wait for flush to complete
		select {
		case <-doneCh:
			v2Logger.Debug("Langfuse: Flush completed successfully")
		case <-ctx.Done():
			v2Logger.Warn("Langfuse: Flush timeout waiting for batch send")
		}
	case <-ctx.Done():
		v2Logger.Warn("Langfuse: Flush timeout sending flush signal")
	}
}

// Shutdown gracefully shuts down the tracer
func (l *LangfuseTracer) Shutdown() {
	close(l.stopCh)
	l.wg.Wait()
	close(l.eventQueue)
}

// EmitEvent processes an agent event and takes appropriate tracing actions
func (l *LangfuseTracer) EmitEvent(event AgentEvent) error {
	v2Logger := l.getV2Logger()
	eventType := event.GetType()
	v2Logger.Info("Langfuse: Processing event",
		loggerv2.String("type", eventType),
		loggerv2.String("correlation_id", event.GetCorrelationID()))

	switch eventType {
	case EventTypeAgentStart:
		return l.handleAgentStart(event)
	case EventTypeAgentEnd:
		return l.handleAgentEnd(event)
	case EventTypeAgentError:
		return l.handleAgentError(event)
	case EventTypeConversationStart:
		return l.handleConversationStart(event)
	case EventTypeConversationEnd:
		return l.handleConversationEnd(event)
	case EventTypeUnifiedCompletion:
		return l.handleConversationEnd(event) // UnifiedCompletionEvent is handled the same way
	case EventTypeLLMGenerationStart:
		return l.handleLLMGenerationStart(event)
	case EventTypeLLMGenerationEnd:
		return l.handleLLMGenerationEnd(event)
	case EventTypeToolCallStart:
		return l.handleToolCallStart(event)
	case EventTypeToolCallEnd:
		return l.handleToolCallEnd(event)
	case EventTypeToolCallError:
		return l.handleToolCallError(event)
	case EventTypeTokenUsage:
		return l.handleTokenUsage(event)

	// MCP Server connection events
	case EventTypeMCPServerConnectionStart:
		return l.handleMCPServerConnectionStart(event)
	case EventTypeMCPServerConnectionEnd:
		return l.handleMCPServerConnectionEnd(event)
	case EventTypeMCPServerSelection:
		return l.handleMCPServerSelection(event)

	// LLM Error events
	case EventTypeLLMGenerationError:
		return l.handleLLMGenerationError(event)

	// Error & Retry events
	case EventTypeRetryAttempt:
		return l.handleRetryAttempt(event)
	case EventTypeMaxTurnsReached:
		return l.handleMaxTurnsReached(event)

	// Streaming events
	case EventTypeStreamingStart:
		return l.handleStreamingStart(event)
	case EventTypeStreamingEnd:
		return l.handleStreamingEnd(event)

	default:
		v2Logger.Debug("Langfuse: Unhandled event type", loggerv2.String("type", event.GetType()))
		return nil
	}
}

// EmitLLMEvent handles LLM events by forwarding them to the primary tracer
func (l *LangfuseTracer) EmitLLMEvent(event LLMEvent) error {
	v2Logger := l.getV2Logger()
	// For now, just log that we received an LLM event
	// In the future, we could implement specific LLM event handling
	v2Logger.Debug("Langfuse: Received LLM event",
		loggerv2.String("model", event.GetModelID()),
		loggerv2.String("provider", event.GetProvider()))
	return nil
}

// handleAgentStart creates a new trace and agent execution span
func (l *LangfuseTracer) handleAgentStart(event AgentEvent) error {
	traceID := event.GetTraceID()

	// Generate meaningful trace name from user query
	traceName := GenerateTraceName(event.GetData())

	// Create trace in Langfuse
	trace := &langfuseTrace{
		ID:        traceID,
		Name:      traceName,
		Input:     event.GetData(),
		Timestamp: time.Now(),
		Metadata: map[string]interface{}{
			"event_type": "agent_start",
			"agent_mode": "simple", // Will be updated when we have more context
		},
	}

	// Store trace
	l.mu.Lock()
	l.traces[traceID] = trace
	l.mu.Unlock()

	// Generate informative span name based on event data
	spanName := GenerateAgentSpanName(event.GetData())
	agentObsID := l.StartObservation(traceID, "SPAN", spanName, event.GetData())

	// Store the agent span ID for this trace to maintain hierarchy
	l.mu.Lock()
	l.agentSpans[traceID] = string(agentObsID)
	l.mu.Unlock()

	v2Logger := l.getV2Logger()
	v2Logger.Debug("Langfuse: Created trace and agent observation",
		loggerv2.String("trace_id", traceID),
		loggerv2.String("span_name", spanName),
		loggerv2.String("agent_obs_id", string(agentObsID)))

	// Don't send trace to Langfuse yet - wait for ConversationStart to get the question
	// The trace will be sent in handleConversationStart with the proper name
	// This fixes the issue where trace was created with default "agent_conversation" name

	return nil
}

// handleAgentEnd updates the existing agent span with final output and updates trace
func (l *LangfuseTracer) handleAgentEnd(event AgentEvent) error {
	traceID := event.GetTraceID()

	// Get the agent span ID for this trace
	l.mu.RLock()
	agentSpanID := l.agentSpans[traceID]
	l.mu.RUnlock()

	// Extract final result from event data
	finalResult := ExtractFinalResult(event.GetData())
	v2Logger := l.getV2Logger()

	if finalResult == "" {
		v2Logger.Debug("Langfuse: No final result found in agent end event",
			loggerv2.String("trace_id", traceID),
			loggerv2.String("event_data_type", fmt.Sprintf("%T", event.GetData())))
		// Still end the agent span even if no result
		if agentSpanID != "" {
			l.EndSpan(SpanID(agentSpanID), event.GetData(), nil)
		}
		return nil
	}

	// Update existing agent span with final output
	if agentSpanID != "" {
		l.EndSpan(SpanID(agentSpanID), finalResult, nil)
		v2Logger.Info("Langfuse: Updated agent span with final output",
			loggerv2.String("span_id", agentSpanID),
			loggerv2.Int("result_length", len(finalResult)))
	} else {
		v2Logger.Warn("Langfuse: No agent span found for trace, cannot set output",
			loggerv2.String("trace_id", traceID))
	}

	// Also update trace output using EndTrace (which properly handles trace updates)
	l.EndTrace(TraceID(traceID), finalResult)
	v2Logger.Info("Langfuse: Updated trace with final output from agent end",
		loggerv2.String("trace_id", traceID),
		loggerv2.String("final_result", finalResult),
		loggerv2.Int("result_length", len(finalResult)))

	return nil
}

// handleAgentError creates a new span for agent error
func (l *LangfuseTracer) handleAgentError(event AgentEvent) error {
	// Create a new span for agent error instead of trying to find previous one
	spanID := l.StartSpan(event.GetTraceID(), "agent_error", event.GetData())

	// Extract error from event data using type-safe switch
	var err error
	switch data := event.GetData().(type) {
	case *events.AgentErrorEvent:
		err = fmt.Errorf("%s", data.Error)
	default:
		// Unknown type - no error extracted
		v2Logger := l.getV2Logger()
		v2Logger.Debug("Langfuse: handleAgentError - unknown event data type",
			loggerv2.String("type", fmt.Sprintf("%T", event.GetData())))
	}

	// End the span immediately with error
	l.EndSpan(spanID, event.GetData(), err)

	v2Logger := l.getV2Logger()
	v2Logger.Debug("Langfuse: Created and ended agent error span",
		loggerv2.String("span_id", string(spanID)))
	return nil
}

// handleConversationStart creates a new conversation span as child of agent span
func (l *LangfuseTracer) handleConversationStart(event AgentEvent) error {
	traceID := event.GetTraceID()

	// Update trace name and input from question (now available in ConversationStartEvent)
	// This fixes the issue where trace was created with default "agent_conversation" name
	// and metadata-only input before the question was available
	l.mu.Lock()
	trace, exists := l.traces[traceID]
	if exists {
		// Generate meaningful trace name from question
		newTraceName := GenerateTraceName(event.GetData())
		trace.Name = newTraceName

		// Extract the actual user question for trace input
		var userQuestion string
		switch data := event.GetData().(type) {
		case *events.ConversationStartEvent:
			userQuestion = data.Question
		case *events.ConversationTurnEvent:
			userQuestion = data.Question
		case *events.UserMessageEvent:
			userQuestion = data.Content
		}

		// Set trace input to the actual user question (not just metadata)
		if userQuestion != "" {
			trace.Input = userQuestion
		}

		v2Logger := l.getV2Logger()
		v2Logger.Debug("Langfuse: Setting trace name and input from conversation start",
			loggerv2.String("trace_id", traceID),
			loggerv2.String("trace_name", newTraceName),
			loggerv2.Int("input_length", len(userQuestion)))

		// Now send the trace to Langfuse with the correct name and input
		// (We delayed sending it in handleAgentStart to wait for the question)
		traceEvent := &langfuseEvent{
			ID:        generateID(),
			Type:      "trace-create",
			Timestamp: time.Now(),
			Body:      trace,
		}

		select {
		case l.eventQueue <- traceEvent:
			v2Logger.Debug("Langfuse: Queued trace creation event with question-based name and input")
		default:
			v2Logger.Warn("Langfuse: Event queue full, dropping trace creation event")
		}
	}
	l.mu.Unlock()

	// Get the agent span ID for this trace to use as parent
	l.mu.RLock()
	parentSpanID := l.agentSpans[traceID]
	l.mu.RUnlock()

	// If no agent span ID found, use trace ID as fallback
	if parentSpanID == "" {
		parentSpanID = traceID
		v2Logger := l.getV2Logger()
		v2Logger.Warn("Langfuse: No agent span found for trace, using trace ID as parent",
			loggerv2.String("trace_id", traceID))
	}

	// Generate informative span name based on event data
	spanName := GenerateConversationSpanName(event.GetData())
	conversationSpanID := l.StartSpan(parentSpanID, spanName, event.GetData())

	// Store the conversation span ID for this trace to maintain hierarchy
	l.mu.Lock()
	l.conversationSpans[traceID] = string(conversationSpanID)
	l.mu.Unlock()

	v2Logger := l.getV2Logger()
	v2Logger.Debug("Langfuse: Started conversation span",
		loggerv2.String("span_name", spanName),
		loggerv2.String("conversation_span_id", string(conversationSpanID)),
		loggerv2.String("parent", parentSpanID))
	return nil
}

// handleConversationEnd updates the existing conversation span with final output and updates trace
func (l *LangfuseTracer) handleConversationEnd(event AgentEvent) error {
	traceID := event.GetTraceID()

	// Get the conversation span ID for this trace
	l.mu.RLock()
	conversationSpanID := l.conversationSpans[traceID]
	l.mu.RUnlock()

	// Extract final result from conversation end event
	finalResult := ExtractFinalResult(event.GetData())
	v2Logger := l.getV2Logger()

	if finalResult == "" {
		v2Logger.Debug("Langfuse: No final result found in conversation end event",
			loggerv2.String("trace_id", traceID),
			loggerv2.String("event_data_type", fmt.Sprintf("%T", event.GetData())))
		// Still end the conversation span even if no result
		if conversationSpanID != "" {
			l.EndSpan(SpanID(conversationSpanID), event.GetData(), nil)
		}
		return nil
	}

	// Update existing conversation span with final output
	if conversationSpanID != "" {
		l.EndSpan(SpanID(conversationSpanID), finalResult, nil)
		v2Logger.Info("Langfuse: Updated conversation span with final output",
			loggerv2.String("span_id", conversationSpanID),
			loggerv2.Int("result_length", len(finalResult)))
	} else {
		v2Logger.Warn("Langfuse: No conversation span found for trace, cannot set output",
			loggerv2.String("trace_id", traceID))
	}

	// Also update trace output using EndTrace (which properly handles trace updates)
	l.EndTrace(TraceID(traceID), finalResult)
	v2Logger.Info("Langfuse: Updated trace with final output from conversation end",
		loggerv2.String("trace_id", traceID),
		loggerv2.String("final_result", finalResult),
		loggerv2.Int("result_length", len(finalResult)))

	return nil
}

// handleLLMGenerationStart creates a new LLM generation span as child of conversation span
func (l *LangfuseTracer) handleLLMGenerationStart(event AgentEvent) error {
	traceID := event.GetTraceID()

	// Get the conversation span ID for this trace to use as parent
	l.mu.RLock()
	parentSpanID := l.conversationSpans[traceID]
	l.mu.RUnlock()

	// If no conversation span ID found, use trace ID as fallback
	if parentSpanID == "" {
		parentSpanID = traceID
		v2Logger := l.getV2Logger()
		v2Logger.Warn("Langfuse: No conversation span found for trace, using trace ID as parent",
			loggerv2.String("trace_id", traceID))
	}

	// Generate informative span name based on event data
	spanName := GenerateLLMSpanName(event.GetData())
	llmGenerationID := l.StartObservation(parentSpanID, "GENERATION", spanName, event.GetData())

	// Extract model and configuration from LLMGenerationStartEvent
	var modelID string
	var modelParameters map[string]interface{}
	if startEvent, ok := event.GetData().(*events.LLMGenerationStartEvent); ok {
		modelID = startEvent.ModelID

		// Build model parameters map for Langfuse
		modelParameters = map[string]interface{}{
			"temperature":    startEvent.Temperature,
			"tools_count":    startEvent.ToolsCount,
			"turn":           startEvent.Turn,
			"messages_count": startEvent.MessagesCount,
		}

		l.mu.Lock()
		if obs, exists := l.spans[string(llmGenerationID)]; exists {
			obs.Model = modelID
			obs.ModelParameters = modelParameters
		}
		l.mu.Unlock()
	}

	// Store the LLM generation span ID for this trace to maintain hierarchy
	l.mu.Lock()
	l.llmGenerationSpans[traceID] = string(llmGenerationID)
	l.mu.Unlock()

	v2Logger := l.getV2Logger()
	v2Logger.Debug("Langfuse: Started LLM generation observation",
		loggerv2.String("span_name", spanName),
		loggerv2.String("llm_generation_id", string(llmGenerationID)),
		loggerv2.String("parent", parentSpanID),
		loggerv2.String("model", modelID),
		loggerv2.Any("model_parameters", modelParameters))
	return nil
}

// handleLLMGenerationEnd updates the existing GENERATION span with token usage and completion data
func (l *LangfuseTracer) handleLLMGenerationEnd(event AgentEvent) error {
	traceID := event.GetTraceID()

	// Get the LLM generation span ID for this trace
	l.mu.RLock()
	generationSpanID := l.llmGenerationSpans[traceID]
	l.mu.RUnlock()

	// If no generation span found, log warning and return (should not happen)
	if generationSpanID == "" {
		v2Logger := l.getV2Logger()
		v2Logger.Warn("Langfuse: No generation span found for LLMGenerationEnd event",
			loggerv2.String("trace_id", traceID))
		return nil
	}

	// Extract usage metrics from event data
	var usageMetrics UsageMetrics
	var metadata map[string]interface{}

	eventData := event.GetData()
	if llmEndEvent, ok := eventData.(*events.LLMGenerationEndEvent); ok {
		// Extract usage metrics from LLMGenerationEndEvent
		usageMetrics = UsageMetrics{
			InputTokens:  llmEndEvent.UsageMetrics.PromptTokens,
			OutputTokens: llmEndEvent.UsageMetrics.CompletionTokens,
			TotalTokens:  llmEndEvent.UsageMetrics.TotalTokens,
			Unit:         "TOKENS",
		}

		// Create metadata from event
		metadata = map[string]interface{}{
			"turn":       llmEndEvent.Turn,
			"content":    llmEndEvent.Content,
			"tool_calls": llmEndEvent.ToolCalls,
			"duration":   llmEndEvent.Duration.String(),
		}
	} else {
		// Fallback: try to extract from generic event data
		metadata = make(map[string]interface{})
		if eventData != nil {
			metadata["event_data"] = eventData
		}
	}

	// Update the existing generation span with token usage
	l.EndGenerationSpan(SpanID(generationSpanID), metadata, usageMetrics, nil)

	v2Logger := l.getV2Logger()
	v2Logger.Debug("Langfuse: Updated generation span with token usage",
		loggerv2.String("span_id", generationSpanID),
		loggerv2.Int("input_tokens", usageMetrics.InputTokens),
		loggerv2.Int("output_tokens", usageMetrics.OutputTokens),
		loggerv2.Int("total_tokens", usageMetrics.TotalTokens))
	return nil
}

// handleToolCallStart creates a new tool call span as child of LLM generation span
func (l *LangfuseTracer) handleToolCallStart(event AgentEvent) error {
	traceID := event.GetTraceID()

	// Get the LLM generation span ID for this trace to use as parent
	l.mu.RLock()
	parentSpanID := l.llmGenerationSpans[traceID]
	l.mu.RUnlock()

	// If no LLM generation span ID found, use trace ID as fallback
	if parentSpanID == "" {
		parentSpanID = traceID
		v2Logger := l.getV2Logger()
		v2Logger.Warn("Langfuse: No LLM generation span found for trace, using trace ID as parent",
			loggerv2.String("trace_id", traceID))
	}

	// Generate informative span name based on event data
	spanName := GenerateToolSpanName(event.GetData())
	toolID := l.StartObservation(parentSpanID, "SPAN", spanName, event.GetData())

	// Store tool span ID for later completion by handleToolCallEnd
	var toolKey string
	if startEvent, ok := event.GetData().(*events.ToolCallStartEvent); ok {
		if startEvent.ToolCallID != "" {
			toolKey = fmt.Sprintf("%s_%s", traceID, startEvent.ToolCallID)
		} else {
			toolKey = fmt.Sprintf("%s_%d_%s", traceID, startEvent.Turn, startEvent.ToolName)
		}
		l.mu.Lock()
		l.toolCallSpans[toolKey] = string(toolID)
		l.mu.Unlock()
	}

	v2Logger := l.getV2Logger()
	v2Logger.Info("Langfuse: Started tool observation",
		loggerv2.String("tool_key", toolKey),
		loggerv2.String("span_name", spanName),
		loggerv2.String("tool_id", string(toolID)),
		loggerv2.String("parent", parentSpanID))
	return nil
}

// handleToolCallEnd ends the existing tool call span created by handleToolCallStart
func (l *LangfuseTracer) handleToolCallEnd(event AgentEvent) error {
	traceID := event.GetTraceID()
	v2Logger := l.getV2Logger()

	// Find the existing tool span to end
	if endEvent, ok := event.GetData().(*events.ToolCallEndEvent); ok {
		var toolKey string
		if endEvent.ToolCallID != "" {
			toolKey = fmt.Sprintf("%s_%s", traceID, endEvent.ToolCallID)
		} else {
			toolKey = fmt.Sprintf("%s_%d_%s", traceID, endEvent.Turn, endEvent.ToolName)
		}
		l.mu.RLock()
		toolSpanID := l.toolCallSpans[toolKey]
		// Also log all available keys for debugging
		availableKeys := make([]string, 0, len(l.toolCallSpans))
		for k := range l.toolCallSpans {
			availableKeys = append(availableKeys, k)
		}
		l.mu.RUnlock()

		v2Logger.Info("Langfuse: Looking for tool span to end",
			loggerv2.String("tool_key", toolKey),
			loggerv2.String("found_span_id", toolSpanID),
			loggerv2.Any("available_keys", availableKeys))

		if toolSpanID != "" {
			// Build output with result, duration, and additional metadata
			output := map[string]interface{}{
				"result":      endEvent.Result,
				"duration":    endEvent.Duration.String(),
				"server_name": endEvent.ServerName,
			}
			// Add optional fields if present
			if endEvent.ModelID != "" {
				output["model_id"] = endEvent.ModelID
			}
			if endEvent.ContextUsagePercent > 0 {
				output["context_usage_percent"] = endEvent.ContextUsagePercent
			}
			if endEvent.ModelContextWindow > 0 {
				output["model_context_window"] = endEvent.ModelContextWindow
			}
			if endEvent.ContextWindowUsage > 0 {
				output["context_window_usage"] = endEvent.ContextWindowUsage
			}

			// End the existing span with output
			l.EndSpan(SpanID(toolSpanID), output, nil)

			// Clean up
			l.mu.Lock()
			delete(l.toolCallSpans, toolKey)
			l.mu.Unlock()

			v2Logger.Info("Langfuse: Ended tool span",
				loggerv2.String("tool_name", endEvent.ToolName),
				loggerv2.String("span_id", toolSpanID),
				loggerv2.String("duration", endEvent.Duration.String()))
			return nil
		}
	}

	// Fallback: log warning if no existing span found
	v2Logger.Warn("Langfuse: No tool span found to end", loggerv2.String("trace_id", traceID))
	return nil
}

// handleToolCallError ends the existing tool call span created by handleToolCallStart with an error
func (l *LangfuseTracer) handleToolCallError(event AgentEvent) error {
	traceID := event.GetTraceID()
	v2Logger := l.getV2Logger()

	// Find the existing tool span to end with error
	if errorEvent, ok := event.GetData().(*events.ToolCallErrorEvent); ok {
		var toolKey string
		if errorEvent.ToolCallID != "" {
			toolKey = fmt.Sprintf("%s_%s", traceID, errorEvent.ToolCallID)
		} else {
			toolKey = fmt.Sprintf("%s_%d_%s", traceID, errorEvent.Turn, errorEvent.ToolName)
		}
		l.mu.RLock()
		toolSpanID := l.toolCallSpans[toolKey]
		l.mu.RUnlock()

		v2Logger.Info("Langfuse: Looking for tool span to end with error",
			loggerv2.String("tool_key", toolKey),
			loggerv2.String("found_span_id", toolSpanID))

		if toolSpanID != "" {
			// Build output with error info
			output := map[string]interface{}{
				"error":    errorEvent.Error,
				"duration": errorEvent.Duration.String(),
			}

			// End the existing span with error
			l.EndSpan(SpanID(toolSpanID), output, errors.New(errorEvent.Error))

			// Clean up
			l.mu.Lock()
			delete(l.toolCallSpans, toolKey)
			l.mu.Unlock()

			v2Logger.Info("Langfuse: Ended tool span with error",
				loggerv2.String("tool_name", errorEvent.ToolName),
				loggerv2.String("span_id", toolSpanID),
				loggerv2.String("error", errorEvent.Error))
			return nil
		}
	}

	// Fallback: log warning if no existing span found
	v2Logger.Warn("Langfuse: No tool span found to end with error", loggerv2.String("trace_id", traceID))
	return nil
}

// handleTokenUsage extracts token usage information and updates trace metadata
func (l *LangfuseTracer) handleTokenUsage(event AgentEvent) error {
	traceID := event.GetTraceID()
	eventData := event.GetData()

	v2Logger := l.getV2Logger()

	// Extract token usage information from event data using type-safe switch
	var promptTokens, completionTokens, totalTokens, reasoningTokens int
	var modelID, provider, operation string
	var costEstimate, cacheDiscount float64

	// Type switch for concrete event types only
	switch data := eventData.(type) {
	case *events.TokenUsageEvent:
		promptTokens = data.PromptTokens
		completionTokens = data.CompletionTokens
		totalTokens = data.TotalTokens
		reasoningTokens = data.ReasoningTokens
		modelID = data.ModelID
		provider = data.Provider
		operation = data.Operation
		costEstimate = data.CostEstimate
		cacheDiscount = data.CacheDiscount
	default:
		// Unknown type - log and use defaults
		v2Logger.Debug("Langfuse: handleTokenUsage - unknown event data type",
			loggerv2.String("type", fmt.Sprintf("%T", eventData)))
	}

	// Log token usage information
	v2Logger.Info("Langfuse: Token usage",
		loggerv2.String("trace_id", traceID),
		loggerv2.String("model_id", modelID),
		loggerv2.String("provider", provider),
		loggerv2.String("operation", operation),
		loggerv2.Int("prompt_tokens", promptTokens),
		loggerv2.Int("completion_tokens", completionTokens),
		loggerv2.Int("total_tokens", totalTokens),
		loggerv2.Int("reasoning_tokens", reasoningTokens),
		loggerv2.Any("cost_estimate", costEstimate),
		loggerv2.Any("cache_discount", cacheDiscount))

	// Update trace metadata with token usage information
	l.mu.Lock()
	if trace, exists := l.traces[traceID]; exists {
		if trace.Metadata == nil {
			trace.Metadata = make(map[string]interface{})
		}

		// Initialize token_usage in metadata if not exists
		tokenUsage, ok := trace.Metadata["token_usage"].(map[string]interface{})
		if !ok {
			tokenUsage = make(map[string]interface{})
			trace.Metadata["token_usage"] = tokenUsage
		}

		// Accumulate token counts (add to existing totals)
		if existingTotal, ok := tokenUsage["total_tokens"].(float64); ok {
			totalTokens += int(existingTotal)
		}
		if existingPrompt, ok := tokenUsage["prompt_tokens"].(float64); ok {
			promptTokens += int(existingPrompt)
		}
		if existingCompletion, ok := tokenUsage["completion_tokens"].(float64); ok {
			completionTokens += int(existingCompletion)
		}
		if existingReasoning, ok := tokenUsage["reasoning_tokens"].(float64); ok {
			reasoningTokens += int(existingReasoning)
		}

		// Update token usage metadata
		tokenUsage["prompt_tokens"] = promptTokens
		tokenUsage["completion_tokens"] = completionTokens
		tokenUsage["total_tokens"] = totalTokens
		if reasoningTokens > 0 {
			tokenUsage["reasoning_tokens"] = reasoningTokens
		}
		if modelID != "" {
			tokenUsage["model_id"] = modelID
		}
		if provider != "" {
			tokenUsage["provider"] = provider
		}
		if costEstimate > 0 {
			tokenUsage["cost_estimate"] = costEstimate
		}
		if cacheDiscount > 0 {
			tokenUsage["cache_discount"] = cacheDiscount
		}

		// Ensure trace ID is set correctly in the body
		trace.ID = traceID

		// Send trace update to Langfuse with updated metadata
		traceUpdateEvent := &langfuseEvent{
			ID:        generateID(),
			Type:      "trace-create", // trace-create with same trace ID updates the trace
			Timestamp: time.Now(),
			Body:      trace,
		}

		select {
		case l.eventQueue <- traceUpdateEvent:
			v2Logger.Info("Langfuse: Queued trace token usage update event",
				loggerv2.String("trace_id", traceID),
				loggerv2.Int("total_tokens", totalTokens),
				loggerv2.Int("prompt_tokens", promptTokens),
				loggerv2.Int("completion_tokens", completionTokens))
		default:
			v2Logger.Warn("Langfuse: Event queue full, dropping trace token usage update event")
		}
	}
	l.mu.Unlock()

	// Token usage is now tracked on generation spans via handleLLMGenerationEnd
	// No need to create a separate span - this would confuse Langfuse
	// The trace metadata update above is sufficient for aggregate tracking

	return nil
}

// MCP Server connection event handlers

// handleMCPServerConnectionStart creates a new span for MCP server connection start
func (l *LangfuseTracer) handleMCPServerConnectionStart(event AgentEvent) error {
	traceID := event.GetTraceID()

	// Get agent span as parent for proper hierarchy
	l.mu.RLock()
	parentSpanID := l.agentSpans[traceID]
	l.mu.RUnlock()
	if parentSpanID == "" {
		parentSpanID = traceID
	}

	// Extract server name for informative span name
	spanName := "mcp_connection_start"
	var serverName string
	// Try MCPServerConnectionEvent first (what the agent actually emits)
	if connEvent, ok := event.GetData().(*events.MCPServerConnectionEvent); ok {
		serverName = connEvent.ServerName
		if serverName != "" {
			spanName = fmt.Sprintf("mcp_connection_%s", serverName)
		}
	} else if startEvent, ok := event.GetData().(*events.MCPServerConnectionStartEvent); ok {
		// Fallback to MCPServerConnectionStartEvent
		serverName = startEvent.ServerName
		if serverName != "" {
			spanName = fmt.Sprintf("mcp_connection_%s", serverName)
		}
	}

	// Create span with agent as parent
	spanID := l.StartSpan(parentSpanID, spanName, event.GetData())

	// Store for later completion by handleMCPServerConnectionEnd
	if serverName != "" {
		l.mu.Lock()
		l.mcpConnectionSpans[serverName] = string(spanID)
		l.mu.Unlock()
	}

	v2Logger := l.getV2Logger()
	v2Logger.Debug("Langfuse: Created MCP server connection span",
		loggerv2.String("span_id", string(spanID)),
		loggerv2.String("server_name", serverName),
		loggerv2.String("trace_id", traceID))

	return nil
}

// handleMCPServerConnectionEnd ends the existing MCP server connection span
func (l *LangfuseTracer) handleMCPServerConnectionEnd(event AgentEvent) error {
	traceID := event.GetTraceID()
	v2Logger := l.getV2Logger()

	// Extract server info from the event - try MCPServerConnectionEvent first (what agent actually emits)
	var serverName string
	var toolCount int
	var toolNames []string
	var connectionTime time.Duration
	var output map[string]interface{}

	if connEvent, ok := event.GetData().(*events.MCPServerConnectionEvent); ok {
		serverName = connEvent.ServerName
		toolCount = connEvent.ToolsCount
		connectionTime = connEvent.ConnectionTime
		output = map[string]interface{}{
			"server_name":     serverName,
			"tool_count":      toolCount,
			"connection_time": connectionTime.String(),
			"status":          connEvent.Status,
		}
		if connEvent.ServerInfo != nil {
			output["server_info"] = connEvent.ServerInfo
		}
	} else if endEvent, ok := event.GetData().(*events.MCPServerConnectionEndEvent); ok {
		serverName = endEvent.ServerName
		toolCount = endEvent.ToolCount
		toolNames = endEvent.ToolNames
		output = map[string]interface{}{
			"server_name": serverName,
			"tool_count":  toolCount,
			"tool_names":  toolNames,
			"duration":    endEvent.Duration,
		}
	}

	// Find and end existing span
	if serverName != "" {
		l.mu.RLock()
		spanID := l.mcpConnectionSpans[serverName]
		l.mu.RUnlock()

		if spanID != "" {
			l.EndSpan(SpanID(spanID), output, nil)

			// Clean up
			l.mu.Lock()
			delete(l.mcpConnectionSpans, serverName)
			l.mu.Unlock()

			v2Logger.Debug("Langfuse: Ended MCP server connection span",
				loggerv2.String("span_id", spanID),
				loggerv2.String("server_name", serverName),
				loggerv2.Int("tool_count", toolCount),
				loggerv2.String("connection_time", connectionTime.String()))
			return nil
		}
	}

	// Fallback: create point-in-time span if no matching start
	l.mu.RLock()
	parentSpanID := l.agentSpans[traceID]
	l.mu.RUnlock()
	if parentSpanID == "" {
		parentSpanID = traceID
	}

	spanName := "mcp_connection_end"
	if serverName != "" {
		spanName = fmt.Sprintf("mcp_connection_%s_end", serverName)
	}
	spanID := l.StartSpan(parentSpanID, spanName, event.GetData())
	l.EndSpan(spanID, output, nil)

	v2Logger.Warn("Langfuse: No MCP connection span found to end, created point-in-time span",
		loggerv2.String("trace_id", traceID),
		loggerv2.String("server_name", serverName))

	return nil
}

// handleMCPServerSelection creates a new span for MCP server selection
func (l *LangfuseTracer) handleMCPServerSelection(event AgentEvent) error {
	traceID := event.GetTraceID()

	// Create a new span for MCP server selection
	spanID := l.StartSpan(traceID, "mcp_server_selection", event.GetData())

	// End the span immediately since selection is a point-in-time event
	l.EndSpan(spanID, event.GetData(), nil)

	v2Logger := l.getV2Logger()
	v2Logger.Debug("Langfuse: Created MCP server selection span",
		loggerv2.String("span_id", string(spanID)),
		loggerv2.String("trace_id", traceID))

	return nil
}

// ============================================================================
// LLM Error Handlers
// ============================================================================

// handleLLMGenerationError creates an error span for LLM generation failures
func (l *LangfuseTracer) handleLLMGenerationError(event AgentEvent) error {
	traceID := event.GetTraceID()
	v2Logger := l.getV2Logger()

	// Get the LLM generation span ID to end it with error
	l.mu.RLock()
	generationSpanID := l.llmGenerationSpans[traceID]
	l.mu.RUnlock()

	var output map[string]interface{}
	var err error
	if errorEvent, ok := event.GetData().(*events.LLMGenerationErrorEvent); ok {
		output = map[string]interface{}{
			"turn":     errorEvent.Turn,
			"model_id": errorEvent.ModelID,
			"error":    errorEvent.Error,
			"duration": errorEvent.Duration.String(),
		}
		err = fmt.Errorf("%s", errorEvent.Error)
	}

	if generationSpanID != "" {
		l.EndSpan(SpanID(generationSpanID), output, err)
		v2Logger.Info("Langfuse: Ended LLM generation span with error",
			loggerv2.String("span_id", generationSpanID),
			loggerv2.String("trace_id", traceID))
	} else {
		// Create point-in-time error span
		spanID := l.StartSpan(traceID, "llm_generation_error", event.GetData())
		l.EndSpan(spanID, output, err)
		v2Logger.Info("Langfuse: Created LLM generation error span",
			loggerv2.String("span_id", string(spanID)),
			loggerv2.String("trace_id", traceID))
	}

	return nil
}

// ============================================================================
// Error & Retry Handlers
// ============================================================================

// handleRetryAttempt creates a span for each retry attempt
func (l *LangfuseTracer) handleRetryAttempt(event AgentEvent) error {
	traceID := event.GetTraceID()

	spanName := "retry_attempt"
	var output map[string]interface{}
	var err error
	if fbEvent, ok := event.GetData().(*events.RetryAttemptEvent); ok {
		spanName = fmt.Sprintf("retry_attempt_%d_%s", fbEvent.AttemptIndex, fbEvent.ModelID)
		output = map[string]interface{}{
			"turn":           fbEvent.Turn,
			"attempt_index":  fbEvent.AttemptIndex,
			"total_attempts": fbEvent.TotalAttempts,
			"model_id":       fbEvent.ModelID,
			"provider":       fbEvent.Provider,
			"phase":          fbEvent.Phase,
			"success":        fbEvent.Success,
			"duration":       fbEvent.Duration,
		}
		if fbEvent.Error != "" {
			output["error"] = fbEvent.Error
			err = fmt.Errorf("%s", fbEvent.Error)
		}
	}

	spanID := l.StartSpan(traceID, spanName, event.GetData())
	l.EndSpan(spanID, output, err)

	v2Logger := l.getV2Logger()
	v2Logger.Debug("Langfuse: Created retry attempt span",
		loggerv2.String("span_id", string(spanID)),
		loggerv2.String("trace_id", traceID))

	return nil
}

// handleMaxTurnsReached creates a span for max turns reached events
func (l *LangfuseTracer) handleMaxTurnsReached(event AgentEvent) error {
	traceID := event.GetTraceID()

	spanID := l.StartSpan(traceID, "max_turns_reached", event.GetData())
	l.EndSpan(spanID, event.GetData(), nil)

	v2Logger := l.getV2Logger()
	v2Logger.Info("Langfuse: Created max turns reached span",
		loggerv2.String("span_id", string(spanID)),
		loggerv2.String("trace_id", traceID))

	return nil
}

// ============================================================================
// Streaming Handlers
// ============================================================================

// handleStreamingStart creates a span for streaming start
func (l *LangfuseTracer) handleStreamingStart(event AgentEvent) error {
	traceID := event.GetTraceID()

	// Get LLM generation span as parent
	l.mu.RLock()
	parentSpanID := l.llmGenerationSpans[traceID]
	l.mu.RUnlock()
	if parentSpanID == "" {
		parentSpanID = traceID
	}

	spanID := l.StartSpan(parentSpanID, "streaming", event.GetData())

	// Store for later completion
	l.mu.Lock()
	l.mcpConnectionSpans["streaming_"+traceID] = string(spanID)
	l.mu.Unlock()

	v2Logger := l.getV2Logger()
	v2Logger.Debug("Langfuse: Started streaming span",
		loggerv2.String("span_id", string(spanID)),
		loggerv2.String("trace_id", traceID))

	return nil
}

// handleStreamingEnd ends the streaming span
func (l *LangfuseTracer) handleStreamingEnd(event AgentEvent) error {
	traceID := event.GetTraceID()
	v2Logger := l.getV2Logger()

	spanKey := "streaming_" + traceID
	l.mu.RLock()
	spanID := l.mcpConnectionSpans[spanKey]
	l.mu.RUnlock()

	var output map[string]interface{}
	if seEvent, ok := event.GetData().(*events.StreamingEndEvent); ok {
		output = map[string]interface{}{
			"total_chunks":  seEvent.TotalChunks,
			"total_tokens":  seEvent.TotalTokens,
			"duration":      seEvent.Duration,
			"finish_reason": seEvent.FinishReason,
		}
	}

	if spanID != "" {
		l.EndSpan(SpanID(spanID), output, nil)
		l.mu.Lock()
		delete(l.mcpConnectionSpans, spanKey)
		l.mu.Unlock()
	} else {
		newSpanID := l.StartSpan(traceID, "streaming_completed", event.GetData())
		l.EndSpan(newSpanID, output, nil)
	}

	v2Logger.Debug("Langfuse: Ended streaming span",
		loggerv2.String("trace_id", traceID))

	return nil
}

// ============================================================================
// Cache Handlers
// ============================================================================
