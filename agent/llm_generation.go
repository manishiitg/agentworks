package mcpagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/observability"
	"github.com/manishiitg/mcpagent/toolerr"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// isContextCanceledError checks if an error is due to context cancellation or deadline exceeded
func isContextCanceledError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(err.Error(), "context canceled") ||
		strings.Contains(err.Error(), "context deadline exceeded")
}

func claudeHTTPRoutingHooksEnabled() bool {
	v := strings.TrimSpace(os.Getenv("MCPAGENT_CLAUDE_ENFORCE_HTTP_TOOL_ROUTING"))
	if v == "" {
		return false
	}
	return v == "1" ||
		strings.EqualFold(v, "true") ||
		strings.EqualFold(v, "yes") ||
		strings.EqualFold(v, "on")
}

// writeClaudeHTTPRoutingHook writes the enforced-mode PreToolUse hook. The
// hook's ALLOWED set is generated from the SAME source of truth as
// --allowedTools (claudeBridgeAllowedToolIdentifiers) — the fixed core bridge
// tools plus additional, whatever a caller registered via
// withAdditionalBridgeTools. Previously this was a hardcoded 4-tool literal,
// which silently denied any additional bridge tool once enforcement was on,
// even though the caller had explicitly registered it.
func writeClaudeHTTPRoutingHook(additional []string, admits func(name, toolType string) bool, nativeAllowed []string) (string, error) {
	hooksDir := filepath.Join(os.TempDir(), "claude-code-hooks")
	if err := os.MkdirAll(hooksDir, 0750); err != nil {
		return "", fmt.Errorf("create claude hooks dir: %w", err)
	}

	allowedIdentifiers := append(claudeBridgeAllowedToolIdentifiers(additional, admits), nativeAllowed...)
	allowedJSON, err := json.Marshal(allowedIdentifiers)
	if err != nil {
		return "", fmt.Errorf("marshal claude hook allowlist: %w", err)
	}

	// The hook filename is content-addressed by the allowlist it enforces.
	// Before withAdditionalBridgeTools was wired into this allowlist, every
	// agent wrote byte-identical content to this fixed path, so a race was
	// harmless. Now that the content genuinely varies per agent, two
	// concurrent agents with DIFFERENT registered tools would otherwise
	// overwrite each other's allowlist file — whichever wrote last would
	// silently govern BOTH sessions' enforcement. Hashing the content instead
	// of using a fixed name means agents with the SAME allowlist safely share
	// one file (no needless growth), while agents with different allowlists
	// never collide.
	hookHash := sha256.Sum256(allowedJSON)
	hookPath := filepath.Join(hooksDir, "enforce-http-tool-routing-"+hex.EncodeToString(hookHash[:8])+".py")
	hookScript := `#!/usr/bin/env python3
import json
import sys

ALLOWED = set(json.loads('` + string(allowedJSON) + `'))

raw = sys.stdin.read()
payload = {}

try:
    payload = json.loads(raw) if raw else {}
except Exception:
    payload = {}

tool_name = payload.get("tool_name", "")

if tool_name in ALLOWED:
    raise SystemExit(0)

sys.stdout.write(json.dumps({
    "hookSpecificOutput": {
        "hookEventName": "PreToolUse",
        "permissionDecision": "deny",
        "permissionDecisionReason": (
            "Only registered bridge tools (" + ", ".join(sorted(ALLOWED)) + ") "
            "are allowed in this Claude Code bridge session. "
            "Use get_api_spec plus execute_shell_command for HTTP-based tool access."
        )
    }
}) + "\n")
`

	if err := os.WriteFile(hookPath, []byte(hookScript), 0600); err != nil { //nolint:gosec
		return "", fmt.Errorf("write claude hook script: %w", err)
	}
	return hookPath, nil
}

func buildClaudeHTTPRoutingSettings(hookPath string) (string, error) {
	command := fmt.Sprintf("python3 %q", hookPath)
	settings := map[string]interface{}{
		"hooks": map[string]interface{}{
			"PreToolUse": []map[string]interface{}{
				{
					"matcher": "*",
					"hooks": []map[string]interface{}{
						{
							"type":    "command",
							"command": command,
							"timeout": 5,
						},
					},
				},
			},
		},
	}

	settingsBytes, err := json.Marshal(settings)
	if err != nil {
		return "", fmt.Errorf("marshal claude hook settings: %w", err)
	}
	return string(settingsBytes), nil
}

func retryLogCategory(errorType string) string {
	category := strings.TrimSuffix(strings.ToUpper(strings.TrimSpace(errorType)), "_ERROR")
	if category == "" {
		return "RETRY"
	}
	return category
}

// retryOriginalModel handles same-model retry scheduling for transient errors.
// Returns: shouldRetry (bool), delay (time.Duration), error
func retryOriginalModel(a *Agent, ctx context.Context, errorType string, attempt, maxRetries int, baseDelay, maxDelay time.Duration, turn int, logger loggerv2.Logger, usage observability.UsageMetrics) (bool, time.Duration, error) {
	return retryOriginalModelWithReason(a, ctx, errorType, errorType, attempt, maxRetries, baseDelay, maxDelay, turn, logger, usage)
}

// retryReasonText is what the retry notice tells the person. A timeout is not a connection problem: a launch that
// never produced a screen says what it was stuck on ("Muse did not start in 1m30s: it was showing a plugin hooks
// review screen"), any other timeout says no response came in time. Other errors include the provider's reason.
func retryReasonText(errorType string, err error) string {
	if llmerrors.KindOf(err) != llmerrors.KindTimeout {
		display := llmerrors.ProviderDisplay(err)
		if display.Message != "" {
			return strings.ReplaceAll(errorType, "_", " ") + ": " + display.Message
		}
		return errorType
	}
	text := err.Error()
	if i := strings.Index(text, "]: "); i >= 0 {
		text = text[i+3:]
	}
	for _, cut := range []string{" Your message is saved", " (timed out"} {
		if i := strings.Index(text, cut); i >= 0 {
			text = text[:i]
		}
	}
	text = strings.TrimRight(strings.TrimSpace(text), ".")
	if strings.Contains(text, " did not start") && len(text) <= 160 && !strings.Contains(text, "\n") {
		return text
	}
	return "no response in time"
}

func retryOriginalModelWithReason(a *Agent, ctx context.Context, errorType, reason string, attempt, maxRetries int, baseDelay, maxDelay time.Duration, turn int, logger loggerv2.Logger, usage observability.UsageMetrics) (bool, time.Duration, error) {
	// Exponential backoff: 10s, 20s, 40s, 80s, 160s...
	delay := baseDelay * time.Duration(1<<attempt)
	if delay > maxDelay {
		delay = maxDelay
	}

	// Emit retry attempt event with proper model/provider info for UI display
	retryAttemptEvent := events.NewRetryAttemptEvent(
		turn, attempt+1, maxRetries,
		a.modelID, string(a.provider), "retry",
		false, delay, fmt.Sprintf("%s - retrying original model", reason),
	)
	a.emitTypedEvent(ctx, retryAttemptEvent)

	category := retryLogCategory(errorType)
	logMsg := fmt.Sprintf("🔄 [%s] Retrying selected model. Waiting %v before retry (attempt %d/%d)...", category, delay, attempt+1, maxRetries)
	logger.Info(logMsg)

	timer := time.NewTimer(delay)
	defer timer.Stop()

	// Wait for delay or context cancellation
	select {
	case <-ctx.Done():
		return false, delay, ctx.Err()
	case <-timer.C:
	}

	retryLogMsg := fmt.Sprintf("🔄 [%s] Retrying with original model (turn %d, attempt %d/%d)...", category, turn, attempt+2, maxRetries)
	logger.Info(retryLogMsg)
	return true, delay, nil
}

// isMaxTokenError checks if an error is due to reaching maximum token limit
func isMaxTokenError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Exclude context cancellation from max token errors
	if isContextCanceledError(err) {
		return false
	}
	// Typed classification from the provider layer wins; string matching below
	// is the fallback for errors that bypassed ProviderAwareLLM.
	if llmerrors.KindOf(err) == llmerrors.KindContextTooLong {
		return true
	}
	return strings.Contains(msg, "max_token") ||
		strings.Contains(msg, "max tokens") ||
		strings.Contains(msg, "Input is too long") ||
		strings.Contains(msg, "ValidationException") ||
		strings.Contains(msg, "too long")
}

// isQuotaExhaustedError checks if an error is a permanent quota exhaustion (daily/monthly limits)
// that will NOT recover within minutes — skip same-model retries and return the error.
func isQuotaExhaustedError(err error) bool {
	if err == nil {
		return false
	}
	if llmerrors.KindOf(err) == llmerrors.KindQuotaExhausted {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "per_day") ||
		strings.Contains(msg, "per_month") ||
		strings.Contains(msg, "generaterequestsperday") ||
		strings.Contains(msg, "resource_exhausted") ||
		strings.Contains(msg, "quota exceeded for metric") ||
		strings.Contains(msg, "exceeded your current quota") ||
		strings.Contains(msg, "retrydelay:3") || // retryDelay >= 3600s (1+ hour)
		strings.Contains(msg, "retrydelay:4") ||
		strings.Contains(msg, "retrydelay:8") ||
		strings.Contains(msg, "retrydelay:9") ||
		strings.Contains(msg, "hit your usage limit") || // Codex CLI usage exhaustion
		strings.Contains(msg, "you've hit your limit") || // Claude Code usage exhaustion
		strings.Contains(msg, "youve hit your limit") ||
		strings.Contains(msg, "usage limit")
}

// isThrottlingError checks if an error is due to API throttling
func isThrottlingError(err error) bool {
	if err == nil {
		return false
	}
	// Exclude context cancellation from throttling errors
	if isContextCanceledError(err) {
		return false
	}
	if llmerrors.KindOf(err) == llmerrors.KindRateLimit {
		return true
	}
	errStr := err.Error()
	return strings.Contains(errStr, "ThrottlingException") ||
		strings.Contains(errStr, "Too many tokens") ||
		strings.Contains(errStr, "StatusCode: 429") ||
		strings.Contains(errStr, "API returned unexpected status code: 429") ||
		strings.Contains(errStr, "status code: 429") ||
		strings.Contains(errStr, "status code 429") ||
		strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "throttled") ||
		strings.Contains(errStr, "overloaded") ||
		strings.Contains(errStr, "model is overloaded") ||
		strings.Contains(errStr, "UNAVAILABLE") ||
		(strings.Contains(errStr, "503") && strings.Contains(errStr, "overloaded"))
}

// isEmptyContentError checks if an error is due to empty content in response
func isEmptyContentError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "MALFORMED_FUNCTION_CALL") {
		return false
	}
	return strings.Contains(msg, "Choice.Content is empty string") ||
		strings.Contains(msg, "empty content error") ||
		strings.Contains(msg, "choice.Content is empty") ||
		strings.Contains(msg, "empty response")
}

// isZeroCandidatesError checks if an error is due to zero candidates returned
func isZeroCandidatesError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "zero candidates") ||
		strings.Contains(msg, "returned zero candidates") ||
		strings.Contains(msg, "no candidates")
}

// isConnectionError checks if an error is due to connection issues
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	// Exclude context cancellation from connection errors
	if isContextCanceledError(err) {
		return false
	}
	if kind := llmerrors.KindOf(err); kind == llmerrors.KindNetwork || kind == llmerrors.KindTimeout {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "network") ||
		strings.Contains(msg, "dial tcp") ||
		strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection lost") ||
		strings.Contains(msg, "connection closed") ||
		strings.Contains(msg, "unexpected EOF")
}

// isStreamError checks if an error is due to streaming issues
func isStreamError(err error) bool {
	if err == nil {
		return false
	}
	// Exclude context cancellation from stream errors
	if isContextCanceledError(err) {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "stream error") ||
		strings.Contains(msg, "stream ID") ||
		strings.Contains(msg, "streaming") ||
		strings.Contains(msg, "stream closed") ||
		strings.Contains(msg, "stream interrupted") ||
		strings.Contains(msg, "stream timeout") ||
		strings.Contains(msg, "streaming error")
}

// isInternalError checks if an error is due to internal server issues
func isInternalError(err error) bool {
	if err == nil {
		return false
	}
	if llmerrors.KindOf(err) == llmerrors.KindServerError {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "INTERNAL_ERROR") ||
		strings.Contains(msg, "internal error") ||
		strings.Contains(msg, "server error") ||
		strings.Contains(msg, "unexpected error") ||
		strings.Contains(msg, "received from peer") ||
		strings.Contains(msg, "peer error") ||
		strings.Contains(msg, "internal server error") ||
		strings.Contains(msg, "service error") ||
		strings.Contains(msg, "status 500") ||
		strings.Contains(msg, "status code: 500") ||
		strings.Contains(msg, "status code 500") ||
		strings.Contains(msg, "StatusCode: 500") ||
		strings.Contains(msg, "500") ||
		strings.Contains(msg, "status 502") ||
		strings.Contains(msg, "status code: 502") ||
		strings.Contains(msg, "status code 502") ||
		strings.Contains(msg, "502") ||
		strings.Contains(msg, "status 503") ||
		strings.Contains(msg, "status code: 503") ||
		strings.Contains(msg, "status code 503") ||
		strings.Contains(msg, "503") ||
		strings.Contains(msg, "status 504") ||
		strings.Contains(msg, "status code: 504") ||
		strings.Contains(msg, "status code 504") ||
		strings.Contains(msg, "504") ||
		strings.Contains(msg, "API returned unexpected status code: 5") ||
		strings.Contains(msg, "Bad Gateway") ||
		strings.Contains(msg, "Service Unavailable") ||
		strings.Contains(msg, "Gateway Timeout")
}

// isAuthError reports whether err is a credential/permission failure (invalid
// or expired key, unauthorized, access denied). These cannot recover by
// retrying the same model with the same credentials.
func isAuthError(err error) bool {
	if err == nil || isContextCanceledError(err) {
		return false
	}
	if kind := llmerrors.KindOf(err); kind != llmerrors.KindUnknown {
		return kind == llmerrors.KindAuth
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "invalid api key") ||
		strings.Contains(msg, "invalid x-api-key") ||
		strings.Contains(msg, "incorrect api key") ||
		strings.Contains(msg, "api key not valid") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "unauthenticated") ||
		strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "accessdeniedexception")
}

// An AGY receipt/answer deadline does not prove that its native turn stopped.
// Retrying the prompt could duplicate work already running in the sidecar.
func isUnconfirmedAGYTurnTimeout(err error) bool {
	var typed *llmerrors.Error
	if !errors.As(err, &typed) || typed.Kind != llmerrors.KindTimeout || typed.Provider != "agy-cli" || typed.Err == nil {
		return false
	}
	msg := strings.SplitN(typed.Err.Error(), "pane tail:", 2)[0]
	return strings.Contains(msg, "sidecar prompt has no matching user step after") ||
		strings.Contains(msg, "sidecar turn has no settled recorded answer within")
}

// AGY ended the turn itself (a finished error step, for example an upstream
// 500). Tools may already have run in its native session, so replaying the
// prompt could repeat their effects; the person can resend instead.
func isAGYTurnEndedByExecutor(err error) bool {
	var typed *llmerrors.Error
	return errors.As(err, &typed) && typed.Provider == "agy-cli" && typed.Err != nil &&
		strings.HasPrefix(typed.Err.Error(), "Antigravity ended the turn: ")
}

// isModelNotFoundError reports whether err means the model ID is unknown or
// unavailable for the provider. This is a permanent config error — the model
// ID will not become valid on retry — so the model is memoized and skipped on
// future turns, like a quota-exhausted model.
func isModelNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	if llmerrors.KindOf(err) == llmerrors.KindModelNotFound {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "model not found") ||
		strings.Contains(msg, "model_not_found") ||
		strings.Contains(msg, "does not exist or you do not have access")
}

// classifyLLMError categorizes the given error into a known LLM error type
func classifyLLMError(err error) string {
	// Typed provider causes take precedence over diagnostic text and terminal history.
	switch llmerrors.KindOf(err) {
	case llmerrors.KindUserInputRequired:
		return "user_input_required"
	case llmerrors.KindCanceled:
		return ""
	case llmerrors.KindContextTooLong:
		return "max_token_error"
	case llmerrors.KindAuth:
		return "auth_error"
	case llmerrors.KindModelNotFound:
		return "model_not_found_error"
	case llmerrors.KindBilling:
		return "billing_error"
	case llmerrors.KindQuotaExhausted:
		return "quota_exhausted_error"
	case llmerrors.KindRateLimit:
		return "throttling_error"
	case llmerrors.KindNetwork, llmerrors.KindTimeout:
		return "connection_error"
	case llmerrors.KindServerError:
		return "internal_error"
	case llmerrors.KindInvalidRequest:
		return ""
	}

	if isMaxTokenError(err) {
		return "max_token_error"
	} else if isAuthError(err) {
		return "auth_error"
	} else if isModelNotFoundError(err) {
		return "model_not_found_error"
	} else if isQuotaExhaustedError(err) {
		return "quota_exhausted_error"
	} else if isThrottlingError(err) {
		return "throttling_error"
	} else if isZeroCandidatesError(err) {
		return "zero_candidates_error"
	} else if isEmptyContentError(err) {
		return "empty_content_error"
	} else if isConnectionError(err) {
		return "connection_error"
	} else if isStreamError(err) {
		return "stream_error"
	} else if isInternalError(err) {
		return "internal_error"
	} else if isTmuxLossContinuationError(err) {
		return "tmux_loss_error"
	}
	return ""
}

// isTmuxLossContinuationError reports whether err is a failed coding-agent
// session continuation due to tmux loss. The session may still be alive after
// this error, so callers should not surface it as a user-visible error.
func isTmuxLossContinuationError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "coding-agent continuation retry after tmux loss failed")
}

// streamingManager handles streaming state and goroutine management
type streamingManager struct {
	accountConnectionID string // immutable selected account for this generation
	lastPlanSnapshot    string
	claudeTaskPlan      llmtypes.ClaudeTaskPlan
	toolArgsByCallID    map[string]string
	streamChan          chan llmtypes.StreamChunk
	streamingDone       chan bool
	contentChunkIndex   int
	totalChunks         int
	sawTerminal         bool
	suppressEvents      bool
	callback            func(llmtypes.StreamChunk)
	startTime           time.Time
	turn                int // conversation turn for event emission
	// CLIToolCalls accumulates completed tool call chunks from CLI providers (Claude Code,
	// Claude Code, Codex CLI). Used by AskWithHistory to reconstruct conversation history
	// with tool calls that ran inside the CLI subprocess.
	CLIToolCalls []llmtypes.StreamChunk
	// toolNameByCallID records the name each ToolCallStart chunk carried, so a
	// ToolCallEnd chunk that arrives with an empty ToolName (the CLI stream
	// occasionally loses it between start and end) can recover it from its own
	// start event instead of logging a blank, unattributable [TOOL_ERROR]
	// marker. Scoped to one streaming call's lifetime, matching sm itself.
	toolNameByCallID map[string]string
	// streamDebugFile is an optional per-turn append-only log of every
	// chunk.Content emitted by the adapter. Off by default; toggled on by
	// MCP_AGENT_STREAM_DEBUG=1. Useful when you need to verify "did the
	// model actually emit X" vs. "did the frontend drop X" — the in-memory
	// event store doesn't persist streamed text otherwise.
	streamDebugFile *os.File
}

// startStreaming initializes streaming if enabled and on the first attempt
func (a *Agent) startStreaming(ctx context.Context, attempt int, turn int, opts *[]llmtypes.CallOption) *streamingManager {
	if !a.enableStreaming || attempt != 0 {
		return nil
	}

	sm := &streamingManager{
		accountConnectionID: a.getLLMModelConfig().ConnectionID,
		streamChan:          make(chan llmtypes.StreamChunk, 100),
		streamingDone:       make(chan bool, 1),
		startTime:           time.Now(),
		turn:                turn,
		suppressEvents:      a.suppressGenerationStreamingEvents,
		callback:            streamingCallbackFromContext(ctx, a.streamingCallback),
	}

	// Per-session/turn raw-stream debug log. Reuses the LOG_AGENT_PROMPTS
	// toggle and the existing logs/agent_prompts/<session>/ directory so
	// the streamed assistant content sits next to its turn's prompt and
	// _conversation.md files — same lifecycle, same cleanup. Captures every
	// chunk.Content emitted by the adapter byte-for-byte, so you can answer
	// "did the model emit X" vs. "did the frontend drop X" without grepping
	// the in-memory event store.
	if os.Getenv("LOG_AGENT_PROMPTS") == "true" && strings.TrimSpace(a.sessionID) != "" {
		sessionDir := agentPromptLogSessionDirName(a.sessionID)
		dir := agentPromptLogSessionDir(a.sessionID)
		if err := os.MkdirAll(dir, 0o750); err == nil {
			name := fmt.Sprintf("stream_turn-%03d_attempt-%d_%s.txt", turn, attempt, time.Now().UTC().Format("150405"))
			// Debug-only sink gated by LOG_AGENT_PROMPTS; path is a fixed
			// logs/agent_prompts root + sanitized session id + generated name.
			// #nosec G304 -- not user-controlled file inclusion.
			if f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
				fmt.Fprintf(f, "# session=%s turn=%d attempt=%d provider=%s model=%s start=%s\n",
					a.sessionID, turn, attempt, a.provider, a.modelID, time.Now().UTC().Format(time.RFC3339Nano))
				sm.streamDebugFile = f
				pruneAgentPromptLogSessions(sessionDir)
			}
		}
	}

	*opts = append(*opts, llmtypes.WithStreamingChan(sm.streamChan))

	if !sm.suppressEvents {
		a.emitTypedEvent(ctx, &events.StreamingStartEvent{
			BaseEventData: events.BaseEventData{Timestamp: time.Now()},
			Model:         a.modelID,
			Provider:      string(a.provider),
		})
	}

	go sm.processChunks(ctx, a)
	return sm
}

// processChunks runs in a goroutine to handle incoming streaming chunks
// contentChunkSource labels a content StreamingChunkEvent so consumers can tell
// clean transcript-tailed text from other content without heuristics. Providers
// that stream from the CLI transcript/markers tag chunks with a
// "<provider>_stream_source":"transcript" metadata key; when present this returns
// "transcript", otherwise "content". Terminal snapshots are labelled separately.
func contentChunkSource(chunk llmtypes.StreamChunk) string {
	for k, v := range chunk.Metadata {
		if !strings.HasSuffix(k, "_stream_source") {
			continue
		}
		if s, ok := v.(string); ok && s == events.StreamingChunkSourceTranscript {
			return events.StreamingChunkSourceTranscript
		}
	}
	return events.StreamingChunkSourceContent
}

// contentChunkIsDelta reports whether a provider content chunk is a token-level
// delta (marked with llmtypes.ContentDeltaMetadataKey by providers whose stream
// splits mid-word, e.g. pi's markers) so the mcpagent StreamingChunkEvent can
// carry that granularity to downstream consumers.
// Only presentation framing crosses this boundary; unrelated provider metadata
// must not become app event policy or model input.
func assistantBlockMetadata(chunk llmtypes.StreamChunk) map[string]interface{} {
	id, _ := chunk.Metadata[llmtypes.AssistantBlockIDMetadataKey].(string)
	if id == "" {
		return nil
	}
	return map[string]interface{}{llmtypes.AssistantBlockIDMetadataKey: id}
}

func contentChunkIsDelta(chunk llmtypes.StreamChunk) bool {
	if chunk.Metadata == nil {
		return false
	}
	v, ok := chunk.Metadata[llmtypes.ContentDeltaMetadataKey].(bool)
	return ok && v
}

func (sm *streamingManager) processChunks(ctx context.Context, a *Agent) {
	if sm.callback == nil {
		sm.callback = a.streamingCallback
	}
	defer func() {
		if sm.streamDebugFile != nil {
			fmt.Fprintf(sm.streamDebugFile, "\n# end %s totalChunks=%d sawTerminal=%t\n",
				time.Now().UTC().Format(time.RFC3339Nano), sm.totalChunks, sm.sawTerminal)
			_ = sm.streamDebugFile.Close()
			sm.streamDebugFile = nil
		}
		sm.streamingDone <- true
	}()

	for chunk := range sm.streamChan {
		switch chunk.Type {
		case llmtypes.StreamChunkTypeNativeGoal:
			if chunk.NativeGoal != nil {
				a.emitTypedEvent(ctx, &events.NativeGoalEvent{BaseEventData: events.BaseEventData{Timestamp: time.Now()}, Update: *chunk.NativeGoal})
				if sm.callback != nil {
					sm.callback(chunk)
				}
			}
		case llmtypes.StreamChunkTypeNativeSubagentTranscript:
			raw, err := json.Marshal(chunk.Metadata["native_subagents"])
			if err == nil {
				var transcripts []llmtypes.NativeSubagentTranscript
				if json.Unmarshal(raw, &transcripts) == nil && len(transcripts) > 0 {
					a.emitTypedEvent(ctx, &events.NativeSubagentTranscriptEvent{BaseEventData: events.BaseEventData{Timestamp: time.Now()}, Transcripts: transcripts})
				}
			}
		case llmtypes.StreamChunkTypeReasoning:
			thinkingActive, _ := chunk.Metadata["thinking_active"].(bool)
			if chunk.Content != "" || thinkingActive {
				sm.totalChunks++
				if !sm.suppressEvents {
					// Preserve presentation independently of reasoning semantics: a
					// visible progress summary is not a final answer or completion.
					var metadata map[string]interface{}
					if chunk.Metadata["presentation"] == "assistant_update" {
						metadata = map[string]interface{}{"presentation": "assistant_update"}
					}
					if thinkingActive {
						if metadata == nil {
							metadata = make(map[string]interface{})
						}
						metadata["thinking_active"] = true
					}
					a.emitTypedEvent(ctx, &events.ConversationThinkingEvent{
						BaseEventData: events.BaseEventData{Timestamp: time.Now(), Metadata: metadata},
						Thinking:      chunk.Content,
						Turn:          sm.turn,
						IsDelta:       contentChunkIsDelta(chunk),
					})
				}
				if sm.callback != nil {
					sm.callback(chunk)
				}
			}

		case llmtypes.StreamChunkTypeAssistantBlock:
			metadata := assistantBlockMetadata(chunk)
			phase, _ := chunk.Metadata[llmtypes.AssistantBlockPhaseMetadataKey].(string)
			if metadata == nil || sm.suppressEvents {
				continue
			}
			metadata[llmtypes.AssistantBlockPhaseMetadataKey] = phase
			switch phase {
			case "start":
				// A real native text start, not a turn start. Reuse the existing
				// stream envelope so replay and row positioning have one path.
				sm.contentChunkIndex = 0
				a.emitTypedEvent(ctx, &events.StreamingStartEvent{BaseEventData: events.BaseEventData{Timestamp: time.Now(), Metadata: metadata}, Model: a.modelID, Provider: string(a.provider)})
			case "end":
				sm.contentChunkIndex++
				a.emitTypedEvent(ctx, &events.StreamingChunkEvent{BaseEventData: events.BaseEventData{Timestamp: time.Now(), Metadata: metadata}, Content: chunk.Content, ChunkIndex: sm.contentChunkIndex, Source: events.StreamingChunkSourceTranscript})
			}
			if sm.callback != nil {
				sm.callback(chunk)
			}

		case llmtypes.StreamChunkTypeContent:
			if chunk.Content != "" {
				sm.contentChunkIndex++
				sm.totalChunks++

				if sm.streamDebugFile != nil {
					fmt.Fprintf(sm.streamDebugFile, "[content idx=%d] %s\n---\n", sm.contentChunkIndex, chunk.Content)
				}

				if !sm.suppressEvents {
					a.emitTypedEvent(ctx, &events.StreamingChunkEvent{
						BaseEventData: events.BaseEventData{Timestamp: time.Now(), Metadata: assistantBlockMetadata(chunk)},
						Content:       chunk.Content,
						ChunkIndex:    sm.contentChunkIndex,
						IsToolCall:    false,
						Source:        contentChunkSource(chunk),
						IsDelta:       contentChunkIsDelta(chunk),
					})
				}

				if sm.callback != nil {
					sm.callback(chunk)
				}
			}

		case llmtypes.StreamChunkTypePromptAccepted:
			// The CLI took the prompt: the turn really began. No text; the app stops saying "Starting <agent>".
			a.emitTypedEvent(ctx, &events.StreamingChunkEvent{
				BaseEventData: events.BaseEventData{Timestamp: time.Now()},
				Source:        events.StreamingChunkSourcePromptAccepted,
			})

		case llmtypes.StreamChunkTypeTerminal:
			if chunk.Content != "" {
				sm.sawTerminal = true
				sm.contentChunkIndex++
				sm.totalChunks++
				if sm.streamDebugFile != nil {
					fmt.Fprintf(sm.streamDebugFile, "[terminal idx=%d] %s\n---\n", sm.contentChunkIndex, chunk.Content)
				}
				metadata := map[string]interface{}{
					"kind":     "terminal",
					"replace":  true,
					"provider": string(a.provider),
				}
				for key, value := range chunk.Metadata {
					metadata[key] = value
				}

				// Terminal pane snapshots are NOT generation streaming
				// events — they're a separate UX channel that the
				// builder's terminal store consumes. Emit them even
				// when suppressEvents (set via withGenerationStreamingEvents(false))
				// disables per-token chat-content streaming. Without
				// this, the terminal panel goes empty for every tmux
				// coding-agent call.
				a.emitTypedEvent(ctx, &events.StreamingChunkEvent{
					BaseEventData: events.BaseEventData{
						Timestamp: time.Now(),
						Metadata:  metadata,
					},
					Content:    chunk.Content,
					ChunkIndex: sm.contentChunkIndex,
					IsToolCall: false,
					Source:     events.StreamingChunkSourceTerminal,
				})

				if sm.callback != nil {
					sm.callback(chunk)
				}
			}

		case llmtypes.StreamChunkTypeToolCallStart:
			if sm.toolArgsByCallID == nil {
				sm.toolArgsByCallID = map[string]string{}
			}
			if chunk.ToolCallID != "" {
				sm.toolArgsByCallID[chunk.ToolCallID] = chunk.ToolArgs
			}
			// Determine source label from provider
			sourceLabel := string(a.provider)
			if sourceLabel == "" {
				sourceLabel = "cli"
			}
			toolStartEvent := events.NewToolCallStartEventWithCorrelation(
				sm.turn,
				chunk.ToolName,
				events.ToolParams{Arguments: chunk.ToolArgs},
				sourceLabel,
				string(a.traceID), string(a.traceID),
			)
			toolStartEvent.ToolCallID = chunk.ToolCallID
			a.emitTypedEvent(ctx, toolStartEvent)

			// Recorded so a same-call ToolCallEnd that arrives with an empty
			// ToolName (the CLI stream occasionally loses it between start and
			// end) can recover the name this start event already carried,
			// rather than logging an unattributable [TOOL_ERROR] marker.
			if chunk.ToolCallID != "" && chunk.ToolName != "" {
				if sm.toolNameByCallID == nil {
					sm.toolNameByCallID = make(map[string]string)
				}
				sm.toolNameByCallID[chunk.ToolCallID] = chunk.ToolName
			}

		case llmtypes.StreamChunkTypeToolCallEnd:
			sourceLabel := string(a.provider)
			if sourceLabel == "" {
				sourceLabel = "cli"
			}
			// Resolve identity before classification. CanonicalFailureForTool has
			// deliberate per-tool suppression for tools whose normal payload can
			// contain records such as {"status":"failed"}. Classifying with the
			// empty end-chunk name and recovering it only for logging would turn
			// legitimate domain data into a false ToolCallError event.
			//
			// The structured call identity (this call's own start event) is
			// authoritative and tried first. The envelope regex is a last-resort
			// fallback for calls with no recorded start. A marker must never go out
			// unattributed, so an unprovable name becomes explicit "unknown".
			toolName := chunk.ToolName
			if toolName == "" && chunk.ToolCallID != "" {
				toolName = sm.toolNameByCallID[chunk.ToolCallID]
			}
			if toolName == "" {
				toolName, _ = toolerr.ToolNameFromResult(chunk.ToolResult)
			}
			if toolName == "" {
				toolName = "unknown"
			}
			chunk.ToolName = toolName

			if signal, failed := toolerr.CanonicalFailureForTool(toolName, chunk.ToolResult); failed {
				if v2Logger := getLogger(a); v2Logger != nil {
					v2Logger.Error(toolerr.Marker+" CLI tool payload failure", nil,
						loggerv2.String("layer", "cli_stream_adapter"),
						loggerv2.String("tool", toolName),
						loggerv2.String("session_id", a.sessionID),
						loggerv2.String("signal", signal),
						loggerv2.String("result", toolerr.TruncateForLog(chunk.ToolResult)))
				}
				toolErrorEvent := events.NewToolCallErrorEvent(sm.turn, toolName, chunk.ToolResult, sourceLabel, chunk.ToolDuration)
				toolErrorEvent.ToolCallID = chunk.ToolCallID
				a.emitTypedEvent(ctx, toolErrorEvent)
			} else {
				args := chunk.ToolArgs
				if args == "" {
					args = sm.toolArgsByCallID[chunk.ToolCallID]
				}
				if plan, ok := sm.successfulToolPlan(ctx, toolName, args, chunk.ToolResult); ok {
					sm.emitCodingAgentPlan(ctx, a, plan)
				}
				toolEndEvent := events.NewToolCallEndEventWithTokenUsageAndModel(
					sm.turn,
					toolName,
					chunk.ToolResult,   // tool execution result from CLI
					sourceLabel,        // serverName
					chunk.ToolDuration, // duration from start to tool_result
					"",                 // spanID
					0, 0, 0,            // context usage metrics (not available)
					a.modelID,
				)
				toolEndEvent.ToolCallID = chunk.ToolCallID
				a.emitTypedEvent(ctx, toolEndEvent)
			}

			// Accumulate for conversation history reconstruction (all CLI providers).
			sm.CLIToolCalls = append(sm.CLIToolCalls, chunk)

			// Forward to StreamingCallback so wrappers (e.g. LLMAgentWrapper) can track
			// completed tool calls for history reconstruction on cancellation.
			if sm.callback != nil {
				sm.callback(chunk)
			}

		case llmtypes.StreamChunkTypeNativeRetry:
			if retry := chunk.NativeRetry; retry != nil && (retry.Phase == "start" || retry.Phase == "end") {
				attempt := retry.Attempt
				if attempt < 1 {
					attempt = 1
				}
				phase := "native_retry_start"
				if retry.Phase == "end" {
					phase = "native_retry_end"
				}
				retryError := ""
				if retry.Phase == "end" && !retry.Success {
					retryError = "The provider request failed after retrying."
				}
				notice := events.NewRetryAttemptEvent(sm.turn, attempt-1, retry.MaxAttempts, a.modelID, retry.Provider, phase, retry.Success, time.Duration(retry.DelayMS)*time.Millisecond, retryError)
				notice.Metadata = map[string]interface{}{"source": "native_cli", "native_session_id": retry.NativeSessionID, "native_turn_id": retry.NativeTurnID}
				a.emitTypedEvent(ctx, notice)
			}

		case llmtypes.StreamChunkTypeCodingAgentPlan:
			sm.emitCodingAgentPlan(ctx, a, chunk.CodingAgentPlan)

		case llmtypes.StreamChunkTypeContextCompaction:
			// Like status lines and terminal frames, compaction is not
			// generation streaming: emit it even when suppressEvents is set.
			if c := chunk.ContextCompaction; c != nil {
				a.emitTypedEvent(ctx, contextCompactionEvent(c, string(a.provider)))
			}

		case llmtypes.StreamChunkTypeStatusLine:
			if chunk.StatusLine != nil {
				// Carry the owning tmux session (when the provider supplies it) so
				// downstream consumers can target the exact pane instead of every
				// terminal in the session.
				tmuxSession, _ := chunk.StatusLine.Metadata["tmux_session"].(string)
				a.emitTypedEvent(ctx, &events.StreamingStatusLineEvent{
					ConnectionID:             sm.accountConnectionID,
					BaseEventData:            events.BaseEventData{Timestamp: time.Now()},
					Provider:                 chunk.StatusLine.Provider,
					Model:                    chunk.StatusLine.Model,
					TmuxSession:              tmuxSession,
					InputTokens:              chunk.StatusLine.InputTokens,
					OutputTokens:             chunk.StatusLine.OutputTokens,
					CacheCreationInputTokens: chunk.StatusLine.CacheCreationInputTokens,
					CacheReadInputTokens:     chunk.StatusLine.CacheReadInputTokens,
					TotalInputTokens:         chunk.StatusLine.TotalInputTokens,
					TotalOutputTokens:        chunk.StatusLine.TotalOutputTokens,
					CostUSD:                  chunk.StatusLine.CostUSD,
					Metadata:                 chunk.StatusLine.Metadata,
				})
				if sm.callback != nil {
					sm.callback(chunk)
				}
			}
		}
	}
}

func streamingCallbackFromContext(ctx context.Context, fallback func(llmtypes.StreamChunk)) func(llmtypes.StreamChunk) {
	if callback, ok := ctx.Value(turnStreamingCallbackContextKey{}).(func(llmtypes.StreamChunk)); ok && callback != nil {
		return callback
	}
	return fallback
}

// finishStreaming waits for streaming to complete and emits the end event
func (a *Agent) finishStreaming(ctx context.Context, sm *streamingManager, resp *llmtypes.ContentResponse) {
	if sm == nil {
		return
	}

	// If executeLLM failed before calling GenerateContent (e.g. InitializeLLM error),
	// the adapter's deferred close never ran, so close the channel here to unblock
	// processChunks. Use recover to safely handle the "close of closed channel" case.
	func() {
		defer func() { recover() }() //nolint:errcheck
		close(sm.streamChan)
	}()

	<-sm.streamingDone

	// Under production config (suppressEvents=true) we still need to
	// fire the StreamingEndEvent for terminal streams — the terminals
	// store reads it to flip terminal panes from active to inactive.
	// Without this carve-out, cancelled workflow steps leave their
	// terminal entries permanently "active" in the frontend.
	if sm.suppressEvents && !sm.sawTerminal {
		return
	}

	endEvent := &events.StreamingEndEvent{
		BaseEventData: events.BaseEventData{Timestamp: time.Now()},
		TotalChunks:   sm.totalChunks,
		Duration:      time.Since(sm.startTime).String(),
	}
	if sm.sawTerminal {
		endEvent.Metadata = map[string]interface{}{
			"kind":        "terminal",
			"provider":    string(a.provider),
			"duration_ms": time.Since(sm.startTime).Milliseconds(),
		}
	}

	if resp != nil && len(resp.Choices) > 0 && resp.Choices[0].GenerationInfo != nil {
		genInfo := resp.Choices[0].GenerationInfo
		if genInfo.TotalTokens != nil {
			endEvent.TotalTokens = *genInfo.TotalTokens
		}
		if resp.Choices[0].StopReason != "" {
			endEvent.FinishReason = resp.Choices[0].StopReason
		}
		// Surface per-call tokens + cost on the streaming_end metadata
		// so the terminals store can populate Status.{InputTokens,
		// OutputTokens, CostUSD} without needing to regex-parse a
		// "[done · X in · Y out · $Z]" trailer out of pane content.
		// This is the structured replacement for the synthetic terminal
		// summary line we suppressed for tmux transports.
		if sm.sawTerminal && endEvent.Metadata != nil {
			if genInfo.PromptTokens != nil {
				endEvent.Metadata["input_tokens"] = *genInfo.PromptTokens
			} else if genInfo.InputTokens != nil {
				endEvent.Metadata["input_tokens"] = *genInfo.InputTokens
			}
			if genInfo.CompletionTokens != nil {
				endEvent.Metadata["output_tokens"] = *genInfo.CompletionTokens
			} else if genInfo.OutputTokens != nil {
				endEvent.Metadata["output_tokens"] = *genInfo.OutputTokens
			}
			if genInfo.Additional != nil {
				if cost, ok := genInfo.Additional["cost_usd_estimated"].(float64); ok && cost > 0 {
					endEvent.Metadata["cost_usd_estimated"] = cost
				}
			}
		}
		// Extract provider-specific metadata. Prefer the normalized typed session
		// handle for process identity; legacy provider keys remain fallbacks for
		// older adapters and persisted responses.
		if sm.sawTerminal && endEvent.Metadata != nil {
			if retentionSeconds := terminalRetentionSecondsFromGenerationInfo(genInfo.Additional); retentionSeconds > 0 {
				endEvent.Metadata["terminal_retention_seconds"] = retentionSeconds
			}
			if tmuxSession := terminalTmuxSessionFromGenerationInfo(genInfo); tmuxSession != "" {
				endEvent.Metadata["tmux_session"] = tmuxSession
			}
		}
		if additional := genInfo.Additional; additional != nil {
			// Claude Code metadata
			if model, ok := additional["claude_code_model"].(string); ok && endEvent.ResolvedModel == "" {
				endEvent.ResolvedModel = model
			}
		}
		// Populate cache tokens from CachedContentTokens (set by both adapters)
		if genInfo.CachedContentTokens != nil {
			endEvent.CacheTokens = *genInfo.CachedContentTokens
		}
	}
	a.emitTypedEvent(ctx, endEvent)
}

func terminalRetentionSecondsFromGenerationInfo(additional map[string]interface{}) int {
	for _, key := range []string{
		"terminal_retention_seconds",
		"claude_code_interactive_retention_seconds",
		"codex_interactive_retention_seconds",
		"gemini_interactive_retention_seconds",
		"cursor_interactive_retention_seconds",
		"pi_interactive_retention_seconds",
	} {
		if seconds := generationInfoIntValue(additional[key]); seconds > 0 {
			return seconds
		}
	}
	return 0
}

func terminalTmuxSessionFromGenerationInfo(genInfo *llmtypes.GenerationInfo) string {
	if handle, ok := llmtypes.ExtractCodingProviderSessionHandle(genInfo); ok {
		if tmuxSession := strings.TrimSpace(handle.TmuxSession); tmuxSession != "" {
			return tmuxSession
		}
	}
	if genInfo == nil {
		return ""
	}
	additional := genInfo.Additional
	for _, key := range []string{
		"claude_code_interactive_session",
		"claude_code_session",
		"codex_interactive_session",
		"gemini_interactive_session",
		"cursor_interactive_session",
		"pi_interactive_session",
	} {
		if value := strings.TrimSpace(fmt.Sprint(additional[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func generationInfoIntValue(value interface{}) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case int32:
		return int(typed)
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	case json.Number:
		i, _ := typed.Int64()
		return int(i)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(typed))
		return i
	default:
		return 0
	}
}

// getEffectiveLLMConfig returns the agent's unified LLM configuration.
func (a *Agent) getEffectiveLLMConfig() AgentLLMConfiguration {
	var config AgentLLMConfiguration

	// If the new config is populated, use it as base
	if a.llmConfig.Primary.ModelID != "" && a.llmConfig.Primary.Provider != "" {
		config = a.llmConfig
	} else {
		// Otherwise, build from legacy fields
		config = AgentLLMConfiguration{
			Primary: LLMModel{
				Provider: string(a.provider),
				ModelID:  a.modelID,
				// Note: API Key not easily accessible from legacy Agent struct without introspection
				// but executeLLM will handle this by checking Agent.APIKeys if model.APIKey is nil
			},
		}
	}

	return config
}

// executeLLM creates an LLM instance and executes it.
func (a *Agent) executeLLM(ctx context.Context, model LLMModel, messages []llmtypes.MessageContent, opts []llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	return a.executeLLMInner(ctx, model, messages, opts, false)
}

func (a *Agent) executeLLMForCodingAgentTransportLaunch(ctx context.Context, model LLMModel, opts []llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	// Carry the agent's accumulated system prompt through the
	// launch-only contract so the adapter projects its provider-specific
	// rule file (mlp-system.mdc / mlp-system.md / AGENTS.md / GEMINI.md /
	// CLAUDE.md) on resume. Without this, launch-only sends nil messages
	// → adapter's split*SystemPrompt returns empty → prepare*ProjectFiles
	// skips the rule-file write → user sees an empty .cursor/rules/
	// directory mid-conversation.
	if sp := strings.TrimSpace(a.outgoingSystemPromptForContext(ctx)); sp != "" {
		opts = append(opts, llmtypes.WithCodingProviderLaunchSystemPrompt(sp))
	}
	return a.executeLLMInner(ctx, model, nil, opts, true)
}

func (a *Agent) appendPiCLIIntegrationOptions(opts []llmtypes.CallOption) ([]llmtypes.CallOption, error) {
	if bridgeConfig, bridgeErr := a.buildBridgeMCPConfig(); bridgeErr == nil {
		// Honor the same tools-mode contract Claude/Codex/Cursor consult.
		// Hardcoding bridge-only here made AgentToolsMode provider-dependent:
		// a caller selecting native tools got them everywhere except Pi,
		// silently, with no error to explain the difference.
		// Full CLI (confined by Landlock/Seatbelt) gives Pi its own tools, shell included, like the other CLIs
		// (owner, 2026-10-20: native shell on everywhere, superseding Pi's bridge-only rule from PLAT-390).
		// Unconfined or mcp_only stays bridge-only.
		bridgeOnly := !a.fullCLIEnabled()
		opts = append(opts,
			llm.WithPiMCPConfig(bridgeConfig),
			llm.WithPiBridgeOnlyTools(bridgeOnly),
		)
		if a.bridgeReadyFile != "" {
			// Hold a cold pi session's first prompt until the bridge reports the
			// tools connected (tools/list answered) — see BuildBridgeMCPConfig.
			opts = append(opts, llm.WithMCPReadyFile(a.bridgeReadyFile))
		}
		if bridgeOnly {
			a.logger.Info("🌉 [PI_CLI] Configured native MCP bridge through private session mcp.json with built-in tools disabled")
		} else {
			a.logger.Info("🌉 [PI_CLI] Configured native MCP bridge through private session mcp.json with native built-in tools enabled")
		}
	} else {
		return nil, fmt.Errorf("Pi CLI requires the MCP bridge: %w", bridgeErr)
	}
	if a.piSessionID != "" {
		opts = append(opts, llm.WithPiResumeSessionID(a.piSessionID))
	}
	a.logger.Info("⏱️ [PI_CLI] Native MCP request timeouts complement request cancellation and the mcpbridge HTTP backstop")
	a.logger.Info("🌉 Using Pi CLI through native RPC with MCP bridge and live input support")
	return opts, nil
}

func (a *Agent) executeLLMInner(ctx context.Context, model LLMModel, messages []llmtypes.MessageContent, opts []llmtypes.CallOption, launchOnly bool) (*llmtypes.ContentResponse, error) {
	opts = appendManagedCodingAgentReleaseSession(opts, os.Getenv("AGENTWORKS_MANAGED_CLI_BIN"), a.sessionID)
	if len(a.codingAgentSecretEnvironment) > 0 {
		opts = append(opts, llmtypes.WithCodingAgentSecretEnvironment(a.codingAgentSecretEnvironment))
	}
	return a.executeLLMInnerAttempt(ctx, model, messages, opts, launchOnly, true)
}

func appendManagedCodingAgentReleaseSession(opts []llmtypes.CallOption, managedBin, sessionID string) []llmtypes.CallOption {
	if managedBin == "" || sessionID == "" || sessionID == "global" {
		return opts
	}
	return append(opts, llmtypes.WithCodingAgentReleaseSession(sessionID))
}

func (a *Agent) executeLLMInnerAttempt(ctx context.Context, model LLMModel, messages []llmtypes.MessageContent, opts []llmtypes.CallOption, launchOnly, allowMissingSessionRecovery bool) (*llmtypes.ContentResponse, error) {
	// Keep the caller-owned options before provider integration appends native
	// resume metadata. A stale-session retry must rebuild those options after
	// clearing the bad ID; reusing the mutated slice can send --resume again.
	baseOpts := append([]llmtypes.CallOption(nil), opts...)
	// Thread attached skills through opts so CLI transport adapters can
	// project SKILL.md folders to disk via ProjectSkills at session
	// launch. API transports use the intrinsic read_skill tool and can ignore
	// this projection metadata.
	// Centralized here so every LLM call (chat, launch-only, retries)
	// carries the same skill set; individual call sites do not need to
	// re-append it.
	if skills := a.attachedSkills; len(skills) > 0 {
		opts = append(opts, llmtypes.WithAttachedSkills(skills))
	}

	// Clone agent-level keys as base (so Azure and Bedrock configs are always available)
	apiKeys := a.apiKeys.Clone()
	if apiKeys == nil {
		apiKeys = &llm.ProviderAPIKeys{}
	}

	// Override with model-specific key if available (for simple API key providers)
	if model.APIKey != nil {
		apiKeys.SetKeyForProvider(llmproviders.Provider(model.Provider), model.APIKey)
	}

	if model.Region != nil && llmproviders.Provider(model.Provider) == llmproviders.ProviderBedrock {
		if apiKeys.Bedrock == nil {
			apiKeys.Bedrock = &llm.BedrockConfig{}
		}
		apiKeys.Bedrock.Region = *model.Region
	}

	// Use model's temperature if available, otherwise fallback to agent's temperature
	temperature := a.temperature
	if model.Temperature != nil {
		temperature = *model.Temperature
	}

	modelProvider := llm.Provider(model.Provider)
	opts = a.appendCodingAgentInteractiveOptionsForProvider(opts, modelProvider, model.ModelID)
	// Building the coding options above made this session's bridge; its shell credentials join the child's
	// environment (the later option replaces the earlier secrets-only one).
	if modelProvider == llm.ProviderCursorCLI {
		// PLAT-872-50: normal native continuation sends only the newest text.
		// A refused load needs this chat's history and must persist its new ID
		// before sending, including when the subsequent turn fails.
		opts = append(opts, func(options *llmtypes.CallOptions) {
			options.CodingProviderHistory = messages
			options.OnCodingProviderSession = func(handle llmtypes.CodingProviderSessionHandle) {
				a.updateCodingProviderSessionHandleFromResponse(&llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{GenerationInfo: &llmtypes.GenerationInfo{CodingProviderSessionHandle: &handle}}}})
			}
		})
	}
	continuationHandle, hasContinuationHandle := a.codingProviderContinuationHandleForModel(modelProvider, model.ModelID)
	if launchOnly && !hasContinuationHandle {
		return nil, fmt.Errorf("cannot launch coding-agent continuation transport: native session handle is missing")
	}

	llmInstance, err := llm.InitializeLLM(llm.Config{
		ConnectionID:        model.ConnectionID,
		Provider:            modelProvider,
		ModelID:             model.ModelID,
		Temperature:         temperature,
		Logger:              a.logger,
		APIKeys:             apiKeys,
		Tracers:             a.tracers,
		TraceID:             a.traceID,
		Context:             ctx,
		ClaudeCodeTransport: a.claudeCodeTransport,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize LLM: %w", err)
	}

	var integrationErr error
	opts, integrationErr = applyCodingAgentIntegrationOptions(a, opts, model)
	if integrationErr != nil {
		return nil, integrationErr
	}

	// Apply model options for all providers (reasoning_effort, thinking_level, etc.)
	if model.Options != nil {
		if effort, ok := model.Options["reasoning_effort"].(string); ok && effort != "" && llmproviders.Provider(model.Provider) != llmproviders.ProviderCodexCLI && llmproviders.Provider(model.Provider) != llmproviders.ProviderCursorCLI {
			opts = append(opts, llmtypes.WithReasoningEffort(effort))
		}
		if level, ok := model.Options["thinking_level"].(string); ok && level != "" {
			opts = append(opts, llmtypes.WithThinkingLevel(level))
		}
		if budget, ok := numericOption(model.Options["thinking_budget"]); ok && budget > 0 {
			opts = append(opts, llmtypes.WithThinkingBudget(int(budget)))
		}
		// Sampling controls. Provider-agnostic; adapters only forward
		// to the wire when they accept the field. JSON unmarshals
		// numeric values into float64, so we accept either int or
		// float types via numericOption.
		if topP, ok := numericOption(model.Options["top_p"]); ok && topP > 0 {
			opts = append(opts, llmtypes.WithTopP(topP))
		}
		if topK, ok := numericOption(model.Options["top_k"]); ok && topK > 0 {
			opts = append(opts, llmtypes.WithTopK(int(topK)))
		}
		if stops, ok := stringSliceOption(model.Options["stop_sequences"]); ok && len(stops) > 0 {
			opts = append(opts, llmtypes.WithStopSequences(stops))
		}
	}

	if hasContinuationHandle {
		if launchOnly {
			transport := strings.TrimSpace(continuationHandle.Transport)
			if transport == "" {
				if contract, ok := llm.GetCodingAgentProviderContract(modelProvider, model.ModelID); ok {
					transport = string(contract.Transport)
				}
			}
			probe := &llmtypes.CallOptions{}
			for _, opt := range opts {
				opt(probe)
			}
			if llmtypes.CodingProviderIdleCompactFromOptions(probe) {
				a.logger.Info(fmt.Sprintf("🔁 [CODING_AGENT_CONTINUATION] Idle compaction of %s (%s) native session %s", model.Provider, transport, continuationHandle.NativeSessionID))
				return llm.CompactCodingAgentSession(ctx, llmInstance, continuationHandle, opts...)
			}
			a.logger.Info(fmt.Sprintf("🔁 [CODING_AGENT_CONTINUATION] Starting %s transport session (%s) for native session %s", model.Provider, transport, continuationHandle.NativeSessionID))
			return llm.StartCodingAgentTransportSession(ctx, llmInstance, continuationHandle, opts...)
		}
		latestMessage, msgOK := pendingHumanTextForProviderContinuation(messages)
		if !msgOK {
			return nil, fmt.Errorf("cannot continue coding-agent session: latest human message not found")
		}
		// Carry the system prompt through the continuation path too —
		// without it, ContinueCodingAgentSession builds messages with
		// only a Human entry and the adapter's split*SystemPrompt
		// returns empty, causing prepare*ProjectFiles to skip the rule
		// file and the "launch configuration changed" guard to recycle
		// the tmux session mid-chat.
		continuationOpts := opts
		if sp := strings.TrimSpace(a.outgoingSystemPromptForContext(ctx)); sp != "" {
			continuationOpts = append(continuationOpts, llmtypes.WithCodingProviderLaunchSystemPrompt(sp))
		}
		a.logger.Info(fmt.Sprintf("🔁 [CODING_AGENT_CONTINUATION] Continuing %s with native session %s", model.Provider, continuationHandle.NativeSessionID))
		resp, continuationErr := llm.ContinueCodingAgentSession(ctx, llmInstance, continuationHandle, latestMessage, continuationOpts...)
		if continuationErr == nil || !allowMissingSessionRecovery ||
			!isMissingCodingProviderNativeSessionError(modelProvider, continuationErr) {
			return resp, continuationErr
		}

		a.clearCodingProviderNativeSession(modelProvider, continuationHandle.NativeSessionID)
		if a.logger != nil {
			a.logger.Warn(fmt.Sprintf(
				"Native %s conversation %q no longer exists; retrying the current turn once as a fresh conversation while preserving AgentWorks history",
				model.Provider,
				continuationHandle.NativeSessionID,
			))
		}
		// Drop the stream channel for the retry: the failed first attempt
		// already ran the adapter's `defer close(opts.StreamChan)` on its way
		// out, so reusing baseOpts hands the retry an ALREADY-CLOSED channel.
		// Its adapter then sends the first assistant chunk into it and the
		// process dies with "panic: send on closed channel" — a crash, not a
		// failed turn. Observed live: a stale --resume id fails, this recovery
		// re-runs the turn fresh, and the claude structured adapter's scanner
		// goroutine panics mid-stream.
		//
		// Same rule llmproviders.ContinueCodingAgentSession already follows for
		// its tmux-session-lost retry ("the first failed GenerateContent owns
		// closing it"). The recovered turn loses mid-turn streaming but still
		// returns its full response; a lost stream beats a dead server.
		return a.executeLLMInnerAttempt(ctx, model, messages, staleSessionRetryOptions(baseOpts), false, false)
	}

	return llmInstance.GenerateContent(ctx, messages, opts...)
}

// staleSessionRetryOptions copies the caller's options for a stale-native-session
// retry with the stream channel removed. See the call site for why: the first
// attempt's adapter already closed that channel, so the retry must not be handed
// it. Kept as a named function so the invariant is testable on its own.
func staleSessionRetryOptions(baseOpts []llmtypes.CallOption) []llmtypes.CallOption {
	retryOpts := append([]llmtypes.CallOption(nil), baseOpts...)
	return append(retryOpts, llmtypes.WithStreamingChan(nil))
}

// StartCodingAgentTransportSession starts or reacquires the agent's current
// launchable coding-agent transport without sending a user message. Terminal
// chunks are emitted through the agent's normal event listeners, but no
// streaming_end event is emitted because this is an idle terminal warmup rather
// than a completed generation turn.
func (a *Agent) startCodingAgentTransportSession(ctx context.Context) (*llmtypes.CodingProviderSessionHandle, error) {
	if a == nil {
		return nil, fmt.Errorf("agent is nil")
	}
	contract, ok := a.codingContract()
	if !ok {
		return nil, fmt.Errorf("agent provider %s/%s is not a coding-agent provider", a.provider, a.modelID)
	}
	// PLAT-856-442: imported Cursor CLI stores require tmux even when the
	// destination defaults to SDK. Use the same normalized handle as the turn.
	transport := a.codingAgentLaunchTransport(string(contract.Transport))
	if transport != llmtypes.CodingProviderTransportTmux {
		return nil, fmt.Errorf("agent provider %s/%s does not expose a launchable terminal transport (%s)", a.provider, a.modelID, transport)
	}
	if err := a.prepareCodingAgentTransportLaunch(); err != nil {
		return nil, err
	}
	primary := a.getEffectiveLLMConfig().Primary
	if strings.TrimSpace(primary.Provider) == "" {
		primary.Provider = string(a.provider)
	}
	if strings.TrimSpace(primary.ModelID) == "" {
		primary.ModelID = a.modelID
	}

	var opts []llmtypes.CallOption
	sm := a.startStreaming(ctx, 0, 0, &opts)
	resp, err := a.executeLLMForCodingAgentTransportLaunch(ctx, primary, opts)
	a.drainStreamingWithoutEnd(sm)
	if err != nil {
		return nil, err
	}
	a.updateCodingProviderSessionHandleFromResponse(resp)
	if handle := a.currentAgentSessionHandle(); handle != nil && !handle.Provider.Empty() {
		providerHandle := handle.Provider
		return &providerHandle, nil
	}
	return nil, fmt.Errorf("coding-agent transport session started without provider handle")
}

func (a *Agent) codingAgentLaunchTransport(defaultTransport string) string {
	if a.wantsStructuredTransport() {
		return llmtypes.CodingProviderTransportStructured
	}
	handle := a.withCurrentCodingTransport(a.codingProviderSessionHandle)
	if handle.Transport != "" {
		return handle.Transport
	}
	return defaultTransport
}

func (a *Agent) prepareCodingAgentTransportLaunch() error {
	if strings.TrimSpace(a.sessionID) == "" {
		return fmt.Errorf("cannot launch coding-agent transport without an owner session id")
	}
	// A restored agent may be prelaunched before ContinueConversation enables
	// persistent mode. The adapter must take its interactive launch-only path;
	// otherwise AGY routes the empty warmup to its JSON exec lane as a turn.
	a.enablePersistentInteractiveForProvider()
	return nil
}

func (a *Agent) drainStreamingWithoutEnd(sm *streamingManager) {
	if sm == nil {
		return
	}
	func() {
		defer func() { recover() }() //nolint:errcheck
		close(sm.streamChan)
	}()
	<-sm.streamingDone
}

// Preserve every queued human input; native resume already contains the
// conversation through the last assistant turn (PLAT-872).
func pendingHumanTextForProviderContinuation(messages []llmtypes.MessageContent) (string, bool) {
	var inputs []string
	for _, message := range llmtypes.PendingHumanMessages(messages) {
		var parts []string
		for _, part := range message.Parts {
			switch typed := part.(type) {
			case llmtypes.TextContent:
				parts = append(parts, typed.Text)
			case *llmtypes.TextContent:
				if typed != nil {
					parts = append(parts, typed.Text)
				}
			case string:
				parts = append(parts, typed)
			}
		}
		if text := strings.TrimSpace(strings.Join(parts, "\n")); text != "" {
			inputs = append(inputs, text)
		}
	}
	text := strings.Join(inputs, "\n\n")
	return text, text != ""
}

// GenerateContentWithRetry retries transient failures on the selected model.
func generateContentWithRetry(a *Agent, ctx context.Context, messages []llmtypes.MessageContent, opts []llmtypes.CallOption, turn int) (*llmtypes.ContentResponse, observability.UsageMetrics, error) {
	logger := getLogger(a)

	maxRetries := 5
	if env := os.Getenv("LLM_MAX_RETRIES"); env != "" {
		if val, err := strconv.Atoi(env); err == nil && val > 0 {
			maxRetries = val
		}
	}

	maxRetriesZeroCandidates := 3 // Limit retries for zero_candidates errors to 3 before returning the error
	maxRetriesEmptyContent := 2   // Empty-content errors are partly structural; 2 retries rides out transient hiccups without burning cost when failure is permanent

	baseDelaySeconds := 10
	if env := os.Getenv("LLM_RETRY_BASE_DELAY_SECONDS"); env != "" {
		if val, err := strconv.Atoi(env); err == nil && val > 0 {
			baseDelaySeconds = val
		}
	}
	baseDelay := time.Duration(baseDelaySeconds) * time.Second

	maxDelaySeconds := 300 // 5 minutes
	if env := os.Getenv("LLM_RETRY_MAX_DELAY_SECONDS"); env != "" {
		if val, err := strconv.Atoi(env); err == nil && val > 0 {
			maxDelaySeconds = val
		}
	}
	maxDelay := time.Duration(maxDelaySeconds) * time.Second
	var lastErr error
	var usage observability.UsageMetrics

	// Get effective configuration (supports new and legacy)
	llmConfig := a.getEffectiveLLMConfig()

	model := llmConfig.Primary
	key := modelQuotaKey(model)
	logger.Info(fmt.Sprintf("🔄 [DEBUG] GenerateContentWithRetry START - Messages: %d, Options: %d, Turn: %d, Quota scope: %s", len(messages), len(opts), turn, key))
	resetAt, exhausted := a.quotaExhaustedModels[key]
	if !exhausted {
		resetAt, exhausted = sharedModelQuotaExhaustion(key)
	}
	command, goalCommand := llmtypes.NativeGoalCommandForSession(ctx, a.sessionID)
	nonModelGoalControl := goalCommand && model.Provider == "codex-cli" && (command.Action == "show" || command.Action == "pause" || command.Action == "clear")
	if exhausted && nonModelGoalControl {
		logger.Info(fmt.Sprintf("[AGENT_GOAL] session=%s action=started reason=nonmodel_control_preserves_quota", a.sessionID))
	}
	if exhausted && !nonModelGoalControl {
		if !resetAt.IsZero() && !resetAt.After(time.Now()) {
			delete(a.quotaExhaustedModels, key)
			forgetSharedModelQuotaExhaustion(key)
		} else {
			return nil, usage, &llmerrors.Error{
				Kind:     llmerrors.KindQuotaExhausted,
				Provider: model.Provider,
				Model:    model.ModelID,
				RetryAt:  resetAt,
				Err:      fmt.Errorf("selected model is quota-exhausted"),
			}
		}
	}

	generationStartTime := time.Now()

	// Emit start event
	a.emitTypedEvent(ctx, &events.LLMGenerationWithRetryEvent{
		BaseEventData: events.BaseEventData{Timestamp: generationStartTime},
		Turn:          turn,
		MaxRetries:    maxRetries,
		PrimaryModel:  llmConfig.Primary.ModelID,
		CurrentLLM:    llmConfig.Primary.ModelID,
		Provider:      llmConfig.Primary.Provider,
		Operation:     "llm_generation_with_retry",
		Status:        "started",
	})

	// Try executing with retries (throttling/transient error handling)
	for attempt := 0; attempt < maxRetries; attempt++ {
		if ctx.Err() != nil {
			return nil, usage, a.handleContextCancellation(ctx, turn, generationStartTime)
		}

		// Create a copy of options for this attempt
		currentOpts := make([]llmtypes.CallOption, len(opts))
		copy(currentOpts, opts)

		sm := a.startStreaming(ctx, attempt, turn, &currentOpts)

		// Execute LLM
		resp, err := a.executeLLM(ctx, model, messages, currentOpts)

		a.finishStreaming(ctx, sm, resp)

		// After finishStreaming, processChunks has fully drained — sm.CLIToolCalls is
		// complete. Attach the collected tool calls to the response so AskWithHistory
		// can reconstruct a proper conversation history for CLI providers (Claude Code,
		// Claude Code, Codex CLI) where tools run inside the subprocess.
		if sm != nil && len(sm.CLIToolCalls) > 0 && resp != nil && len(resp.Choices) > 0 {
			choice := resp.Choices[0]
			if choice.GenerationInfo == nil {
				choice.GenerationInfo = &llmtypes.GenerationInfo{}
			}
			if choice.GenerationInfo.Additional == nil {
				choice.GenerationInfo.Additional = make(map[string]interface{})
			}
			if histJSON, err2 := json.Marshal(sm.CLIToolCalls); err2 == nil {
				choice.GenerationInfo.Additional["cli_tool_call_chunks"] = string(histJSON)
			}
		}

		if err == nil {
			usage = extractUsageMetricsWithMessages(resp, messages)

			logger.Info(fmt.Sprintf("✅ LLM succeeded: %s/%s", model.Provider, model.ModelID))

			return resp, usage, nil
		}

		// Handle context cancellation specifically
		if isContextCanceledError(err) || ctx.Err() != nil {
			return nil, usage, a.handleContextCancellation(ctx, turn, generationStartTime)
		}

		if llmerrors.KindOf(err) == llmerrors.KindUserInputRequired {
			return nil, usage, err
		}
		// A native goal control/activation may already have changed its thread.
		// Retrying it could clear that goal or repeat work; native goals own
		// their continuation and never fall back to another program here.
		if _, goalCommand := llmtypes.NativeGoalCommandForSession(ctx, a.sessionID); goalCommand && llmerrors.KindOf(err) != llmerrors.KindQuotaExhausted {
			logger.Info(fmt.Sprintf("[AGENT_GOAL] session=%s action=failed status=unknown reason=native_goal_operation_failed", a.sessionID))
			return nil, usage, err
		}
		errorType := classifyLLMError(err)
		lastErr = err
		if isUnconfirmedAGYTurnTimeout(err) || isAGYTurnEndedByExecutor(err) {
			break
		}

		// Special handling for retrying SAME model (throttling/zero candidates/internal errors)
		// For zero_candidates errors: limit to 3 retries before returning the error
		// For throttling/internal errors: use full 5 retries
		shouldRetrySameModel := false
		if errorType == "quota_exhausted_error" {
			// Permanent quota exhaustion (daily/monthly) — retrying same model is pointless.
			// Remember this account/model so future turns skip it immediately, in
			// this turn and in every later turn regardless
			// of which *Agent instance handles it.
			if a.quotaExhaustedModels == nil {
				a.quotaExhaustedModels = make(map[quotaModelKey]time.Time)
			}
			// Zero when the provider stated no reliable reset: still skipped,
			// but never retried on a guessed schedule.
			resetAt := llmerrors.RetryAtOrZero(err)
			a.quotaExhaustedModels[key] = resetAt
			markModelQuotaExhaustedShared(key, resetAt)
			if resetAt.IsZero() {
				logger.Info(fmt.Sprintf("🚫 [QUOTA_EXHAUSTED] Usage window spent for %s (no reset time stated) — skipping it on later turns", key))
			} else {
				logger.Info(fmt.Sprintf("🚫 [QUOTA_EXHAUSTED] Usage window spent for %s until %s — skipping it until then", key, resetAt.Format(time.RFC3339)))
			}
			break
		} else if errorType == "auth_error" {
			// Authentication errors require corrected credentials.
			logger.Warn(fmt.Sprintf("🔑 [AUTH] Authentication/permission failed for %s/%s; skipping same-model retry, returning the error", model.Provider, model.ModelID))
			break
		} else if errorType == "model_not_found_error" {
			// Unknown/unavailable model ID — a permanent config error. Memoize it
			// (like quota) so future turns skip it.
			if a.quotaExhaustedModels == nil {
				a.quotaExhaustedModels = make(map[quotaModelKey]time.Time)
			}
			// A missing model is a config error, not a window that reopens:
			// the zero reset keeps it skipped permanently rather than
			// retried on a schedule. Shared so a broken model_id learned by
			// one turn does not get rediscovered the slow way by the next.
			a.quotaExhaustedModels[key] = time.Time{}
			markModelQuotaExhaustedShared(key, time.Time{})
			logger.Warn(fmt.Sprintf("🚫 [MODEL_NOT_FOUND] Model %s is unavailable; marked to skip on future turns, returning the error", key))
			break
		} else if errorType == "zero_candidates_error" {
			// Zero candidates: retry up to 3 times (attempts 0, 1, 2 = 3 retries total)
			if attempt < maxRetriesZeroCandidates-1 {
				shouldRetrySameModel = true
			} else {
				logger.Info(fmt.Sprintf("🔄 [ZERO_CANDIDATES] Reached max retries (%d) for zero_candidates error, returning the error", maxRetriesZeroCandidates))
				// Break immediately - don't continue the loop
				logger.Warn(fmt.Sprintf("❌ Model failed after %d retries: %s/%s - %v", maxRetriesZeroCandidates, model.Provider, model.ModelID, err))
				break // Retry budget exhausted.
			}
		} else if errorType == "throttling_error" || errorType == "internal_error" || errorType == "connection_error" || errorType == "stream_error" {
			// Throttling/internal/connection/stream errors: retry up to 5 times (transient)
			if attempt < maxRetries-1 {
				shouldRetrySameModel = true
			}
		} else if errorType == "empty_content_error" {
			// Empty-content errors include both transient cases (coding-agent CLIs
			// status=error mid-stream with no detail, e.g. backend 5xx) and
			// non-transient ones (context too large, safety filter). Retry
			// up to 2 times — enough to ride out a transient hiccup without
			// burning extra cost when the failure is structural.
			if attempt < maxRetriesEmptyContent-1 {
				shouldRetrySameModel = true
			}
		}

		if shouldRetrySameModel && attempt < maxRetries-1 {
			// Use error-type-specific retry caps.
			retryLimit := maxRetries
			if errorType == "zero_candidates_error" {
				retryLimit = min(maxRetries, maxRetriesZeroCandidates)
			} else if errorType == "empty_content_error" {
				retryLimit = min(maxRetries, maxRetriesEmptyContent)
			}
			shouldRetry, _, retryErr := retryOriginalModelWithReason(a, ctx, errorType, retryReasonText(errorType, err), attempt, retryLimit, baseDelay, maxDelay, turn, logger, usage)
			if retryErr != nil {
				return nil, usage, retryErr
			}
			if shouldRetry {
				continue // Retry same model
			}
		}

		// If not a retryable error on same model, or max retries reached:
		// Stop after the selected model exhausts its retries.
		logger.Warn(fmt.Sprintf("❌ Model failed: %s/%s - %v", model.Provider, model.ModelID, err))

		break // Retry budget exhausted.
	}
	// Preserve the selected provider error for callers.
	return nil, usage, fmt.Errorf("selected LLM failed: %w", lastErr)
}

// handleContextCancellation emits cancellation event and returns the error
func (a *Agent) handleContextCancellation(ctx context.Context, turn int, startTime time.Time) error {
	err := ctx.Err()
	if err == nil {
		err = context.Canceled
	}
	a.emitTypedEvent(ctx, events.NewContextCancelledEvent(turn, err.Error(), time.Since(startTime)))
	return err
}

// contextCompactionEvent converts the provider chunk into the agent event.
func contextCompactionEvent(c *llmtypes.ContextCompaction, fallbackProvider string) *events.ContextCompactionEvent {
	formatTime := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339Nano)
	}
	provider := c.Provider
	if provider == "" {
		provider = fallbackProvider
	}
	return &events.ContextCompactionEvent{
		BaseEventData: events.BaseEventData{Timestamp: time.Now()},
		Provider:      provider,
		Phase:         string(c.Phase),
		CompactionID:  c.ID,
		Trigger:       c.Trigger,
		Outcome:       c.Outcome,
		TokensBefore:  c.TokensBefore,
		TokensAfter:   c.TokensAfter,
		StartedAt:     formatTime(c.StartedAt),
		EndedAt:       formatTime(c.EndedAt),
		DurationMs:    c.DurationMs,
	}
}
