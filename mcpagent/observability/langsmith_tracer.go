//go:build !langsmith_disabled

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"

	"github.com/joho/godotenv"

	"github.com/manishiitg/mcpagent/events"
)

// LangsmithTracer implements the Tracer interface using LangSmith API.
// Follows the same pattern as LangfuseTracer for consistency.
type LangsmithTracer struct {
	client  *http.Client
	host    string
	apiKey  string
	project string
	debug   bool

	// Shared state for all instances
	traces map[string]*langsmithRun
	runs   map[string]*langsmithRun

	// External trace ID to LangSmith UUID mapping
	// LangSmith requires UUIDs for trace_id and run IDs
	traceIDToUUID map[string]string // external trace ID -> LangSmith UUID

	// Hierarchy tracking: traceID -> runID mappings
	agentRuns         map[string]string // traceID -> agent run ID
	conversationRuns  map[string]string // traceID -> conversation run ID
	llmGenerationRuns map[string]string // traceID -> current LLM generation run ID
	toolCallRuns      map[string]string // {traceID}_{turn}_{toolName} -> tool run ID
	mcpConnectionRuns map[string]string // serverName -> mcp connection run ID

	mu sync.RWMutex

	// Background processing
	postQueue  chan *langsmithRun // Queue for new runs (POST)
	patchQueue chan *langsmithRun // Queue for run updates (PATCH)
	flushCh    chan chan struct{} // Channel to signal flush and wait for completion
	stopCh     chan struct{}
	wg         sync.WaitGroup

	logger loggerv2.Logger
}

// Shared state across all instances (similar to Python class-level variables)
var (
	sharedLangsmithClient *LangsmithTracer
	langsmithInitialized  bool
	langsmithMutex        sync.Mutex
)

// langsmithRun represents a run in LangSmith API format
type langsmithRun struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	RunType       string                 `json:"run_type"` // "llm", "chain", "tool", "retriever"
	StartTime     time.Time              `json:"start_time"`
	EndTime       *time.Time             `json:"end_time,omitempty"`
	Inputs        map[string]interface{} `json:"inputs,omitempty"`
	Outputs       map[string]interface{} `json:"outputs,omitempty"`
	ParentRunID   string                 `json:"parent_run_id,omitempty"`
	SessionName   string                 `json:"session_name,omitempty"` // Maps to project
	Tags          []string               `json:"tags,omitempty"`
	Extra         map[string]interface{} `json:"extra,omitempty"`
	Error         string                 `json:"error,omitempty"`
	Serialized    map[string]interface{} `json:"serialized,omitempty"`
	Events        []langsmithEvent       `json:"events,omitempty"`
	InputsS3URLs  map[string]string      `json:"inputs_s3_urls,omitempty"`
	OutputsS3URLs map[string]string      `json:"outputs_s3_urls,omitempty"`
	TraceID       string                 `json:"trace_id,omitempty"`     // Root run ID for hierarchy
	DottedOrder   string                 `json:"dotted_order,omitempty"` // Ordering for nested runs

	// LLM-specific fields (included in extra.invocation_params or tokens)
	Model            string `json:"-"` // Stored separately, added to extra
	PromptTokens     int    `json:"-"`
	CompletionTokens int    `json:"-"`
	TotalTokens      int    `json:"-"`
}

// langsmithEvent represents an event within a run
type langsmithEvent struct {
	Name   string                 `json:"name"`
	Time   time.Time              `json:"time"`
	Kwargs map[string]interface{} `json:"kwargs,omitempty"`
}

// langsmithBatchPayload represents the batch ingestion payload
type langsmithBatchPayload struct {
	Post  []*langsmithRun `json:"post,omitempty"`  // New runs to create
	Patch []*langsmithRun `json:"patch,omitempty"` // Existing runs to update
}

// newLangsmithTracerWithLogger creates a new LangSmith tracer with an injected logger
func newLangsmithTracerWithLogger(logger loggerv2.Logger) (Tracer, error) {
	langsmithMutex.Lock()
	defer langsmithMutex.Unlock()

	if !langsmithInitialized {
		if err := initializeSharedLangsmithClientWithLogger(logger); err != nil {
			langsmithInitialized = true // Mark as initialized even on failure to prevent retry loops
			return nil, err
		}
		langsmithInitialized = true
	}

	if sharedLangsmithClient == nil {
		return nil, errors.New("failed to initialize shared LangSmith client")
	}

	return sharedLangsmithClient, nil
}

// NewLangsmithTracerWithLogger creates a new LangSmith tracer with an injected logger
func NewLangsmithTracerWithLogger(logger loggerv2.Logger) (Tracer, error) {
	return newLangsmithTracerWithLogger(logger)
}

// initializeSharedLangsmithClientWithLogger initializes the shared LangSmith client with an injected logger
func initializeSharedLangsmithClientWithLogger(logger loggerv2.Logger) error {
	// Auto-load .env file if present
	if _, err := os.Stat(".env"); err == nil {
		if err := godotenv.Load(); err != nil {
			log.Printf("Warning: Could not load .env file: %v", err)
		}
	}

	// Load credentials from environment
	apiKey := os.Getenv("LANGSMITH_API_KEY")
	host := os.Getenv("LANGSMITH_ENDPOINT")
	project := os.Getenv("LANGSMITH_PROJECT")

	if host == "" {
		host = "https://api.smith.langchain.com"
	}

	if project == "" {
		project = "default"
	}

	if apiKey == "" {
		return fmt.Errorf("LangSmith credentials missing. Required environment variables:\n"+
			"- LANGSMITH_API_KEY\n"+
			"- LANGSMITH_ENDPOINT (optional, default: %s)\n"+
			"- LANGSMITH_PROJECT (optional, default: %s)", host, project)
	}

	// Always enable debug for comprehensive observability
	debug := true

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	tracer := &LangsmithTracer{
		client:            client,
		host:              host,
		apiKey:            apiKey,
		project:           project,
		debug:             debug,
		traces:            make(map[string]*langsmithRun),
		runs:              make(map[string]*langsmithRun),
		traceIDToUUID:     make(map[string]string),
		agentRuns:         make(map[string]string),
		conversationRuns:  make(map[string]string),
		llmGenerationRuns: make(map[string]string),
		toolCallRuns:      make(map[string]string),
		mcpConnectionRuns: make(map[string]string),
		postQueue:         make(chan *langsmithRun, 1000),
		patchQueue:        make(chan *langsmithRun, 1000),
		flushCh:           make(chan chan struct{}),
		stopCh:            make(chan struct{}),
		logger:            logger,
	}

	// Test authentication
	if err := tracer.authCheck(); err != nil {
		return fmt.Errorf("LangSmith authentication failed: %w", err)
	}

	// Start background event processor
	tracer.wg.Add(1)
	go tracer.eventProcessor()

	sharedLangsmithClient = tracer

	if tracer.debug {
		tracer.logger.Info("LangSmith: Authentication successful",
			loggerv2.String("project", project),
			loggerv2.String("host", host))
	}

	return nil
}

// authCheck verifies authentication with LangSmith API
func (l *LangsmithTracer) authCheck() error {
	req, err := http.NewRequest("GET", l.host+"/info", nil)
	if err != nil {
		return err
	}

	req.Header.Set("X-API-Key", l.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("authentication failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// StartTrace starts a new trace (root run) using LangSmith API pattern
func (l *LangsmithTracer) StartTrace(name string, input interface{}) TraceID {
	id := generateID()
	now := time.Now()

	run := &langsmithRun{
		ID:          id,
		Name:        name,
		RunType:     "chain", // Root runs are chains
		StartTime:   now,
		SessionName: l.project,
		TraceID:     id, // Root run is its own trace
		DottedOrder: fmt.Sprintf("%sZ%s", strings.Replace(now.Format("20060102T150405.000000"), ".", "", 1), id),
		Inputs: map[string]interface{}{
			"input": input,
		},
		Extra: map[string]interface{}{
			"metadata": map[string]interface{}{
				"event_type": "trace_start",
			},
		},
	}

	l.mu.Lock()
	l.traces[id] = run
	l.runs[id] = run
	l.mu.Unlock()

	// Queue run creation
	select {
	case l.postQueue <- run:
	default:
		l.logger.Error("LangSmith: Post queue full, dropping trace-create event", nil)
	}

	l.logger.Info("LangSmith: Started trace",
		loggerv2.String("name", name),
		loggerv2.String("id", id))

	return TraceID(id)
}

// EndTrace ends a trace (root run) with output
func (l *LangsmithTracer) EndTrace(traceID TraceID, output interface{}) {
	l.mu.Lock()
	run, exists := l.runs[string(traceID)]
	if !exists {
		l.mu.Unlock()
		l.logger.Warn("LangSmith: Trace not found for ending",
			loggerv2.String("trace_id", string(traceID)))
		return
	}

	now := time.Now()
	run.EndTime = &now
	run.Outputs = map[string]interface{}{
		"output": output,
	}
	dottedOrder := run.DottedOrder // Capture before unlocking
	l.mu.Unlock()

	// Queue run update - include trace_id and dotted_order which are required by API
	updateRun := &langsmithRun{
		ID:          run.ID,      // Use the internal UUID
		TraceID:     run.TraceID, // Use the internal UUID
		DottedOrder: dottedOrder, // Required by LangSmith API for PATCH
		EndTime:     &now,
		Outputs:     run.Outputs,
	}

	select {
	case l.patchQueue <- updateRun:
	default:
		l.logger.Error("LangSmith: Patch queue full, dropping trace-end event", nil)
	}

	l.logger.Info("LangSmith: Ended trace",
		loggerv2.String("trace_id", string(traceID)),
		loggerv2.String("uuid", run.ID))

	// Explicitly cleanup trace data to prevent memory leaks
	l.cleanupTrace(traceID)
}

// cleanupTrace removes trace-related data from memory
func (l *LangsmithTracer) cleanupTrace(traceID TraceID) {
	id := string(traceID)
	l.mu.Lock()
	defer l.mu.Unlock()

	// Get UUID for the trace to clean up 'runs' map
	uuid := l.traceIDToUUID[id]

	// Remove from traces map (keyed by external ID)
	delete(l.traces, id)

	// Remove from UUID mapping
	delete(l.traceIDToUUID, id)

	// Remove from runs map (keyed by UUID)
	if uuid != "" {
		delete(l.runs, uuid)
		// Also clean up by external ID if present (LangsmithTracer stores both)
		delete(l.runs, id)
	}

	// Remove from hierarchy mappings
	delete(l.agentRuns, id)
	delete(l.conversationRuns, id)
	delete(l.llmGenerationRuns, id)

	// Note: toolCallRuns and mcpConnectionRuns are harder to clean precisely
	// as they use compound keys. Background cleanup will catch them.
}

// StartRun starts a new run (child of parent) - equivalent to StartSpan in Langfuse
func (l *LangsmithTracer) StartRun(parentID, runType, name string, input interface{}) SpanID {
	id := generateID()
	now := time.Now()

	// Get trace ID and parent info for hierarchy
	l.mu.RLock()
	parentRun, hasParent := l.runs[parentID]
	var traceID, parentUUID, dottedOrder string
	if hasParent {
		traceID = parentRun.TraceID
		parentUUID = parentRun.ID // Use the parent's UUID as parent_run_id
		if traceID == "" {
			traceID = parentRun.ID
		}
		// Build dotted order for hierarchy - each segment is {timestamp}Z{uuid}
		if parentRun.DottedOrder != "" {
			dottedOrder = fmt.Sprintf("%s.%sZ%s", parentRun.DottedOrder, strings.Replace(now.Format("20060102T150405.000000"), ".", "", 1), id)
		}
	} else {
		// Parent not found - use or create a UUID for the trace
		// This ensures trace_id is always a valid UUID
		traceID = l.traceIDToUUID[parentID]
		if traceID == "" {
			// Generate a new UUID and store the mapping (will be done outside lock)
			traceID = generateID()
		}
		parentUUID = traceID // The trace is the parent
	}
	l.mu.RUnlock()

	// If parent wasn't found, ensure the UUID mapping is stored and create a root trace
	if !hasParent {
		needsRootTrace := false
		l.mu.Lock()
		if existing := l.traceIDToUUID[parentID]; existing == "" {
			l.traceIDToUUID[parentID] = traceID
			needsRootTrace = true
		} else {
			traceID = existing
			parentUUID = existing
		}
		// Check if we already have a trace run for this UUID
		if _, exists := l.runs[traceID]; !exists {
			needsRootTrace = true
		}
		l.mu.Unlock()

		// Create root trace if needed
		if needsRootTrace {
			tsFormat := strings.Replace(now.Format("20060102T150405.000000"), ".", "", 1)
			rootRun := &langsmithRun{
				ID:          traceID,
				Name:        "trace",
				RunType:     "chain",
				StartTime:   now,
				SessionName: l.project,
				TraceID:     traceID,
				DottedOrder: fmt.Sprintf("%sZ%s", tsFormat, traceID),
				Inputs: map[string]interface{}{
					"input": parentID, // Store external trace ID for reference
				},
			}
			l.mu.Lock()
			l.runs[traceID] = rootRun
			l.traces[parentID] = rootRun
			l.mu.Unlock()

			// Queue root trace creation
			select {
			case l.postQueue <- rootRun:
			default:
				l.logger.Error("LangSmith: Post queue full, dropping root trace", nil)
			}
		}

		// Now set dottedOrder for this child run
		l.mu.RLock()
		if parentRun, ok := l.runs[traceID]; ok && parentRun.DottedOrder != "" {
			dottedOrder = fmt.Sprintf("%s.%sZ%s", parentRun.DottedOrder, strings.Replace(now.Format("20060102T150405.000000"), ".", "", 1), id)
		}
		l.mu.RUnlock()
	}

	if dottedOrder == "" {
		// Fallback format: {timestamp}Z{trace_uuid}.{timestamp}Z{run_uuid}
		tsFormat := strings.Replace(now.Format("20060102T150405.000000"), ".", "", 1)
		dottedOrder = fmt.Sprintf("%sZ%s.%sZ%s", tsFormat, traceID, tsFormat, id)
	}

	run := &langsmithRun{
		ID:          id,
		Name:        name,
		RunType:     runType,
		StartTime:   now,
		SessionName: l.project,
		TraceID:     traceID,
		ParentRunID: parentUUID, // Use UUID instead of external ID
		DottedOrder: dottedOrder,
		Inputs: map[string]interface{}{
			"input": input,
		},
	}

	l.mu.Lock()
	l.runs[id] = run
	l.mu.Unlock()

	// Queue run creation
	select {
	case l.postQueue <- run:
	default:
		l.logger.Error("LangSmith: Post queue full, dropping run-create event", nil)
	}

	l.logger.Debug("LangSmith: Started run",
		loggerv2.String("name", name),
		loggerv2.String("id", id),
		loggerv2.String("run_type", runType),
		loggerv2.String("parent_id", parentID))

	return SpanID(id)
}

// EndRun ends a run with output - equivalent to EndSpan in Langfuse
func (l *LangsmithTracer) EndRun(runID SpanID, output interface{}, err error) {
	l.mu.Lock()
	run, exists := l.runs[string(runID)]
	if !exists {
		l.mu.Unlock()
		l.logger.Warn("LangSmith: Run not found for ending",
			loggerv2.String("run_id", string(runID)))
		return
	}

	now := time.Now()
	run.EndTime = &now
	run.Outputs = map[string]interface{}{
		"output": output,
	}
	if err != nil {
		run.Error = err.Error()
	}
	traceID := run.TraceID         // Capture before unlocking
	dottedOrder := run.DottedOrder // Capture before unlocking
	parentRunID := run.ParentRunID // Capture before unlocking
	l.mu.Unlock()

	// Queue run update - include trace_id and dotted_order which are required by API
	updateRun := &langsmithRun{
		ID:          run.ID,      // Use the internal UUID
		TraceID:     traceID,     // Required by LangSmith API for PATCH
		DottedOrder: dottedOrder, // Required by LangSmith API for PATCH
		ParentRunID: parentRunID, // Required if dotted_order implies hierarchy
		EndTime:     &now,
		Outputs:     run.Outputs,
		Error:       run.Error,
	}

	select {
	case l.patchQueue <- updateRun:
	default:
		l.logger.Error("LangSmith: Patch queue full, dropping run-end event", nil)
	}

	l.logger.Debug("LangSmith: Ended run",
		loggerv2.String("run_id", string(runID)),
		loggerv2.Any("has_error", err != nil))
}

// StartLLMRun starts an LLM generation run with model info
func (l *LangsmithTracer) StartLLMRun(parentID, name, model string, input interface{}) SpanID {
	id := generateID()
	now := time.Now()

	// Get trace ID and parent info for hierarchy
	l.mu.RLock()
	parentRun, hasParent := l.runs[parentID]
	var traceID, parentUUID, dottedOrder string
	if hasParent {
		traceID = parentRun.TraceID
		parentUUID = parentRun.ID // Use the parent's UUID as parent_run_id
		if traceID == "" {
			traceID = parentRun.ID
		}
		if parentRun.DottedOrder != "" {
			// Each segment is {timestamp}Z{uuid}
			dottedOrder = fmt.Sprintf("%s.%sZ%s", parentRun.DottedOrder, strings.Replace(now.Format("20060102T150405.000000"), ".", "", 1), id)
		}
	} else {
		// Parent not found - use or create a UUID for the trace
		traceID = l.traceIDToUUID[parentID]
		if traceID == "" {
			traceID = generateID()
		}
		parentUUID = traceID
	}
	l.mu.RUnlock()

	// If parent wasn't found, ensure the UUID mapping is stored and create a root trace
	if !hasParent {
		needsRootTrace := false
		l.mu.Lock()
		if existing := l.traceIDToUUID[parentID]; existing == "" {
			l.traceIDToUUID[parentID] = traceID
			needsRootTrace = true
		} else {
			traceID = existing
			parentUUID = existing
		}
		// Check if we already have a trace run for this UUID
		if _, exists := l.runs[traceID]; !exists {
			needsRootTrace = true
		}
		l.mu.Unlock()

		// Create root trace if needed
		if needsRootTrace {
			tsFormat := strings.Replace(now.Format("20060102T150405.000000"), ".", "", 1)
			rootRun := &langsmithRun{
				ID:          traceID,
				Name:        "trace",
				RunType:     "chain",
				StartTime:   now,
				SessionName: l.project,
				TraceID:     traceID,
				DottedOrder: fmt.Sprintf("%sZ%s", tsFormat, traceID),
				Inputs: map[string]interface{}{
					"input": parentID, // Store external trace ID for reference
				},
			}
			l.mu.Lock()
			l.runs[traceID] = rootRun
			l.traces[parentID] = rootRun
			l.mu.Unlock()

			// Queue root trace creation
			select {
			case l.postQueue <- rootRun:
			default:
				l.logger.Error("LangSmith: Post queue full, dropping root trace", nil)
			}
		}

		// Now set dottedOrder for this child run
		l.mu.RLock()
		if parentRun, ok := l.runs[traceID]; ok && parentRun.DottedOrder != "" {
			dottedOrder = fmt.Sprintf("%s.%sZ%s", parentRun.DottedOrder, strings.Replace(now.Format("20060102T150405.000000"), ".", "", 1), id)
		}
		l.mu.RUnlock()
	}

	if dottedOrder == "" {
		// Fallback format: {timestamp}Z{trace_uuid}.{timestamp}Z{run_uuid}
		tsFormat := strings.Replace(now.Format("20060102T150405.000000"), ".", "", 1)
		dottedOrder = fmt.Sprintf("%sZ%s.%sZ%s", tsFormat, traceID, tsFormat, id)
	}

	run := &langsmithRun{
		ID:          id,
		Name:        name,
		RunType:     "llm",
		StartTime:   now,
		SessionName: l.project,
		TraceID:     traceID,
		ParentRunID: parentUUID, // Use UUID instead of external ID
		DottedOrder: dottedOrder,
		Model:       model,
		Inputs: map[string]interface{}{
			"input": input,
		},
		Extra: map[string]interface{}{
			"invocation_params": map[string]interface{}{
				"model": model,
			},
		},
		Serialized: map[string]interface{}{
			"name": model,
		},
	}

	l.mu.Lock()
	l.runs[id] = run
	l.mu.Unlock()

	// Queue run creation
	select {
	case l.postQueue <- run:
	default:
		l.logger.Error("LangSmith: Post queue full, dropping llm-run-create event", nil)
	}

	l.logger.Debug("LangSmith: Started LLM run",
		loggerv2.String("name", name),
		loggerv2.String("id", id),
		loggerv2.String("model", model))

	return SpanID(id)
}

// EndLLMRun ends an LLM run with output and token usage
func (l *LangsmithTracer) EndLLMRun(runID SpanID, output interface{}, usage UsageMetrics, err error) {
	l.mu.Lock()
	run, exists := l.runs[string(runID)]
	if !exists {
		l.mu.Unlock()
		l.logger.Warn("LangSmith: LLM run not found for ending",
			loggerv2.String("run_id", string(runID)))
		return
	}

	now := time.Now()
	run.EndTime = &now
	run.Outputs = map[string]interface{}{
		"output": output,
	}
	run.PromptTokens = usage.InputTokens
	run.CompletionTokens = usage.OutputTokens
	run.TotalTokens = usage.TotalTokens

	// Add token usage to extra
	if run.Extra == nil {
		run.Extra = make(map[string]interface{})
	}
	run.Extra["tokens"] = map[string]interface{}{
		"prompt":     usage.InputTokens,
		"completion": usage.OutputTokens,
		"total":      usage.TotalTokens,
	}

	if err != nil {
		run.Error = err.Error()
	}
	traceID := run.TraceID         // Capture before unlocking
	dottedOrder := run.DottedOrder // Capture before unlocking
	parentRunID := run.ParentRunID // Capture before unlocking
	l.mu.Unlock()

	// Queue run update - include trace_id and dotted_order which are required by API
	updateRun := &langsmithRun{
		ID:          run.ID,      // Use the internal UUID
		TraceID:     traceID,     // Required by LangSmith API for PATCH
		DottedOrder: dottedOrder, // Required by LangSmith API for PATCH
		ParentRunID: parentRunID, // Required if dotted_order implies hierarchy
		EndTime:     &now,
		Outputs:     run.Outputs,
		Extra:       run.Extra,
		Error:       run.Error,
	}

	select {
	case l.patchQueue <- updateRun:
	default:
		l.logger.Error("LangSmith: Patch queue full, dropping llm-run-end event", nil)
	}

	l.logger.Debug("LangSmith: Ended LLM run",
		loggerv2.String("run_id", string(runID)),
		loggerv2.Int("prompt_tokens", usage.InputTokens),
		loggerv2.Int("completion_tokens", usage.OutputTokens))
}

// eventProcessor processes events in the background and sends them to LangSmith
func (l *LangsmithTracer) eventProcessor() {
	defer l.wg.Done()

	ticker := time.NewTicker(2 * time.Second) // Batch events every 2 seconds
	defer ticker.Stop()

	cleanupTicker := time.NewTicker(5 * time.Minute) // Cleanup every 5 minutes
	defer cleanupTicker.Stop()

	var postBatch []*langsmithRun
	var patchBatch []*langsmithRun

	for {
		select {
		case run := <-l.postQueue:
			postBatch = append(postBatch, run)
			// Send batch when it reaches size limit
			if len(postBatch)+len(patchBatch) >= 50 {
				l.sendBatch(postBatch, patchBatch)
				postBatch = nil
				patchBatch = nil
			}

		case run := <-l.patchQueue:
			patchBatch = append(patchBatch, run)
			// Send batch when it reaches size limit
			if len(postBatch)+len(patchBatch) >= 50 {
				l.sendBatch(postBatch, patchBatch)
				postBatch = nil
				patchBatch = nil
			}

		case <-ticker.C:
			// Send batch on timer if there are events
			if len(postBatch) > 0 || len(patchBatch) > 0 {
				l.sendBatch(postBatch, patchBatch)
				postBatch = nil
				patchBatch = nil
			}

		case <-cleanupTicker.C:
			// Cleanup old runs from memory
			l.cleanupOldRuns()

		case doneCh := <-l.flushCh:
			// Flush signal received - send any pending batch immediately
			if len(postBatch) > 0 || len(patchBatch) > 0 {
				l.sendBatch(postBatch, patchBatch)
				postBatch = nil
				patchBatch = nil
			}
			close(doneCh)

		case <-l.stopCh:
			// Send final batch and exit
			if len(postBatch) > 0 || len(patchBatch) > 0 {
				l.sendBatch(postBatch, patchBatch)
			}
			return
		}
	}
}

// sendRunUpdate sends a direct PATCH request to update a run
// This is used for updating running traces (e.g. setting name/inputs) where
// the batch API has validation issues (requiring end_time).
func (l *LangsmithTracer) sendRunUpdate(runID string, updates map[string]interface{}) {
	jsonData, err := json.Marshal(updates)
	if err != nil {
		l.logger.Error("LangSmith: Failed to marshal run update", err)
		return
	}

	req, err := http.NewRequest("PATCH", l.host+"/runs/"+runID, bytes.NewBuffer(jsonData))
	if err != nil {
		l.logger.Error("LangSmith: Failed to create update request", err)
		return
	}

	req.Header.Set("X-API-Key", l.apiKey)
	req.Header.Set("Content-Type", "application/json")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := l.client.Do(req)
	if err != nil {
		l.logger.Error("LangSmith: Failed to send run update", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		l.logger.Error("LangSmith: Run update failed", nil,
			loggerv2.Int("status_code", resp.StatusCode),
			loggerv2.String("body", string(body)),
			loggerv2.String("run_id", runID))
		return
	}

	l.logger.Debug("LangSmith: Updated run successfully",
		loggerv2.String("run_id", runID))
}

// cleanupOldRuns removes old runs from memory to prevent leaks
func (l *LangsmithTracer) cleanupOldRuns() {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Retention period: 1 hour
	threshold := time.Now().Add(-1 * time.Hour)
	count := 0

	// Cleanup runs map
	for id, run := range l.runs {
		if run.EndTime != nil && run.EndTime.Before(threshold) {
			delete(l.runs, id)
			count++
		}
	}

	// Cleanup traces map and mappings
	for id, run := range l.traces {
		if run.EndTime != nil && run.EndTime.Before(threshold) {
			delete(l.traces, id)
			delete(l.traceIDToUUID, id)
			delete(l.agentRuns, id)
			delete(l.conversationRuns, id)
			delete(l.llmGenerationRuns, id)
			// Note: toolCallRuns and mcpConnectionRuns are harder to clean precisely by trace ID
			// without iterating them all, but they are less critical.
		}
	}

	if count > 0 {
		l.logger.Debug("LangSmith: Cleaned up old runs", loggerv2.Int("count", count))
	}
}

// sendBatch sends a batch of runs to LangSmith ingestion API
func (l *LangsmithTracer) sendBatch(postRuns, patchRuns []*langsmithRun) {
	if len(postRuns) == 0 && len(patchRuns) == 0 {
		return
	}

	payload := langsmithBatchPayload{
		Post:  postRuns,
		Patch: patchRuns,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		l.logger.Error("LangSmith: Failed to marshal batch", err)
		return
	}

	req, err := http.NewRequest("POST", l.host+"/runs/batch", bytes.NewBuffer(jsonData))
	if err != nil {
		l.logger.Error("LangSmith: Failed to create request", err)
		return
	}

	req.Header.Set("X-API-Key", l.apiKey)
	req.Header.Set("Content-Type", "application/json")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := l.client.Do(req)
	if err != nil {
		l.logger.Error("LangSmith: Failed to send batch", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	// Handle response - accept 200, 201, 202, 207
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		l.logger.Error("LangSmith: Batch failed", nil,
			loggerv2.Int("status_code", resp.StatusCode),
			loggerv2.String("body", string(body)))
		return
	}

	l.logger.Info("LangSmith: Sent batch successfully",
		loggerv2.Int("post_count", len(postRuns)),
		loggerv2.Int("patch_count", len(patchRuns)))
}

// Flush sends any pending events immediately and waits for them to be sent
func (l *LangsmithTracer) Flush() {
	l.logger.Debug("LangSmith: Flush started")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Wait for queues to drain
	for {
		select {
		case <-ctx.Done():
			l.logger.Warn("LangSmith: Flush timeout waiting for queue to drain")
			return
		default:
			if len(l.postQueue) == 0 && len(l.patchQueue) == 0 {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if len(l.postQueue) == 0 && len(l.patchQueue) == 0 {
			break
		}
	}

	l.logger.Debug("LangSmith: Queue drained, sending flush signal")

	// Send flush signal and wait for completion
	doneCh := make(chan struct{})
	select {
	case l.flushCh <- doneCh:
		select {
		case <-doneCh:
			l.logger.Debug("LangSmith: Flush completed successfully")
		case <-ctx.Done():
			l.logger.Warn("LangSmith: Flush timeout waiting for batch send")
		}
	case <-ctx.Done():
		l.logger.Warn("LangSmith: Flush timeout sending flush signal")
	}
}

// Shutdown gracefully shuts down the tracer
func (l *LangsmithTracer) Shutdown() {
	close(l.stopCh)
	l.wg.Wait()
	close(l.postQueue)
	close(l.patchQueue)
}

// EmitEvent processes an agent event and takes appropriate tracing actions
func (l *LangsmithTracer) EmitEvent(event AgentEvent) error {
	eventType := event.GetType()
	l.logger.Debug("LangSmith: Processing event",
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
		return l.handleConversationEnd(event)
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

	// MCP Server connection events
	case EventTypeMCPServerConnectionStart:
		return l.handleMCPServerConnectionStart(event)
	case EventTypeMCPServerConnectionEnd:
		return l.handleMCPServerConnectionEnd(event)

	default:
		l.logger.Debug("LangSmith: Unhandled event type",
			loggerv2.String("type", eventType))
		return nil
	}
}

// EmitLLMEvent processes an LLM event
func (l *LangsmithTracer) EmitLLMEvent(event LLMEvent) error {
	l.logger.Debug("LangSmith: Processing LLM event",
		loggerv2.String("model", event.GetModelID()),
		loggerv2.String("provider", event.GetProvider()))
	return nil
}

// Helper functions for generating names
// Note: Most helper functions have been moved to utils.go

// Event handlers

// getOrCreateUUID gets or creates a LangSmith UUID for an external trace ID.
// LangSmith requires trace_id and run IDs to be in UUID format (32 hex chars).
// This function maintains a mapping from external trace IDs to LangSmith UUIDs.
func (l *LangsmithTracer) getOrCreateUUID(externalTraceID string) string {
	l.mu.RLock()
	if uuid, exists := l.traceIDToUUID[externalTraceID]; exists {
		l.mu.RUnlock()
		return uuid
	}
	l.mu.RUnlock()

	// Generate new UUID and store mapping
	uuid := generateID()
	l.mu.Lock()
	// Double-check after acquiring write lock
	if existingUUID, exists := l.traceIDToUUID[externalTraceID]; exists {
		l.mu.Unlock()
		return existingUUID
	}
	l.traceIDToUUID[externalTraceID] = uuid
	l.mu.Unlock()

	l.logger.Debug("LangSmith: Created UUID mapping",
		loggerv2.String("external_trace_id", externalTraceID),
		loggerv2.String("langsmith_uuid", uuid))

	return uuid
}

func (l *LangsmithTracer) handleAgentStart(event AgentEvent) error {
	externalTraceID := event.GetTraceID()

	// Generate LangSmith UUID for this trace (LangSmith requires UUID format)
	langsmithUUID := l.getOrCreateUUID(externalTraceID)

	// Generate trace name
	traceName := GenerateTraceName(event.GetData())
	now := time.Now()

	// Create root run (trace) with LangSmith UUID
	run := &langsmithRun{
		ID:          langsmithUUID,
		Name:        traceName,
		RunType:     "chain",
		StartTime:   now,
		SessionName: l.project,
		TraceID:     langsmithUUID, // LangSmith requires UUID format
		DottedOrder: fmt.Sprintf("%sZ%s", strings.Replace(now.Format("20060102T150405.000000"), ".", "", 1), langsmithUUID),
		Inputs: map[string]interface{}{
			"input": event.GetData(),
		},
		Extra: map[string]interface{}{
			"metadata": map[string]interface{}{
				"event_type":        "agent_start",
				"external_trace_id": externalTraceID, // Store original for reference
			},
		},
	}

	l.mu.Lock()
	l.traces[externalTraceID] = run // Key by external ID for easy lookup
	l.runs[langsmithUUID] = run     // Also store by LangSmith UUID
	l.runs[externalTraceID] = run   // And by external ID for EndTrace lookup
	l.mu.Unlock()

	// Create agent run as child (using LangSmith UUID as parent)
	agentRunName := GenerateAgentSpanName(event.GetData())
	agentRunID := l.StartRun(langsmithUUID, "chain", agentRunName, event.GetData())

	l.mu.Lock()
	l.agentRuns[externalTraceID] = string(agentRunID)
	l.mu.Unlock()

	// Queue trace creation
	select {
	case l.postQueue <- run:
	default:
		l.logger.Error("LangSmith: Post queue full, dropping agent-start event", nil)
	}

	l.logger.Debug("LangSmith: Created trace and agent run",
		loggerv2.String("external_trace_id", externalTraceID),
		loggerv2.String("langsmith_uuid", langsmithUUID),
		loggerv2.String("agent_run_id", string(agentRunID)))

	return nil
}

func (l *LangsmithTracer) handleAgentEnd(event AgentEvent) error {
	traceID := event.GetTraceID()

	l.mu.RLock()
	agentRunID := l.agentRuns[traceID]
	l.mu.RUnlock()

	finalResult := ExtractFinalResult(event.GetData())

	// End agent run
	if agentRunID != "" {
		l.EndRun(SpanID(agentRunID), finalResult, nil)
	}

	// End trace
	l.EndTrace(TraceID(traceID), finalResult)

	l.logger.Debug("LangSmith: Ended agent and trace",
		loggerv2.String("trace_id", traceID))

	return nil
}

func (l *LangsmithTracer) handleAgentError(event AgentEvent) error {
	traceID := event.GetTraceID()

	var errMsg string
	switch data := event.GetData().(type) {
	case *events.AgentErrorEvent:
		errMsg = data.Error
	}

	l.mu.RLock()
	agentRunID := l.agentRuns[traceID]
	l.mu.RUnlock()

	// End agent run with error
	if agentRunID != "" {
		l.EndRun(SpanID(agentRunID), nil, fmt.Errorf("%s", errMsg))
	}

	// Get trace info for PATCH
	l.mu.RLock()
	trace, traceExists := l.traces[traceID]
	var traceDottedOrder string
	if traceExists {
		traceDottedOrder = trace.DottedOrder
	}
	l.mu.RUnlock()

	// End trace with error
	now := time.Now()
	updateRun := &langsmithRun{
		ID:          trace.ID,         // Use the internal UUID
		TraceID:     trace.ID,         // Use the internal UUID (trace is its own trace)
		DottedOrder: traceDottedOrder, // Required by LangSmith API for PATCH
		EndTime:     &now,
		Error:       errMsg,
	}

	select {
	case l.patchQueue <- updateRun:
	default:
		l.logger.Error("LangSmith: Patch queue full, dropping agent-error event", nil)
	}

	return nil
}

func (l *LangsmithTracer) handleConversationStart(event AgentEvent) error {
	traceID := event.GetTraceID()

	// Update trace name with question
	l.mu.Lock()
	trace, exists := l.traces[traceID]
	if exists {
		newName := GenerateTraceName(event.GetData())
		trace.Name = newName

		// Extract question for input
		var question string
		switch data := event.GetData().(type) {
		case *events.ConversationStartEvent:
			question = data.Question
		}

		if question != "" {
			trace.Inputs = map[string]interface{}{
				"question": question,
			}
		}

		// Use direct PATCH for running trace updates to avoid "catch-22" with batch API
		// (Batch API requires end_time if trace_id/dotted_order are present)
		updates := map[string]interface{}{
			"name":   newName,
			"inputs": trace.Inputs,
		}

		// Send update asynchronously
		l.wg.Add(1)
		go func(id string, data map[string]interface{}) {
			defer l.wg.Done()
			l.sendRunUpdate(id, data)
		}(trace.ID, updates)
	}
	l.mu.Unlock()

	// Get agent run as parent
	l.mu.RLock()
	parentRunID := l.agentRuns[traceID]
	l.mu.RUnlock()

	if parentRunID == "" {
		parentRunID = traceID
	}

	// Create conversation run
	conversationRunID := l.StartRun(parentRunID, "chain", "conversation", event.GetData())

	l.mu.Lock()
	l.conversationRuns[traceID] = string(conversationRunID)
	l.mu.Unlock()

	l.logger.Debug("LangSmith: Started conversation run",
		loggerv2.String("run_id", string(conversationRunID)))

	return nil
}

func (l *LangsmithTracer) handleConversationEnd(event AgentEvent) error {
	traceID := event.GetTraceID()

	l.mu.RLock()
	conversationRunID := l.conversationRuns[traceID]
	l.mu.RUnlock()

	finalResult := ExtractFinalResult(event.GetData())

	if conversationRunID != "" {
		l.EndRun(SpanID(conversationRunID), finalResult, nil)
	}

	return nil
}

func (l *LangsmithTracer) handleLLMGenerationStart(event AgentEvent) error {
	traceID := event.GetTraceID()

	// Get parent (conversation run or agent run)
	l.mu.RLock()
	parentRunID := l.conversationRuns[traceID]
	if parentRunID == "" {
		parentRunID = l.agentRuns[traceID]
	}
	if parentRunID == "" {
		parentRunID = traceID
	}
	l.mu.RUnlock()

	var turn int
	var modelID string

	switch data := event.GetData().(type) {
	case *events.LLMGenerationStartEvent:
		turn = data.Turn
		modelID = data.ModelID
	}

	runName := GenerateLLMSpanName(event.GetData())
	llmRunID := l.StartLLMRun(parentRunID, runName, modelID, event.GetData())

	l.mu.Lock()
	l.llmGenerationRuns[traceID] = string(llmRunID)
	l.mu.Unlock()

	l.logger.Debug("LangSmith: Started LLM generation run",
		loggerv2.String("run_id", string(llmRunID)),
		loggerv2.Int("turn", turn))

	return nil
}

func (l *LangsmithTracer) handleLLMGenerationEnd(event AgentEvent) error {
	traceID := event.GetTraceID()

	l.mu.RLock()
	llmRunID := l.llmGenerationRuns[traceID]
	l.mu.RUnlock()

	if llmRunID == "" {
		l.logger.Warn("LangSmith: No LLM run found for ending",
			loggerv2.String("trace_id", traceID))
		return nil
	}

	var content string
	var usage UsageMetrics

	switch data := event.GetData().(type) {
	case *events.LLMGenerationEndEvent:
		content = data.Content
		usage = UsageMetrics{
			InputTokens:  data.UsageMetrics.PromptTokens,
			OutputTokens: data.UsageMetrics.CompletionTokens,
			TotalTokens:  data.UsageMetrics.TotalTokens,
		}
	}

	l.EndLLMRun(SpanID(llmRunID), content, usage, nil)

	l.logger.Debug("LangSmith: Ended LLM generation run",
		loggerv2.String("run_id", llmRunID))

	return nil
}

func (l *LangsmithTracer) handleToolCallStart(event AgentEvent) error {
	traceID := event.GetTraceID()

	// Get parent (current LLM generation run)
	l.mu.RLock()
	parentRunID := l.llmGenerationRuns[traceID]
	if parentRunID == "" {
		parentRunID = l.conversationRuns[traceID]
	}
	if parentRunID == "" {
		parentRunID = traceID
	}
	l.mu.RUnlock()

	var turn int
	var toolName, serverName string
	var toolParams interface{}

	switch data := event.GetData().(type) {
	case *events.ToolCallStartEvent:
		turn = data.Turn
		toolName = data.ToolName
		serverName = data.ServerName
		toolParams = data.ToolParams
	}

	runName := GenerateToolSpanName(event.GetData())
	toolRunID := l.StartRun(parentRunID, "tool", runName, map[string]interface{}{
		"tool_name":   toolName,
		"server_name": serverName,
		"params":      toolParams,
	})

	// Store with compound key — use ToolCallID when available to avoid collisions
	// with parallel calls to the same tool in the same turn
	var toolCallID string
	if startEvent, ok := event.GetData().(*events.ToolCallStartEvent); ok {
		toolCallID = startEvent.ToolCallID
	}
	var key string
	if toolCallID != "" {
		key = fmt.Sprintf("%s_%s", traceID, toolCallID)
	} else {
		key = fmt.Sprintf("%s_%d_%s", traceID, turn, toolName)
	}
	l.mu.Lock()
	l.toolCallRuns[key] = string(toolRunID)
	l.mu.Unlock()

	l.logger.Debug("LangSmith: Started tool call run",
		loggerv2.String("run_id", string(toolRunID)),
		loggerv2.String("tool", toolName))

	return nil
}

func (l *LangsmithTracer) handleToolCallEnd(event AgentEvent) error {
	traceID := event.GetTraceID()

	var turn int
	var toolName, result, toolCallID string

	switch data := event.GetData().(type) {
	case *events.ToolCallEndEvent:
		turn = data.Turn
		toolName = data.ToolName
		result = data.Result
		toolCallID = data.ToolCallID
	}

	var key string
	if toolCallID != "" {
		key = fmt.Sprintf("%s_%s", traceID, toolCallID)
	} else {
		key = fmt.Sprintf("%s_%d_%s", traceID, turn, toolName)
	}

	l.mu.RLock()
	toolRunID := l.toolCallRuns[key]
	l.mu.RUnlock()

	if toolRunID == "" {
		l.logger.Warn("LangSmith: No tool run found for ending",
			loggerv2.String("key", key))
		return nil
	}

	l.EndRun(SpanID(toolRunID), result, nil)

	l.logger.Debug("LangSmith: Ended tool call run",
		loggerv2.String("run_id", toolRunID))

	return nil
}

func (l *LangsmithTracer) handleToolCallError(event AgentEvent) error {
	traceID := event.GetTraceID()

	var turn int
	var toolName, errMsg, toolCallID string

	switch data := event.GetData().(type) {
	case *events.ToolCallErrorEvent:
		turn = data.Turn
		toolName = data.ToolName
		errMsg = data.Error
		toolCallID = data.ToolCallID
	}

	var key string
	if toolCallID != "" {
		key = fmt.Sprintf("%s_%s", traceID, toolCallID)
	} else {
		key = fmt.Sprintf("%s_%d_%s", traceID, turn, toolName)
	}

	l.mu.RLock()
	toolRunID := l.toolCallRuns[key]
	l.mu.RUnlock()

	if toolRunID == "" {
		l.logger.Warn("LangSmith: No tool run found for error",
			loggerv2.String("key", key))
		return nil
	}

	l.EndRun(SpanID(toolRunID), nil, fmt.Errorf("%s", errMsg))

	l.logger.Debug("LangSmith: Ended tool call run with error",
		loggerv2.String("run_id", toolRunID))

	return nil
}

func (l *LangsmithTracer) handleMCPServerConnectionStart(event AgentEvent) error {
	traceID := event.GetTraceID()

	var serverName string
	switch data := event.GetData().(type) {
	case *events.MCPServerConnectionEvent:
		serverName = data.ServerName
	}

	if serverName == "" {
		serverName = "mcp_connection"
	}

	runName := fmt.Sprintf("mcp_connection_%s", serverName)
	mcpRunID := l.StartRun(traceID, "chain", runName, event.GetData())

	l.mu.Lock()
	l.mcpConnectionRuns[serverName] = string(mcpRunID)
	l.mu.Unlock()

	l.logger.Debug("LangSmith: Started MCP connection run",
		loggerv2.String("run_id", string(mcpRunID)),
		loggerv2.String("server", serverName))

	return nil
}

func (l *LangsmithTracer) handleMCPServerConnectionEnd(event AgentEvent) error {
	var serverName string
	switch data := event.GetData().(type) {
	case *events.MCPServerConnectionEvent:
		serverName = data.ServerName
	}

	l.mu.RLock()
	mcpRunID := l.mcpConnectionRuns[serverName]
	l.mu.RUnlock()

	if mcpRunID == "" {
		return nil
	}

	l.EndRun(SpanID(mcpRunID), event.GetData(), nil)

	return nil
}
