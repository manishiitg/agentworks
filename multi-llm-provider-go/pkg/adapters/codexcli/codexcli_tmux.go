package codexcli

import (
	"context"
	"errors"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/internal/tmuxcontrol"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/launchscrub"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/tmuxexec"
	"github.com/manishiitg/multi-llm-provider-go/pkg/codingready"
	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxinput"
	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxstartup"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (c *CodexCLIAdapter) generateContentInteractive(ctx context.Context, messages []llmtypes.MessageContent, opts *llmtypes.CallOptions) (resp *llmtypes.ContentResponse, err error) {
	var tmuxSessionName string
	defer func() {
		if isCodexTmuxSessionLostError(err) {
			err = llmtypes.WrapCodingAgentTmuxSessionLostError(err, "codex-cli", tmuxSessionName, "tmux session lost")
		}
	}()

	if _, err := exec.LookPath("tmux"); err != nil {
		return nil, fmt.Errorf("tmux not found in PATH; codex-cli interactive mode requires tmux")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		return nil, fmt.Errorf("codex cli not found in PATH. Please install it first (npm install -g @openai/codex)")
	}

	ownerSessionID := codexInteractiveSessionIDFromOptions(opts)
	if ownerSessionID == "" {
		return nil, fmt.Errorf("codex-cli interactive mode requires an owner session ID")
	}
	persistent := codexPersistentInteractiveFromOptions(opts)
	c.logger.Debugf("codex interactive launch enter owner=%s persistent=%v workingDir=%q", ownerSessionID, persistent, codexWorkingDirFromOptions(opts))

	turnStart := time.Now().UTC()

	// Inspector emitter — no-op when opts.InspectorSink is nil. Lifecycle
	// events (tmux acquiring/ready/prompt/captured) are what the unified
	// debug panel renders for tmux providers, since they have no
	// per-token stream to tap into.
	inspector := llmtypes.NewInspectorEmitter(opts.InspectorSink, "codex-cli", c.modelID)
	inspector.EmitRequest(map[string]interface{}{
		"transport":     "tmux",
		"message_count": len(messages),
		"persistent":    persistent,
	})

	callCtx, cancel := codexInteractiveCallContext(ctx)
	defer cancel()

	// On user-initiated cancellation, tear down the persistent tmux
	// session so the live pane closes alongside the workflow step.
	// Without this, "cancel step" leaves the codex process running
	// in tmux indefinitely and the UI keeps the terminal entry
	// listed as active. DeadlineExceeded is treated separately —
	// timeouts shouldn't kill a session a user might still want.
	defer func() {
		if ctx.Err() != context.Canceled {
			return
		}
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		closeCodexPersistentSession(ownerSessionID, "workflow context canceled", c.logger)
		_ = killCtx
	}()

	systemPrompt, conversationMessages := splitCodexSystemPrompt(messages)
	systemPrompt += llmtypes.CLIPrivateSkillInstructions(opts, codexSkillsSubdir)
	historicalAssistantTexts := codexAssistantHistory(conversationMessages)
	prompt := buildCodexInteractivePrompt(conversationMessages)
	initialPromptAtLaunch := codexCanSubmitInitialPromptAtLaunch(opts, prompt)

	acquireStart := time.Now()
	inspector.EmitEvent("tmux_session_acquiring", map[string]interface{}{
		"owner_session_id": ownerSessionID,
	})
	bindSince := time.Now()
	session, created, err := c.acquireCodexInteractiveSession(callCtx, ownerSessionID, opts, systemPrompt, prompt)
	if err != nil {
		inspector.EmitError(err, map[string]interface{}{
			"phase":      "tmux_session_acquire",
			"elapsed_ms": time.Since(acquireStart).Milliseconds(),
		})
		return nil, err
	}
	c.logger.Debugf("codex interactive startup timing owner=%s stage=acquire created=%v elapsed=%s total=%s", ownerSessionID, created, time.Since(acquireStart).Round(time.Millisecond), time.Since(turnStart).Round(time.Millisecond))
	tmuxSessionName = session.tmuxSessionName
	// Keep one content-free lifecycle record at ordinary log level. A missing
	// rollout must be distinguishable from a native completion without enabling
	// verbose request logging (PLAT-856).
	completionSource, finalExtractionSource := "", ""
	diagnosticRollout, diagnosticThread := codexRolloutIdentity(session)
	diagnosticStartedAt := turnStart
	diagnosticAnswerRollout, diagnosticNativeTurn, diagnosticCompletedAt := "", "", ""
	diagnosticForcedComplete := false
	diagnosticWorkingDir, diagnosticAccountRoot := session.workingDir, session.accountRoot
	defer func() {
		outcome := "completed"
		if llmtypes.CodingProviderLaunchOnlyFromOptions(opts) {
			outcome = "launch_only"
		}
		if err != nil {
			outcome = "failed"
		}
		loggedCompletionSource := completionSource
		if diagnosticForcedComplete {
			loggedCompletionSource = "forced_complete"
		}
		contextError := ""
		if callCtx.Err() != nil {
			contextError = callCtx.Err().Error()
		}
		c.logger.Infof("[COMPLETION_TRACE] stage=codex_turn_returned owner=%q tmux=%q thread=%q rollout=%q working_dir=%q account_root=%q turn_started_at=%s completion_source=%q extraction_source=%q answer_rollout=%q native_turn=%q native_completed_at=%q forced_complete=%v outcome=%s error_type=%T context_error=%q", ownerSessionID, tmuxSessionName, diagnosticThread, diagnosticRollout, diagnosticWorkingDir, diagnosticAccountRoot, diagnosticStartedAt.UTC().Format(time.RFC3339Nano), loggedCompletionSource, finalExtractionSource, diagnosticAnswerRollout, diagnosticNativeTurn, diagnosticCompletedAt, diagnosticForcedComplete, outcome, err, contextError)
	}()
	tmuxstartup.Publish(callCtx, opts.StreamChan, "codex-cli", c.modelID, session.tmuxSessionName, session.workingDir, map[string]interface{}{
		"codex_interactive_session": session.tmuxSessionName,
	})
	inspector.EmitEvent("tmux_session_ready", map[string]interface{}{
		"tmux_session_name": session.tmuxSessionName,
		"elapsed_ms":        time.Since(acquireStart).Milliseconds(),
	})
	releaseSession := true
	defer func() {
		if releaseSession && session != nil {
			if persistent {
				releaseCodexInteractiveSession(session, c.logger)
			} else {
				releaseCodexBoundedInteractiveSession(session, c.logger)
			}
		}
	}()

	launchOnly := llmtypes.CodingProviderLaunchOnlyFromOptions(opts)
	initialPromptAtLaunch = created && initialPromptAtLaunch
	if created {
		if initialPromptAtLaunch {
			c.logger.Debugf("codex interactive acquired new session owner=%s tmux=%s with launch prompt", ownerSessionID, session.tmuxSessionName)
		} else {
			c.logger.Debugf("codex interactive acquired new session owner=%s tmux=%s; waiting for startup prompt", ownerSessionID, session.tmuxSessionName)
			startupWaitStart := time.Now()
			waitForStartup := waitForCodexPrompt
			if !launchOnly {
				// Codex exposes its composer before MCP startup has gone quiet. The
				// composer already accepts input at that point, so do not hold the
				// first user message behind the slower full-idle readiness check.
				waitForStartup = waitForCodexInputPrompt
			}
			if err := waitForStartup(callCtx, session.tmuxSessionName, opts.StreamChan, codexInteractiveStreamTmuxScreenEnabled(opts)); err != nil {
				markCodexInteractiveSessionFailedLocked(session, err, c.logger)
				releaseSession = false
				failedSession := session
				session.mu.Unlock()
				session = nil
				cleanupFailedCodexInteractiveSession(failedSession)
				return nil, err
			}
			c.logger.Debugf("codex interactive startup timing owner=%s stage=prompt_ready tmux=%s launch_only=%v elapsed=%s total=%s", ownerSessionID, session.tmuxSessionName, launchOnly, time.Since(startupWaitStart).Round(time.Millisecond), time.Since(turnStart).Round(time.Millisecond))
		}
	} else {
		c.logger.Debugf("codex interactive reused initialized session owner=%s tmux=%s", ownerSessionID, session.tmuxSessionName)
	}

	if launchOnly {
		launchFinalizeStart := time.Now()
		tmuxinput.MarkReady(session.tmuxSessionName)
		markReadyElapsed := time.Since(launchFinalizeStart)
		var lastSnapshot string
		snapshotStart := time.Now()
		// A launch-only call has not completed a model turn yet, so there is no
		// current token usage to publish. Avoid walking every historical Codex
		// rollout merely to render the initial terminal pane; on a large session
		// archive that scan added multiple seconds to an otherwise millisecond
		// launch finalization.
		streamCodexTerminalPaneSnapshot(callCtx, session.tmuxSessionName, opts.StreamChan, &lastSnapshot)
		snapshotElapsed := time.Since(snapshotStart)
		handleStart := time.Now()
		additional := map[string]interface{}{
			"provider":                     "codex-cli",
			"codex_mode":                   "interactive",
			"codex_interactive_session":    session.tmuxSessionName,
			"codex_persistent_interactive": persistent,
			"codex_uses_exec_json":         false,
		}
		gi := &llmtypes.GenerationInfo{Additional: additional}
		handleModel := c.modelID
		if modelToUse := resolveCodexCLIModelID(c.modelID); modelToUse != "" {
			handleModel = modelToUse
		}
		llmtypes.AttachCodingProviderSessionHandle(gi, llmtypes.CodingProviderSessionHandle{
			Provider:        "codex-cli",
			Transport:       llmtypes.CodingProviderTransportTmux,
			NativeSessionID: codexResumeSessionIDFromOptions(opts),
			TmuxSession:     session.tmuxSessionName,
			WorkingDir:      session.workingDir,
			RuntimeDir:      opts.CLIRuntimeDir,
			Model:           handleModel,
			Status:          llmtypes.CodingProviderSessionStatusIdle,
		})
		c.logger.Debugf("codex interactive startup timing owner=%s stage=launch_finalize tmux=%s mark_ready=%s snapshot=%s handle=%s total=%s", ownerSessionID, session.tmuxSessionName, markReadyElapsed.Round(time.Millisecond), snapshotElapsed.Round(time.Millisecond), time.Since(handleStart).Round(time.Millisecond), time.Since(turnStart).Round(time.Millisecond))
		return &llmtypes.ContentResponse{
			Choices: []*llmtypes.ContentChoice{{
				Content:        "",
				GenerationInfo: gi,
			}},
		}, nil
	}

	// Cold session: the composer being ready does not mean the MCP bridge has
	// finished connecting. Hold the first prompt until the bridge reports
	// tools/list answered (marker file). No-op on reused sessions / when
	// unconfigured; bounded, best-effort. See pkg/codingready.
	if created && opts.Metadata != nil {
		if rf := codingready.MCPReadyFileFromMetadata(opts.Metadata.Custom); rf != "" {
			codingready.WaitForMCPReadyFile(callCtx, rf, codingready.MCPReadyWait())
		}
	}

	baseline := ""
	if !initialPromptAtLaunch {
		baseline, _ = captureCodexPane(callCtx, session.tmuxSessionName)
	}
	c.logger.Infof("Executing Codex CLI interactive tmux session: %s", session.tmuxSessionName)
	setCodexRolloutBindPrompt(session, prompt, bindSince)
	promptSentAt := time.Now()
	if initialPromptAtLaunch {
		// The prompt was supplied when acquiring the process, so Codex may
		// already have recorded task_started. Use the pre-launch boundary for
		// completion and transcript streaming too; a post-launch timestamp can
		// discard that event and let an idle loading pane end the turn early.
		promptSentAt = bindSince
		var err error
		baseline, err = waitForCodexInitialPromptAccepted(callCtx, session.tmuxSessionName, prompt, opts.StreamChan, codexInteractiveStreamTmuxScreenEnabled(opts), codexTurnStartOracle(session, turnStart))
		if err != nil {
			err = codexConfirmInitialSubmitAfterPaneError(ctx, session, prompt, turnStart, err)
		}
		if err != nil {
			inspector.EmitError(err, map[string]interface{}{"phase": "tmux_initial_prompt_start"})
			markCodexInteractiveSessionFailedLocked(session, err, c.logger)
			releaseSession = false
			failedSession := session
			session.mu.Unlock()
			session = nil
			cleanupFailedCodexInteractiveSession(failedSession)
			return nil, err
		}
		c.logger.Debugf("codex interactive launch prompt accepted owner=%s tmux=%s elapsed=%s", ownerSessionID, session.tmuxSessionName, time.Since(promptSentAt).Round(time.Millisecond))
	} else if err := codexConfirmInitialSubmitAfterPaneError(ctx, session, prompt, promptSentAt,
		sendCodexPromptToTmuxConfirmedBy(callCtx, session.tmuxSessionName, prompt, codexTurnStartOracle(session, promptSentAt))); err != nil {
		inspector.EmitError(err, map[string]interface{}{"phase": "tmux_prompt_send"})
		markCodexInteractiveSessionFailedLocked(session, err, c.logger)
		releaseSession = false
		failedSession := session
		session.mu.Unlock()
		session = nil
		cleanupFailedCodexInteractiveSession(failedSession)
		return nil, err
	}
	tmuxinput.MarkReady(session.tmuxSessionName)
	llmtypes.SignalPromptAccepted(opts.StreamChan)
	inspector.EmitEvent("tmux_prompt_sent", map[string]interface{}{
		"prompt_length":       len(prompt),
		"submitted_at_launch": initialPromptAtLaunch,
	})

	initialRolloutPath, initialThreadID := codexRolloutIdentity(session)
	diagnosticStartedAt = promptSentAt
	c.logger.Debugf("[COMPLETION_TRACE] stage=codex_wait_started owner=%q tmux=%q thread=%q rollout=%q turn_started_at=%s", ownerSessionID, session.tmuxSessionName, initialThreadID, initialRolloutPath, promptSentAt.UTC().Format(time.RFC3339Nano))
	completionDiagnostics := &codexCompletionDiagnosticHooks{
		completedBy: func(source string) { completionSource = source },
		rolloutSelected: func(path, threadID string) {
			diagnosticRollout, diagnosticThread = path, threadID
			c.logger.Debugf("[COMPLETION_TRACE] stage=codex_rollout_selected owner=%q tmux=%q thread=%q rollout=%q", ownerSessionID, session.tmuxSessionName, threadID, path)
		},
		taskComplete: func(path, threadID, turnID string, completedAt time.Time, offset int64) {
			diagnosticNativeTurn, diagnosticCompletedAt = turnID, completedAt.UTC().Format(time.RFC3339Nano)
			c.logger.Debugf("[COMPLETION_TRACE] stage=codex_task_complete_observed owner=%q tmux=%q thread=%q native_turn=%q rollout=%q completed_at=%s offset=%d", ownerSessionID, session.tmuxSessionName, threadID, turnID, path, completedAt.UTC().Format(time.RFC3339Nano), offset)
		},
	}
	closeStream := func(reason string) {
		if opts.StreamChan == nil {
			return
		}
		c.logger.Debugf("[COMPLETION_TRACE] stage=codex_stream_closing owner=%q tmux=%q reason=%s", ownerSessionID, session.tmuxSessionName, reason)
		close(opts.StreamChan)
		c.logger.Debugf("[COMPLETION_TRACE] stage=codex_stream_closed owner=%q tmux=%q reason=%s", ownerSessionID, session.tmuxSessionName, reason)
	}
	captured, err := waitForCodexInteractiveResponse(callCtx, session.tmuxSessionName, baseline, opts.StreamChan, promptSentAt, session.workingDir, codexInteractiveStreamTranscriptEnabled(opts), codexInteractiveStreamTmuxScreenEnabled(opts), codexRolloutResolverForSession(session), completionDiagnostics)
	forcedComplete := errors.Is(err, tmuxcontrol.ErrForceComplete)
	diagnosticForcedComplete = forcedComplete
	if err != nil && !forcedComplete {
		var loginRequired *CodexLoginRequiredError
		if errors.As(err, &loginRequired) {
			c.logger.Errorf("[TURN] stalled cause=login_required provider=codex-cli owner=%s tmux=%s elapsed=%s", ownerSessionID, session.tmuxSessionName, time.Since(promptSentAt).Round(time.Second))
		}
		err = tmuxexec.WithFailureDiagnostic(err, "codex-cli", session.tmuxSessionName)
		c.logger.Debugf("[COMPLETION_TRACE] stage=codex_wait_returned owner=%q tmux=%q outcome=error elapsed=%s error=%q", ownerSessionID, session.tmuxSessionName, time.Since(promptSentAt).Round(time.Millisecond), err.Error())
		inspector.EmitError(err, map[string]interface{}{
			"phase":      "tmux_wait_response",
			"elapsed_ms": time.Since(promptSentAt).Milliseconds(),
		})
		if ctx.Err() != nil {
			interruptCodexInteractiveSession(session.tmuxSessionName, c.logger)
		}
		closeStream("wait_error")
		return nil, err
	}
	c.logger.Debugf("[COMPLETION_TRACE] stage=codex_wait_returned owner=%q tmux=%q outcome=completed elapsed=%s", ownerSessionID, session.tmuxSessionName, time.Since(promptSentAt).Round(time.Millisecond))
	if err := codexPolicyInvalidPromptError(captured); err != nil {
		inspector.EmitError(err, map[string]interface{}{
			"phase":      "tmux_wait_response",
			"elapsed_ms": time.Since(promptSentAt).Milliseconds(),
		})
		closeStream("captured_policy_error")
		return nil, err
	}
	inspector.EmitEvent("tmux_response_captured", map[string]interface{}{
		"response_chars": len(captured),
		"elapsed_ms":     time.Since(promptSentAt).Milliseconds(),
	})
	c.logger.Debugf("codex interactive response captured owner=%s tmux=%s elapsed=%s", ownerSessionID, session.tmuxSessionName, time.Since(promptSentAt).Round(time.Millisecond))

	content := parseCodexInteractiveResponse(captured, baseline, prompt, historicalAssistantTexts)
	if forcedComplete && strings.TrimSpace(content) == "" {
		content = forcedCodexInteractiveResponse(captured, baseline, prompt, historicalAssistantTexts)
	}
	// The rollout is authoritative for the final answer. The tmux pane is still
	// used for visibility and compatibility, but it can retain or reflow MCP
	// calls beside the final response. Persisting that pane-derived mixture made
	// plan-step results and AUTO-NOTIFICATION messages contain full tool calls.
	// Codex's task_complete event carries exactly the committed final message, so
	// prefer it whenever the current-turn rollout is available.
	// PLAT-106: resolve THIS session's rollout. Selecting by working directory
	// alone returns the newest transcript in that directory, which is the other
	// conversation's whenever a workflow's Chat and Schedule run concurrently.
	// session.mu is held for this entire function (released by the deferred
	// releaseCodexInteractiveSession), so the *Locked variant is required here.
	sessionRolloutPath := resolveCodexRolloutPathLocked(session, promptSentAt.UTC())
	// The answer must come from the transcript that recorded this turn's prompt. A chat whose rollout was
	// picked by directory and recency could otherwise return another chat's final message (a new Code tab
	// returned its opener's answer, 2026-10-17); the pane text is the fallback.
	if sessionRolloutPath != "" {
		if strict, loose := codexRolloutRecordsBindPrompt(sessionRolloutPath, prompt, promptSentAt.UTC()); !strict && !loose {
			c.logger.Infof("codex interactive rollout %s does not record this turn's prompt; not used for the final answer owner=%s", filepath.Base(sessionRolloutPath), ownerSessionID)
			sessionRolloutPath = ""
		}
	}
	diagnosticAnswerRollout = sessionRolloutPath
	finalExtractionSource = "tmux_pane"
	if transcriptFinal, source := readCodexRolloutFinalAssistantText(sessionRolloutPath, promptSentAt.UTC()); strings.TrimSpace(transcriptFinal) != "" {
		content = transcriptFinal
		finalExtractionSource = source
	}
	// A turn Codex itself failed (unsupported model for the account, an API
	// 4xx) completes with no final message and the reason on task_complete.
	// That is an error for the caller, not an empty answer: an empty reply
	// with a session handle attached reads as a launch-only success upstream.
	// The rollout's own record decides, not the pane text: when the turn failed (for example
	// "Selected model is at capacity"), the pane's last lines are the error banner, which must
	// not be taken for an answer, and the chat must get a real error event.
	if turnErr, turnInfo := readCodexRolloutTurnFailure(sessionRolloutPath, promptSentAt.UTC()); turnErr != "" {
		err := &CodexTurnError{Message: turnErr, Info: turnInfo}
		inspector.EmitError(err, map[string]interface{}{
			"phase":      "rollout_turn_error",
			"elapsed_ms": time.Since(promptSentAt).Milliseconds(),
			"capacity":   err.Capacity(),
			"codex_info": turnInfo,
		})
		closeStream("rollout_turn_error")
		return nil, err
	}
	if err := codexPolicyInvalidPromptTextError(content); err != nil {
		inspector.EmitError(err, map[string]interface{}{
			"phase":      "tmux_parse_response",
			"elapsed_ms": time.Since(promptSentAt).Milliseconds(),
		})
		closeStream("final_policy_error")
		return nil, err
	}
	// A bounded session is about to be torn down, so retain the grace window
	// that captures late terminal output before destruction. Persistent chat
	// sessions remain live and have their own terminal stream; waiting for two
	// unchanged two-second polls here only delays the structured final answer.
	if !persistent {
		llmtypes.RunTrailingPaneCapture(callCtx, opts.StreamChan,
			func(ctx context.Context) (string, error) {
				snap, err := captureCodexPane(ctx, session.tmuxSessionName)
				if err != nil {
					return "", err
				}
				return strings.TrimRight(stripCodexANSI(snap), "\n"), nil
			},
			map[string]interface{}{
				"tmux_session":              session.tmuxSessionName,
				"codex_interactive_session": session.tmuxSessionName,
			},
		)
	} else {
		c.logger.Debugf("codex interactive trailing capture skipped owner=%s tmux=%s reason=persistent_session", ownerSessionID, session.tmuxSessionName)
	}
	closeStream("completed")

	additional := map[string]interface{}{
		"provider":                      "codex-cli",
		"codex_mode":                    "interactive",
		"codex_interactive_session":     session.tmuxSessionName,
		"codex_persistent_interactive":  persistent,
		"codex_uses_exec_json":          false,
		"codex_final_extraction_source": finalExtractionSource,
		"codex_completion_source":       completionSource,
		"context_window_usage_known":    false,
	}
	if !persistent {
		// terminal_retention_seconds intentionally not set — see cursor.
		additional["codex_interactive_retention_seconds"] = int(codexInteractiveRetention().Seconds())
	}

	// Best-effort usage from codex's local rollout JSONL — tmux mode
	// has no stdout JSON, but ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl
	// records token_count event_msgs with per-turn token snapshots.
	gi := &llmtypes.GenerationInfo{Additional: additional}
	handleModel := c.modelID
	// Read usage from the SAME rollout the final answer came from. Re-scanning by
	// working directory here would reintroduce the PLAT-106 ambiguity — and worse,
	// the thread ID it returns is what pins this session's identity, so a wrong
	// pick would bind the session permanently to another conversation.
	var usage *llmtypes.GenerationInfo
	var effectiveModel, threadID string
	if sessionRolloutPath != "" {
		usage, effectiveModel, threadID, _ = readCodexTranscriptUsageFile(sessionRolloutPath, turnStart)
	}
	if threadID != "" {
		additional["codex_thread_id"] = threadID
	}
	if effectiveModel != "" {
		handleModel = effectiveModel
		additional["codex_effective_model"] = effectiveModel
	}
	if usage != nil {
		gi.PromptTokens = usage.PromptTokens
		gi.CompletionTokens = usage.CompletionTokens
		gi.TotalTokens = usage.TotalTokens
		gi.CachedContentTokens = usage.CachedContentTokens
		gi.ReasoningTokens = usage.ReasoningTokens
		// Forward raw cache-token keys from the parser into the
		// adapter's local Additional map so they survive to the cost
		// ledger's extractCacheTokens (keyed off raw Anthropic-style
		// names rather than the typed CachedContentTokens field).
		for k, v := range usage.Additional {
			additional[k] = v
		}
	}
	if effectiveModel != "" {
		if meta, _ := c.GetModelMetadata(effectiveModel); meta != nil {
			if cost := llmtypes.ComputeUSDCostFromMetadata(meta, gi); cost > 0 {
				additional["cost_usd_estimated"] = cost
				additional["cost_model_id"] = effectiveModel
			}
		}
	}
	llmtypes.AttachCodingProviderSessionHandle(gi, llmtypes.CodingProviderSessionHandle{
		Provider:        "codex-cli",
		Transport:       llmtypes.CodingProviderTransportTmux,
		NativeSessionID: threadID,
		TmuxSession:     session.tmuxSessionName,
		WorkingDir:      session.workingDir,
		RuntimeDir:      opts.CLIRuntimeDir,
		Model:           handleModel,
		Status:          llmtypes.CodingProviderSessionStatusIdle,
	})

	// Reconstruct the CLI's internal tool-use trail (assistant text,
	// function/custom tool calls + outputs) from the same rollout
	// JSONL we read tokens from, so workflow conversation logs can
	// splice the in-CLI turn-by-turn record into their persisted
	// history. Best-effort: empty when the rollout is missing or has
	// not been flushed yet.
	if sidecarMsgs := readCodexTranscriptMessages(turnStart, session.workingDir, session.accountRoot); len(sidecarMsgs) > 0 {
		llmtypes.AttachCodingProviderIntermediateMessages(gi, llmtypes.CodingProviderIntermediateMessages{
			Provider:  "codex-cli",
			Transport: llmtypes.CodingProviderTransportTmux,
			Messages:  sidecarMsgs,
		})
	}

	// Inspector: emit the completion envelope. Token counts are
	// best-effort from the rollout JSONL; cost may be unavailable on
	// runs that didn't write a rollout file yet.
	if inspector.Enabled() {
		completionMeta := map[string]interface{}{
			"stop_reason":   "tmux_response_captured",
			"duration_ms":   time.Since(turnStart).Milliseconds(),
			"content_chars": len(content),
		}
		if gi.PromptTokens != nil {
			completionMeta["prompt_tokens"] = *gi.PromptTokens
		}
		if gi.CompletionTokens != nil {
			completionMeta["completion_tokens"] = *gi.CompletionTokens
		}
		if cost, ok := additional["cost_usd_estimated"].(float64); ok {
			completionMeta["cost_usd_estimated"] = cost
		}
		if cm, ok := additional["cost_model_id"].(string); ok && cm != "" {
			completionMeta["cost_model_id"] = cm
		}
		inspector.EmitCompletion(completionMeta)
	}

	return &llmtypes.ContentResponse{
		Choices: []*llmtypes.ContentChoice{
			{
				Content:        content,
				GenerationInfo: gi,
			},
		},
	}, nil
}

type relaunchedForScopeKey struct{}

// relaunchedForScope bounds the scope-change relaunch to one attempt per call.

func relaunchedForScope(ctx context.Context) bool {
	done, _ := ctx.Value(relaunchedForScopeKey{}).(bool)
	return done
}

func withRelaunchedForScope(ctx context.Context) context.Context {
	return context.WithValue(ctx, relaunchedForScopeKey{}, true)
}

func (c *CodexCLIAdapter) acquireCodexInteractiveSession(ctx context.Context, ownerSessionID string, opts *llmtypes.CallOptions, systemPrompt, initialPrompt string) (*codexInteractiveSession, bool, error) {
	acquireStart := time.Now()
	c.logger.Debugf("codex interactive acquire enter owner=%s", ownerSessionID)
	now := time.Now()
	workingDir := codexWorkingDirFromOptions(opts)
	securityFingerprint := clisandbox.Fingerprint(opts.CLISecurity) + ":" + llmtypes.CodingAgentScopeFingerprint(opts) + ":" + workingDir + ":" + c.modelID
	session, created, ok := codexPersistentRegistry.GetOrCreate(ownerSessionID, func() *codexInteractiveSession {
		session := &codexInteractiveSession{
			ownerSessionID:         ownerSessionID,
			tmuxSessionName:        newCodexTmuxSessionName(),
			workingDir:             workingDir,
			accountRoot:            llmtypes.CLIHomeEnvironment(opts)["CODEX_HOME"],
			cliSecurityFingerprint: securityFingerprint,
			createdAt:              now,
			// A resumed conversation already has its Codex thread; pinning it
			// now (instead of after the first turn binds a rollout) gives the
			// first submission after a relaunch an authoritative rollout to
			// confirm against, rather than pane guesses over a TUI that may
			// still be replaying history.
			threadID: codexResumeSessionIDFromOptions(opts),
		}
		session.mu.Lock()
		return session
	})
	if !ok {
		return nil, false, fmt.Errorf("codex-cli interactive mode requires an owner session ID")
	}
	if !created {
		// Release the GLOBAL registry lock BEFORE taking the per-session lock.
		// session.mu is held for the duration of a whole turn; acquiring it
		// while still holding the global map lock stalls every other codex
		// acquire behind a busy session (a lock-held-across-blocking-call
		// deadlock — the exact hang that froze background step orchestrators).
		// The two locks have different lifetimes: the registry lock guards the
		// map only; grab the pointer under it, release it, then take the
		// per-session lock. Holding a pointer keeps the session valid even if a
		// concurrent teardown removes it from the map (initErr guards that).
		sessionLockStart := time.Now()
		session.mu.Lock()
		if session.cliSecurityFingerprint != securityFingerprint {
			// The retained process was launched under another sandbox policy or
			// credential scope and must not serve this turn. Holding session.mu
			// means no turn is running in it, so close it and launch a fresh one
			// under the new scope; it resumes the same Codex thread. Refusing
			// instead left the chat unusable until someone closed it by hand
			// (a queued turn rebuilt its scope differently, 2026-10-07).
			c.logger.Infof("codex interactive scope changed owner=%s tmux=%s: relaunching under the new scope", ownerSessionID, session.tmuxSessionName)
			closeCodexSessionLocked(session, "security policy changed", c.logger)
			session.mu.Unlock()
			if relaunchedForScope(ctx) {
				return nil, false, errors.New("Codex session security policy changed; close the existing session before continuing")
			}
			return c.acquireCodexInteractiveSession(withRelaunchedForScope(ctx), ownerSessionID, opts, systemPrompt, initialPrompt)
		}
		if session.initErr != nil {
			err := session.initErr
			session.mu.Unlock()
			return nil, false, err
		}
		session.idleLease.Pause()
		c.logger.Debugf("codex interactive startup timing owner=%s stage=reuse_lock tmux=%s elapsed=%s total=%s", ownerSessionID, session.tmuxSessionName, time.Since(sessionLockStart).Round(time.Millisecond), time.Since(acquireStart).Round(time.Millisecond))
		return session, false, nil
	}

	// buildCodexInteractiveArgs also performs the opt-in workspace
	// artifact projection (AGENTS.md system prompt, .codex/config.toml
	// session defaults and optional MCP tables) up front so it can skip the CLI-side
	// -c model_instructions_file injection when project-instruction-only
	// mode is on AND the AGENTS.md projection succeeded. The returned
	// cleanup (nil when nothing was projected) is stored on the session
	// and run at teardown. The projection is off by default; the existing
	// -c model_instructions_file and -c mcp_servers.* overrides already
	// inject equivalent configuration. The workspace projection is
	// additive and useful when downstream tooling reads codex's on-disk
	// conventions directly.
	c.logger.Debugf("codex interactive building args owner=%s tmux=%s workingDir=%q", ownerSessionID, session.tmuxSessionName, workingDir)
	argsStart := time.Now()
	args, systemPromptTempFile, projectCleanup, err := c.buildCodexInteractiveArgs(opts, systemPrompt)
	if err != nil {
		session.initErr = err
		session.mu.Unlock()
		removeCodexPersistentSession(ownerSessionID, session)
		return nil, false, err
	}
	c.logger.Debugf("codex interactive startup timing owner=%s stage=build_args args=%d elapsed=%s total=%s", ownerSessionID, len(args), time.Since(argsStart).Round(time.Millisecond), time.Since(acquireStart).Round(time.Millisecond))
	args = appendCodexInitialPromptArgs(args, opts, initialPrompt)
	session.systemPromptTempFile = systemPromptTempFile
	session.projectInstructionCleanup = projectCleanup

	// Project attached skills into .agents/skills/ so Codex's
	// repo-scoped skill loader picks them up at startup. Independent
	// of writeProjectInstructionFromOptions (which gates the
	// AGENTS.md + .codex/ artifact projection); skills are useful
	// even when the operator-instruction-file projection is off.
	// Best-effort.
	if attachedSkills := llmtypes.AttachedSkillsFromOptions(opts); len(attachedSkills) > 0 && workingDir != "" {
		_ = c.ProjectSkills(llmtypes.CLIArtifactDirectory(opts, workingDir), attachedSkills)
	}
	if reaped := reapOrphanCodexTmuxSessions(ctx, ownerSessionID, session.tmuxSessionName, c.logger.Infof); len(reaped) > 0 {
		c.logger.Infof("codex interactive reaped orphaned tmux session(s) %v for owner %s before launching a new one", reaped, ownerSessionID)
	}
	c.logger.Debugf("codex interactive starting tmux owner=%s tmux=%s", ownerSessionID, session.tmuxSessionName)
	runtimeReadPaths := []string{systemPromptTempFile}
	runtimePathsStart := time.Now()
	mcpRuntimePaths, err := strictCodexMCPRuntimePaths(opts)
	if err != nil {
		session.initErr = err
		if systemPromptTempFile != "" {
			_ = os.Remove(systemPromptTempFile)
		}
		if session.projectInstructionCleanup != nil {
			session.projectInstructionCleanup()
			session.projectInstructionCleanup = nil
		}
		session.mu.Unlock()
		removeCodexPersistentSession(ownerSessionID, session)
		return nil, false, err
	}
	runtimeReadPaths = append(runtimeReadPaths, mcpRuntimePaths...)
	c.logger.Debugf("codex interactive startup timing owner=%s stage=runtime_paths elapsed=%s total=%s", ownerSessionID, time.Since(runtimePathsStart).Round(time.Millisecond), time.Since(acquireStart).Round(time.Millisecond))
	tmuxStart := time.Now()
	// Codex was the one retained CLI outside the isolation contract: the
	// structured path applies it, the other interactive adapters now do, and
	// this launch did not.
	scopedEnv, unsetEnv := llmtypes.ScopedCodingAgentEnvironmentPlan(os.Environ(), nil, opts)
	scopedScrub := launchscrub.Scoped(nil, scopedEnv, opts)
	if err := c.seedTmuxAccountLogin(opts, workingDir); err != nil {
		c.logger.Errorf("codex interactive could not save the API key login owner=%s: %v", ownerSessionID, err)
	}
	launchCleanup, err := startCodexTmuxSession(ctx, session.tmuxSessionName, args, workingDir, opts.CLISecurity, runtimeReadPaths, scopedEnv, unsetEnv, scopedScrub)
	if err != nil {
		c.logger.Errorf("codex interactive failed to start tmux owner=%s tmux=%s: %v", ownerSessionID, session.tmuxSessionName, err)
		session.initErr = err
		if systemPromptTempFile != "" {
			_ = os.Remove(systemPromptTempFile)
		}
		if session.projectInstructionCleanup != nil {
			session.projectInstructionCleanup()
			session.projectInstructionCleanup = nil
		}
		session.mu.Unlock()
		removeCodexPersistentSession(ownerSessionID, session)
		return nil, false, err
	}
	session.launchCleanup = launchCleanup
	registerCodexInteractiveSession(ownerSessionID, session.tmuxSessionName)
	c.logger.Debugf("codex interactive startup timing owner=%s stage=start_tmux tmux=%s elapsed=%s total=%s", ownerSessionID, session.tmuxSessionName, time.Since(tmuxStart).Round(time.Millisecond), time.Since(acquireStart).Round(time.Millisecond))
	return session, true, nil
}

func (c *CodexCLIAdapter) seedTmuxAccountLogin(opts *llmtypes.CallOptions, workingDir string) error {
	selected := llmtypes.MergeCodingAgentSecretEnvironment(codexLoginEnv(buildCodexStructuredEnv(c.apiKey), c.apiKey), opts)
	// The terminal login writer uses CODEX_API_KEY; accept OPENAI_API_KEY only
	// after account selection has removed any ambient/server credential.
	key := ""
	for _, entry := range selected {
		if v, ok := strings.CutPrefix(entry, "OPENAI_API_KEY="); ok {
			key = v
		}
	}
	return seedCodexAPIKeyLogin(codexLoginEnv(selected, key), llmtypes.CLIHomeEnvironment(opts)["CODEX_HOME"], workingDir)
}
