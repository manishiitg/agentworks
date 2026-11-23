package cursorcli

// The terminal transport is selected explicitly (CURSOR_CLI_TRANSPORT=tmux).
import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/internal/shelllaunch"
	"github.com/manishiitg/multi-llm-provider-go/internal/tmuxcontrol"
	"github.com/manishiitg/multi-llm-provider-go/internal/tmuxsize"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/launchscrub"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/paneview"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/sessionlease"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/sessionregistry"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/tmuxexec"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/tmuxlaunch"
	"github.com/manishiitg/multi-llm-provider-go/pkg/codingready"
	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxinput"
	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxstartup"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type cursorInteractiveSession struct {
	accountHome      string
	accountConfigDir string
	runtimeDir       string
	// Independent of mu: live input is accepted while GenerateContent owns mu.
	retainedMu         sync.Mutex
	retainedStoreDB    string
	retainedWorkingDir string
	retainedNativeID   string
	retainedInput      *cursorRetainedInput

	ownerSessionID  string
	tmuxSessionName string
	workingDir      string
	// pendingDurable scopes durability proofs per send; guarded by
	// durableMu (never session.mu — the watcher runs concurrent with
	// a turn that owns mu for its full lifetime).
	pendingDurable []cursorPendingDurableAck
	durableMu      sync.Mutex
	persistent     bool
	cleanupFiles   func()
	idleLease      sessionlease.Lease
	initErr        error
	createdAt      time.Time
	// scopeFingerprint identifies the credential scope this live process was
	// LAUNCHED with, so a later turn's changed scope replaces it rather than
	// silently reusing the old environment.
	scopeFingerprint string
	mu               sync.Mutex
}

var cursorInteractiveRegistry = sessionregistry.NewOwnerRegistry[string]()

var cursorPersistentRegistry = sessionregistry.NewOwnerRegistry[*cursorInteractiveSession]()

func (c *CursorCLIAdapter) generateContentTmux(ctx context.Context, messages []llmtypes.MessageContent, opts *llmtypes.CallOptions) (resp *llmtypes.ContentResponse, err error) {
	var tmuxSessionName string
	defer func() {
		if isCursorTmuxSessionLostError(err) {
			err = llmtypes.WrapCodingAgentTmuxSessionLostError(err, "cursor-cli", tmuxSessionName, "tmux session lost")
		}
	}()

	if _, err := exec.LookPath("tmux"); err != nil {
		return nil, fmt.Errorf("tmux not found in PATH; cursor-cli tmux mode requires tmux")
	}
	if _, err := exec.LookPath("cursor-agent"); err != nil {
		return nil, fmt.Errorf("cursor-agent not found in PATH. Install Cursor Agent CLI with `curl https://cursor.com/install -fsS | bash`")
	}

	persistent := cursorPersistentInteractiveFromOptions(opts)
	ownerSessionID := cursorInteractiveSessionIDFromOptions(opts)
	if ownerSessionID == "" {
		ownerSessionID = "cursor-bounded-" + cursorRandomHex(8)
	}
	ctx = llmtypes.ScopeCodingAgentQuestions(ctx, "cursor-cli", ownerSessionID)
	// Capture turn start before doing any I/O so the sidecar parser
	// can scope its store.db pick to a freshly-modified session.
	turnStart := time.Now()

	callCtx, cancel := cursorInteractiveCallContext(ctx)
	defer cancel()

	// On user-initiated cancellation, tear down the persistent tmux
	// session so the live pane closes alongside the workflow step.
	defer func() {
		if ctx.Err() != context.Canceled {
			return
		}
		closeCursorPersistentSession(ownerSessionID, "workflow context canceled", c.logger)
	}()

	systemPrompt, conversationMessages := splitCursorSystemPrompt(messages)
	historicalAssistantTexts := cursorAssistantHistory(conversationMessages)
	resumeID := cursorResumeSessionIDFromOptions(opts)
	resume := resumeID != ""
	launchOnly := llmtypes.CodingProviderLaunchOnlyFromOptions(opts)
	prompt := buildCursorPrompt(conversationMessages, resume)
	// A resumed Cursor chat keeps the rules it started with; a rewritten
	// .cursor/rules file is not re-read (verified on cursor-agent
	// 2026.09.18). Resend a changed system prompt once, inline.
	if resume && !launchOnly && strings.TrimSpace(prompt) != "" && cursorResumeSystemPromptChanged(cursorWorkingDirFromOptions(opts), resumeID, systemPrompt) {
		prompt = "[Updated System Instructions]\n" + systemPrompt + "\n\n[User Message]\n" + prompt
	}
	// Launch-only: boot tmux with --resume so the user can see the prior
	// cursor conversation in the pane without sending any prompt yet.
	// Mirrors what claude-code experimental does; the chat-history
	// resumed-terminal path calls model.GenerateContent(ctx, nil, ...)
	// with WithCodingProviderLaunchOnly() expecting this contract.
	if !launchOnly && strings.TrimSpace(prompt) == "" {
		if opts.StreamChan != nil {
			close(opts.StreamChan)
		}
		return nil, fmt.Errorf("cursor-cli prompt is empty")
	}

	transportLaunchStartedAt := time.Now()
	session, created, err := c.acquireCursorInteractiveSession(callCtx, ownerSessionID, persistent, opts, systemPrompt)
	if err != nil {
		if opts.StreamChan != nil {
			close(opts.StreamChan)
		}
		return nil, err
	}
	tmuxSessionName = session.tmuxSessionName
	if resumeID == "" {
		resumeID = session.retainedNativeID
	}
	session.setRetainedStore(resumeID)
	stopCursorRetainedControls(session.tmuxSessionName)
	tmuxstartup.Publish(callCtx, opts.StreamChan, "cursor-cli", c.modelID, session.tmuxSessionName, session.workingDir, map[string]interface{}{
		"cursor_interactive_session": session.tmuxSessionName,
	})
	releaseSession := true
	defer func() {
		if !releaseSession || session == nil {
			return
		}
		if err == nil {
			startCursorRetainedControls(ctx, session.tmuxSessionName, cursorAutoApproveWebSearchFromOptions(opts))
		}
		if persistent {
			releaseCursorInteractiveSession(session, c.logger)
		} else {
			releaseCursorBoundedInteractiveSession(session, c.logger)
		}
	}()

	if created {
		if err := waitForCursorPrompt(callCtx, session.tmuxSessionName, opts.StreamChan, cursorInteractiveStreamTmuxScreenEnabled(opts)); err != nil {
			markCursorInteractiveSessionFailedLocked(session, err, c.logger)
			releaseSession = false
			failedSession := session
			session.mu.Unlock()
			session = nil
			cleanupFailedCursorInteractiveSession(failedSession)
			if opts.StreamChan != nil {
				close(opts.StreamChan)
			}
			return nil, err
		}
	}

	// Cold session: the input prompt being ready does not mean cursor's MCP
	// servers (the bridge) have finished connecting. Hold the first prompt until
	// the bridge reports tools/list answered (marker file). No-op on reused
	// sessions / when unconfigured; bounded, best-effort. See pkg/codingready.
	if created && opts.Metadata != nil {
		if rf := codingready.MCPReadyFileFromMetadata(opts.Metadata.Custom); rf != "" {
			codingready.WaitForMCPReadyFile(callCtx, rf, codingready.MCPReadyWait())
		}
	}

	baseline, _ := captureCursorPane(callCtx, session.tmuxSessionName)
	// Launch-only path: cursor is up with --resume applied (if resumeID
	// was provided), the pane shows whatever cursor has restored from
	// its store.db, and we hand back a SessionHandle so the orchestrator
	// can rebind to this tmux session on subsequent turns instead of
	// spawning yet another one.
	if launchOnly {
		// A resumed Cursor process briefly paints its generic welcome composer
		// before it has loaded the native conversation. The generic startup wait
		// deliberately accepts that banner after a grace period for brand-new
		// sessions, but returning it as a restored session is unsafe: an immediate
		// follow-up can be pasted into the transient composer and discarded. For a
		// real resume, give the restored conversation time to replace it. Older
		// native sessions can remain on the welcome composer; that fallback is
		// accepted only after the longer restored-session settling window.
		if resumeID != "" {
			if err := waitForCursorRestoredPrompt(callCtx, session.tmuxSessionName, opts.StreamChan, cursorInteractiveStreamTmuxScreenEnabled(opts)); err != nil {
				markCursorInteractiveSessionFailedLocked(session, err, c.logger)
				releaseSession = false
				failedSession := session
				session.mu.Unlock()
				session = nil
				cleanupFailedCursorInteractiveSession(failedSession)
				if opts.StreamChan != nil {
					close(opts.StreamChan)
				}
				return nil, err
			}
			// Cursor can paint a normal-looking composer while the resumed TUI is
			// still replacing its internal editor. That transient state has no
			// unique text marker once the header scrolls out of a shorter pane.
			// Enforce a bounded minimum launch age as the final readiness signal;
			// this is paid only when rebuilding a missing tmux transport, not on
			// ordinary follow-up turns.
			if remaining := time.Until(transportLaunchStartedAt.Add(cursorRestoredSessionMinReadyAge)); remaining > 0 {
				timer := time.NewTimer(remaining)
				select {
				case <-callCtx.Done():
					timer.Stop()
					return nil, callCtx.Err()
				case <-timer.C:
				}
			}
		}
		tmuxinput.MarkReady(session.tmuxSessionName)
		var lastSnapshot string
		streamCursorTerminalSnapshot(callCtx, session.tmuxSessionName, opts.StreamChan, &lastSnapshot)
		if opts.StreamChan != nil {
			close(opts.StreamChan)
		}
		nativeSessionID := resumeID
		if nativeSessionID == "" {
			if _, storeDBPath := readCursorTranscriptMessagesAndStoreDB(turnStart, session.workingDir, nil, resumeID, session.accountHome, session.accountConfigDir); storeDBPath != "" {
				nativeSessionID = cursorNativeSessionIDFromStoreDBPath(storeDBPath)
			}
		}
		additional := map[string]interface{}{
			"provider":                      "cursor-cli",
			"cursor_mode":                   "tmux",
			"cursor_interactive_session":    session.tmuxSessionName,
			"cursor_persistent_interactive": persistent,
			"cursor_uses_print_json":        false,
			"cursor_working_dir":            session.workingDir,
			"cursor_launch_only":            true,
		}
		if nativeSessionID != "" {
			additional["cursor_session_id"] = nativeSessionID
			session.setRetainedStore(nativeSessionID)
		}
		gi := &llmtypes.GenerationInfo{Additional: additional}
		llmtypes.AttachCodingProviderSessionHandle(gi, llmtypes.CodingProviderSessionHandle{
			Provider:        "cursor-cli",
			Transport:       llmtypes.CodingProviderTransportTmux,
			NativeSessionID: nativeSessionID,
			TmuxSession:     session.tmuxSessionName,
			WorkingDir:      session.workingDir,
			RuntimeDir:      session.runtimeDir,
			Model:           c.modelID,
		})
		return &llmtypes.ContentResponse{
			Choices: []*llmtypes.ContentChoice{{Content: "", GenerationInfo: gi}},
		}, nil
	}
	// Prime history before submitting: a fast response must not be classified
	// as pre-existing history by the streaming deduplicator.
	var streamState *cursorTranscriptStreamState
	if opts.StreamChan != nil && cursorInteractiveStreamTranscriptEnabled(opts) {
		streamState = newCursorTranscriptStreamState(turnStart, session.workingDir, ownerSessionID, resumeID, session.accountHome, session.accountConfigDir)
	}
	c.logInfof("Executing Cursor Agent CLI tmux session: %s", session.tmuxSessionName)
	// Snapshot only an already-pinned store (resumed session). A cold
	// session has no store for this chat yet, and discovering one here
	// would pin whichever sibling chat in the same workdir was freshest
	// (an earlier chat or helper run), blinding every later durable ack.
	initialStore := pinnedCursorStoreNoMu(session)
	if initialStore == "" && resumeID != "" {
		// A resumed native session already holds earlier turns; snapshot it
		// so this turn records only what follows its prompt.
		home := session.accountHome
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		initialStore = cursorStoreDBForNativeSession(home, session.workingDir, resumeID, session.accountConfigDir)
	}
	initialRefs := cursorSnapshotStoreRefs(initialStore)
	if err := cursorConfirmInitialSubmitAfterPaneError(ctx, session, prompt, turnStart, initialRefs,
		sendCursorInitialPromptToTmux(callCtx, session.tmuxSessionName, prompt)); err != nil {
		markCursorInteractiveSessionFailedLocked(session, err, c.logger)
		releaseSession = false
		failedSession := session
		session.mu.Unlock()
		session = nil
		cleanupFailedCursorInteractiveSession(failedSession)
		if opts.StreamChan != nil {
			close(opts.StreamChan)
		}
		return nil, err
	}
	tmuxinput.MarkReady(session.tmuxSessionName)

	// Opt-in structured streaming: poll cursor's store.db mid-turn so a
	// design-first UI gets assistant text + tool-call starts without the pane.
	// Runs as a goroutine (the wait function's signature is already large) and is
	// stopped — with a final flush — before any close(opts.StreamChan) below.
	var cursorStreamStop context.CancelFunc
	var cursorStreamDone chan struct{}
	if streamState != nil {
		var streamCtx context.Context
		streamCtx, cursorStreamStop = context.WithCancel(callCtx)
		cursorStreamDone = make(chan struct{})
		go func() { defer close(cursorStreamDone); streamState.run(streamCtx, opts.StreamChan) }()
	}

	storeAnswer := func(query string) (string, cursorTrailState) {
		storeDB := resolveCursorStoreNoMu(session)
		trail := readCursorRetainedInput(&cursorRetainedInput{
			storeDB:  storeDB,
			query:    strings.Join(strings.Fields(query), " "),
			baseline: initialRefs,
		})
		return cursorTurnTrailState(trail)
	}
	captured, storeFinal, err := waitForCursorInteractiveResponseWithStore(callCtx, session.tmuxSessionName, baseline, prompt, historicalAssistantTexts, opts.StreamChan, cursorAutoApproveWebSearchFromOptions(opts), cursorInteractiveStreamTmuxScreenEnabled(opts), storeAnswer)
	if cursorStreamStop != nil {
		cursorStreamStop()
		<-cursorStreamDone // goroutine (and its final flush) done before any close(opts.StreamChan)
	}
	forcedComplete := errors.Is(err, tmuxcontrol.ErrForceComplete)
	if err != nil && !forcedComplete {
		err = tmuxexec.WithFailureDiagnostic(err, "cursor-cli", session.tmuxSessionName)
		if isCursorTmuxSessionLostError(err) {
			markCursorInteractiveSessionFailedLocked(session, err, c.logger)
			releaseSession = false
			failedSession := session
			session.mu.Unlock()
			session = nil
			cleanupFailedCursorInteractiveSession(failedSession)
		} else if ctx.Err() != nil {
			interruptCursorInteractiveSession(session.tmuxSessionName, c.logger)
		}
		if opts.StreamChan != nil {
			close(opts.StreamChan)
		}
		return nil, err
	}

	content := parseCursorInteractiveResponse(captured, baseline, prompt, historicalAssistantTexts)
	completionSource := "pane"
	if storeFinal != "" {
		content = storeFinal
		completionSource = "store_turn_answer"
	}
	if forcedComplete && strings.TrimSpace(content) == "" {
		content = forcedCursorInteractiveResponse(captured, baseline, prompt, historicalAssistantTexts)
	}
	// The pane told us the turn ended, which it is reliable at. It is NOT
	// reliable for the reply TEXT: a terminal hard-wraps, and a soft wrap is
	// indistinguishable from a newline the model typed, so markdown structure
	// arrives broken. cursor has already written the same reply unwrapped to its
	// own store.db, so prefer that when it demonstrably holds this same message.
	// See llmtypes.ReconcileFinalAnswer for why the pane is used as a checksum
	// rather than simply replaced (store.db commits asynchronously, so it can
	// still hold the PREVIOUS turn).
	// Read cursor's own store.db transcript ONCE for this turn. It is needed
	// twice — to recover the unwrapped reply text here, and for the native
	// session ID / intermediate messages further down — and the read polls for up
	// to 4s on cursor's async commit, so it must not happen twice per turn.
	// This turn's messages are what Cursor committed after the snapshot taken
	// before its prompt: one turn at a time, nothing carried across turns.
	sidecarMsgs, storeDBPath := readCursorTranscriptMessagesAndStoreDB(turnStart, session.workingDir, initialRefs, resumeID, session.accountHome, session.accountConfigDir)
	content = llmtypes.ReconcileFinalAnswer(content, latestCursorAssistantText(sidecarMsgs))
	// Trailing-capture grace window — see llmtypes.RunTrailingPaneCapture.
	llmtypes.RunTrailingPaneCapture(callCtx, opts.StreamChan,
		func(ctx context.Context) (string, error) {
			snap, err := captureCursorPane(ctx, session.tmuxSessionName)
			if err != nil {
				return "", err
			}
			return strings.TrimRight(stripCursorANSI(snap), "\n"), nil
		},
		map[string]interface{}{
			"tmux_session":               session.tmuxSessionName,
			"cursor_interactive_session": session.tmuxSessionName,
		},
	)
	if opts.StreamChan != nil {
		close(opts.StreamChan)
	}

	additional := map[string]interface{}{
		"provider":                      "cursor-cli",
		"cursor_mode":                   "tmux",
		"cursor_interactive_session":    session.tmuxSessionName,
		"cursor_persistent_interactive": persistent,
		"cursor_uses_print_json":        false,
		"cursor_completion_source":      completionSource,
		"cursor_working_dir":            session.workingDir,
		// Cursor's TUI exposes neither tokenizer telemetry nor billing totals.
		// Preserve this fact through the event bridge so product UIs do not
		// present the fallback character count as exact model usage.
		"token_usage_estimated": true,
		"token_usage_source":    "character_estimate",
	}
	if !persistent {
		// terminal_retention_seconds intentionally not set: the rail
		// snapshot stays read-only until the user dismisses it via the
		// X button. Tmux itself is killed quickly after the turn via
		// the bounded-session cleanup using llmtypes.TmuxKillDelay.
		// The cursor_interactive_retention_seconds value is kept for
		// any backend code that still tracks it for diagnostics.
		additional["cursor_interactive_retention_seconds"] = int(cursorInteractiveRetention().Seconds())
	}

	// Cursor's tmux TUI does not expose exact token counts in a format the
	// adapter can read (the running counter on the "⠰⠰ Composing 1.87k
	// tokens" line is cleared once the turn settles). We approximate so the
	// cost ledger receives a non-zero row rather than a bare timestamp.
	// The approximation is char-based (≈ 4 chars/token for English prose,
	// the standard fallback used elsewhere in this codebase); cost is then
	// computed via the same ComputeUSDCostFromMetadata path the structured
	// adapter uses. Numbers may be ±20-30% off the true tokenizer counts —
	// good enough for cost-tracking trends, not for fine-grained per-call
	// billing reconciliation.
	inputTokens, outputTokens := estimateCursorTmuxTokens(prompt, content)
	totalTokens := inputTokens + outputTokens
	genInfo := &llmtypes.GenerationInfo{
		InputTokens:  intPtrFromInt(inputTokens),
		OutputTokens: intPtrFromInt(outputTokens),
		TotalTokens:  intPtrFromInt(totalTokens),
		Additional:   additional,
	}
	costLookupModel := c.modelID
	if opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		if model, ok := opts.Metadata.Custom[MetadataKeyCursorModel].(string); ok && strings.TrimSpace(model) != "" {
			costLookupModel = model
		}
	}
	if costLookupModel != "" {
		if meta, _ := c.GetModelMetadata(costLookupModel); meta != nil {
			if cost := llmtypes.ComputeUSDCostFromMetadata(meta, genInfo); cost > 0 {
				additional["cost_usd_estimated"] = cost
				additional["cost_model_id"] = costLookupModel
			}
		}
	}

	// Reconstruct the CLI's internal tool-use trail from cursor's
	// local sqlite store at ~/.cursor/chats/<md5(cwd)>/<agentId>/store.db.
	// Cursor doesn't expose tokens (subscription-priced) but stores
	// the full conversation including tool-call / tool-result blocks,
	// so workflow conversation logs gain the same richness as the
	// claude-code / codex tmux flows.
	//
	// The same store.db path also carries cursor's native session ID
	// (the <agentId> dir name), which `cursor-agent --resume <id>`
	// accepts. Publishing it as additional[cursor_session_id] is what
	// lets mcpagent persist the ID across server restarts so a chat
	// can be resumed in a fresh tmux session. Without this, resume is
	// silently broken in tmux mode even though the rest of the
	// orchestrator wiring works end-to-end.
	// sidecarMsgs/storeDBPath were read once, up where the final answer is
	// reconciled — see the comment there.
	if len(sidecarMsgs) > 0 {
		llmtypes.AttachCodingProviderIntermediateMessages(genInfo, llmtypes.CodingProviderIntermediateMessages{
			Provider:  "cursor-cli",
			Transport: llmtypes.CodingProviderTransportTmux,
			Messages:  sidecarMsgs,
		})
	}
	nativeSessionID := cursorNativeSessionIDFromStoreDBPath(storeDBPath)
	if nativeSessionID == "" {
		// Fall back to a caller-supplied resume ID so a SessionHandle
		// still gets attached when cursor hasn't committed the store.db
		// yet (the trivial-turn race documented above).
		nativeSessionID = resumeID
	}
	if nativeSessionID != "" {
		additional["cursor_session_id"] = nativeSessionID
		session.setRetainedStore(nativeSessionID)
	}
	// Attach a SessionHandle so the orchestrator can reattach to this
	// same tmux pane on future turns (rebind path), and so the
	// resumed-terminal restore tier picks up the native session ID even
	// when the agent isn't kept in memory.
	llmtypes.AttachCodingProviderSessionHandle(genInfo, llmtypes.CodingProviderSessionHandle{
		Provider:        "cursor-cli",
		Transport:       llmtypes.CodingProviderTransportTmux,
		NativeSessionID: nativeSessionID,
		TmuxSession:     session.tmuxSessionName,
		WorkingDir:      session.workingDir,
		RuntimeDir:      session.runtimeDir,
		Model:           c.modelID,
	})

	return &llmtypes.ContentResponse{
		Choices: []*llmtypes.ContentChoice{
			{
				Content:        content,
				GenerationInfo: genInfo,
			},
		},
		Usage: &llmtypes.Usage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			TotalTokens:  totalTokens,
		},
	}, nil
}

func estimateCursorTmuxTokens(prompt, content string) (int, int) {
	estimate := func(s string) int {
		n := len(s)
		if n == 0 {
			return 0
		}
		// (n + 3) / 4 = ceil(n / 4)
		return (n + 3) / 4
	}
	return estimate(prompt), estimate(content)
}

func (c *CursorCLIAdapter) acquireCursorInteractiveSession(ctx context.Context, ownerSessionID string, persistent bool, opts *llmtypes.CallOptions, systemPrompt string) (*cursorInteractiveSession, bool, error) {
	now := time.Now()
	session, created, ok := cursorPersistentRegistry.GetOrCreate(ownerSessionID, func() *cursorInteractiveSession {
		session := &cursorInteractiveSession{
			ownerSessionID:   ownerSessionID,
			accountHome:      llmtypes.CLIHomeEnvironment(opts)["HOME"],
			tmuxSessionName:  newCursorTmuxSessionName(),
			persistent:       persistent,
			createdAt:        now,
			scopeFingerprint: cursorTmuxScopeFingerprint(opts, c.modelID),
		}
		session.mu.Lock()
		return session
	})
	if !ok {
		return nil, false, fmt.Errorf("cursor-cli tmux mode requires an owner session ID")
	}
	if !created {
		// The registry lock protects only the owner map. Take the per-session
		// lock after lookup so one busy Cursor turn does not block unrelated
		// session acquisition.
		session.mu.Lock()
		// The live process holds the environment it launched with, so a
		// changed (or removed) scope cannot take effect without replacing it.
		if session.scopeFingerprint != cursorTmuxScopeFingerprint(opts, c.modelID) {
			session.mu.Unlock()
			closeCursorPersistentSession(ownerSessionID, "Cursor credential scope changed", c.logger)
			return c.acquireCursorInteractiveSession(ctx, ownerSessionID, persistent, opts, systemPrompt)
		}
		if session.initErr != nil {
			err := session.initErr
			session.mu.Unlock()
			return nil, false, err
		}
		session.idleLease.Pause()
		return session, false, nil
	}

	args, env, workingDir, cleanupFiles, err := c.buildCursorInteractiveLaunch(opts, systemPrompt, ownerSessionID)
	if err != nil {
		session.initErr = err
		session.mu.Unlock()
		removeCursorPersistentSession(ownerSessionID, session)
		return nil, false, err
	}
	session.workingDir = workingDir
	session.runtimeDir = opts.CLIRuntimeDir
	session.cleanupFiles = cleanupFiles

	// The structured path applies the caller's scoped environment when it
	// builds cmd.Env; a tmux pane has no such slice -- it inherits the tmux
	// SERVER's environment -- so the same contract has to be executed inside
	// the launch script as export/unset. Without this, an interactive session
	// silently ran with whatever credentials were ambient on the backend.
	scopedEnv, unsetEnv := llmtypes.ScopedCodingAgentEnvironmentPlan(os.Environ(), env, opts)
	scopedScrub := launchscrub.Scoped(env, scopedEnv, opts)
	env = append(env, scopedEnv...)
	env = append(env, cursorAmbientAPIKeyEnv(env, os.Getenv("CURSOR_API_KEY"), opts)...)
	if llmtypes.CLIHasPrivateRuntime(opts) {
		configDir, configErr := prepareCursorPrivateRuntime(opts, llmtypes.MergeCodingAgentSecretEnvironment(buildCursorStructuredEnv(c.apiKey), opts))
		if configErr != nil {
			cleanupFiles()
			session.initErr = configErr
			session.mu.Unlock()
			removeCursorPersistentSession(ownerSessionID, session)
			return nil, false, configErr
		}
		env = append(env, "CURSOR_CONFIG_DIR="+configDir)
		session.accountConfigDir = configDir
	}

	// Allocate the exact native ID before the first prompt. Never discover a
	// different conversation by choosing the newest store in a shared cwd.
	nativeID := cursorResumeSessionIDFromOptions(opts)
	if nativeID == "" {
		nativeID, err = createCursorTmuxChat(ctx, opts, workingDir, env)
		if err != nil {
			cleanupFiles()
			session.initErr = err
			session.mu.Unlock()
			removeCursorPersistentSession(ownerSessionID, session)
			return nil, false, err
		}
		args = append(args, "--resume", nativeID)
	}
	session.setRetainedStore(nativeID)
	args, cleanupSandbox, err := cursorLandlockArgs(opts, args, workingDir)
	if err != nil {
		session.initErr = fmt.Errorf("confine Cursor: %w", err)
		if cleanupFiles != nil {
			cleanupFiles()
		}
		session.mu.Unlock()
		removeCursorPersistentSession(ownerSessionID, session)
		return nil, false, session.initErr
	}
	time.AfterFunc(30*time.Second, cleanupSandbox)
	if err := startCursorTmuxSession(ctx, session.tmuxSessionName, args, env, unsetEnv, scopedScrub, workingDir); err != nil {
		session.initErr = err
		if cleanupFiles != nil {
			cleanupFiles()
		}
		session.mu.Unlock()
		removeCursorPersistentSession(ownerSessionID, session)
		return nil, false, err
	}
	registerCursorInteractiveSession(ownerSessionID, session.tmuxSessionName)
	return session, true, nil
}

func (c *CursorCLIAdapter) buildCursorInteractiveLaunch(opts *llmtypes.CallOptions, systemPrompt string, ownerSessionID string) ([]string, []string, string, func(), error) {
	workingDir := cursorWorkingDirFromOptions(opts)
	if workingDir == "" {
		workingDir = cursorMustGetwd()
	}
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		return nil, nil, "", nil, fmt.Errorf("failed to create Cursor CLI working directory: %w", err)
	}

	// Project attached skills into .cursor/skills/ so Cursor's native
	// Agent Skills loader picks them up at session start. Non-fatal:
	// the listing is also in the system prompt via mcpagent.
	if skills := llmtypes.AttachedSkillsFromOptions(opts); len(skills) > 0 {
		_ = c.ProjectSkills(llmtypes.CLIArtifactDirectory(opts, workingDir), skills)
	}

	cleanupFiles, err := prepareCursorProjectFiles(llmtypes.CLIArtifactDirectory(opts, workingDir), systemPrompt, opts, ownerSessionID)
	if err != nil {
		return nil, nil, "", nil, err
	}

	modelToUse := cursorCLIModelForLaunch(c.modelID)
	if opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		if model, ok := opts.Metadata.Custom[MetadataKeyCursorModel].(string); ok {
			modelToUse = resolveCursorCLIModelID(model)
		}
	}

	modelToUse = cursorModelWithReasoning(modelToUse, opts)

	// --trust: AgentWorks picks the working folder (a private runtime folder per
	// chat), so Cursor must not stop on its "Workspace Trust Required" screen
	// for each new one (found by the cross-CLI sandbox contract, 2026-10-04). A
	// flag, not a key press on the screen.
	args := []string{"cursor-agent", "--workspace", workingDir, "--trust"}
	if llmtypes.CLIHasPrivateRuntime(opts) {
		args = append(args, "--plugin-dir", opts.CLIRuntimeDir)
	}
	if opts != nil {
		// Granted folders reached through links (project/) are outside Cursor's
		// workspace; --add-dir makes them workspace roots (see SeatbeltGrantedDirs).
		for _, dir := range clisandbox.SeatbeltGrantedDirs(opts.CLISecurity, workingDir) {
			args = append(args, "--add-dir", dir)
		}
	}

	if modelToUse != "" {
		args = append(args, "--model", modelToUse)
	}
	if opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		// Match native ACP: --force must not bypass bridge-only or full-mode hooks.
		if cursorMetaBool(opts, MetadataKeyForce) && !cursorMetaBool(opts, MetadataKeyDenyBuiltinTools) && !cursorFullNativeFromOptions(opts) {
			args = append(args, "--force")
		}
		if autoReview, ok := opts.Metadata.Custom[MetadataKeyAutoReview].(bool); ok && autoReview {
			args = append(args, "--auto-review")
		}
		if approve, ok := opts.Metadata.Custom[MetadataKeyApproveMCPs].(bool); ok && approve {
			args = append(args, "--approve-mcps")
		}
		sandbox, _ := opts.Metadata.Custom[MetadataKeySandbox].(string)
		if sandbox = cursorSandboxUnderSeatbelt(opts, strings.TrimSpace(sandbox)); sandbox != "" {
			args = append(args, "--sandbox", sandbox)
		}
		if mode, ok := opts.Metadata.Custom[MetadataKeyMode].(string); ok && strings.TrimSpace(mode) != "" {
			args = append(args, "--mode", strings.TrimSpace(mode))
		}
		if resumeID, ok := opts.Metadata.Custom[MetadataKeyResumeSessionID].(string); ok && strings.TrimSpace(resumeID) != "" {
			args = append(args, "--resume", strings.TrimSpace(resumeID))
		}
		if headers, ok := opts.Metadata.Custom[MetadataKeyHeaders].([]string); ok {
			for _, header := range headers {
				if strings.TrimSpace(header) != "" {
					args = append(args, "-H", strings.TrimSpace(header))
				}
			}
		}
		if pluginDirs, ok := opts.Metadata.Custom[MetadataKeyPluginDirs].([]string); ok {
			for _, dir := range pluginDirs {
				if strings.TrimSpace(dir) != "" {
					args = append(args, "--plugin-dir", strings.TrimSpace(dir))
				}
			}
		}
	}

	// Apply the selected account before protecting launch exports from shell startup.
	selected := llmtypes.MergeCodingAgentSecretEnvironment(buildCursorStructuredEnv(c.apiKey), opts)
	env := []string{}
	for _, entry := range selected {
		if strings.HasPrefix(entry, "CURSOR_API_KEY=") {
			env = append(env, entry)
		}
	}
	return args, env, workingDir, cleanupFiles, nil
}

func writeCursorDenyBuiltinHooks(cursorDir string, fullNativeMode ...bool) (func(), error) {
	return writeCursorDenyBuiltinHooksWithBridge(cursorDir, len(fullNativeMode) > 0 && fullNativeMode[0], "")
}

func releaseCursorInteractiveSession(session *cursorInteractiveSession, logger interfaces.Logger) {
	if session == nil {
		return
	}
	session.idleLease.Arm(cursorInteractiveIdleTimeout(), func() {
		closeCursorPersistentSession(session.ownerSessionID, "idle timeout", logger)
	})
	session.mu.Unlock()
}

func releaseCursorBoundedInteractiveSession(session *cursorInteractiveSession, logger interfaces.Logger) {
	if session == nil {
		return
	}
	// Keep the real tmux pane alive for the shared bounded retention window so
	// the UI terminal remains inspectable/debuggable while it is visible.
	retention := llmtypes.TmuxKillDelay
	if retention <= 0 {
		closeCursorSessionLocked(session, "bounded turn complete", logger)
		return
	}
	if logger != nil {
		logger.Debugf("Retaining completed Cursor interactive session %s for owner %s for %s (then kill)", session.tmuxSessionName, session.ownerSessionID, retention)
	}
	session.idleLease.Arm(retention, func() {
		closeCursorPersistentSession(session.ownerSessionID, "bounded retention elapsed", logger)
	})
	session.mu.Unlock()
}

func closeCursorPersistentSession(ownerSessionID, reason string, logger interfaces.Logger) {
	session, ok := cursorPersistentRegistry.Delete(ownerSessionID)
	if !ok || session == nil {
		return
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	closeCursorSessionLocked(session, reason, logger)
}

func CloseCursorCLIInteractiveSessionByTmux(tmuxSessionName, reason string) {
	name := strings.TrimSpace(tmuxSessionName)
	if name == "" {
		return
	}
	owner, _, ok := cursorPersistentRegistry.Find(func(session *cursorInteractiveSession) bool {
		return session != nil && session.tmuxSessionName == name
	})
	if !ok || owner == "" {
		return
	}
	closeCursorPersistentSession(owner, reason, nil)
}

func closeCursorSessionLocked(session *cursorInteractiveSession, reason string, logger interfaces.Logger) {
	if session == nil {
		return
	}
	session.idleLease.Stop()
	if logger != nil {
		logger.Debugf("Closing Cursor interactive session %s for owner %s: %s", session.tmuxSessionName, session.ownerSessionID, reason)
	}
	removeCursorPersistentSession(session.ownerSessionID, session)
	unregisterCursorInteractiveSession(session.ownerSessionID, session.tmuxSessionName)
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = tmuxinput.Default.Do(closeCtx, tmuxinput.Request{
		SessionID: session.tmuxSessionName,
		Source:    "cursor-cli-close",
		Priority:  tmuxinput.PriorityInterrupt,
	}, func(ctx context.Context) error {
		_ = runCursorCommand(ctx, nil, "tmux", "send-keys", "-t", session.tmuxSessionName, "C-c")
		return killCursorTmuxSession(ctx, session.tmuxSessionName)
	})
	if session.cleanupFiles != nil {
		session.cleanupFiles()
		session.cleanupFiles = nil
	}
}

func markCursorInteractiveSessionFailedLocked(session *cursorInteractiveSession, err error, logger interfaces.Logger) {
	if session == nil {
		return
	}
	if err != nil {
		session.initErr = err
	}
	session.idleLease.Stop()
	if logger != nil {
		logger.Debugf("Discarding Cursor interactive session %s for owner %s: %v", session.tmuxSessionName, session.ownerSessionID, err)
	}
}

func cleanupFailedCursorInteractiveSession(session *cursorInteractiveSession) {
	if session == nil {
		return
	}
	removeCursorPersistentSession(session.ownerSessionID, session)
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = killCursorTmuxSession(cleanupCtx, session.tmuxSessionName)
	unregisterCursorInteractiveSession(session.ownerSessionID, session.tmuxSessionName)
	if session.cleanupFiles != nil {
		session.cleanupFiles()
	}
}

func removeCursorPersistentSession(ownerSessionID string, session *cursorInteractiveSession) {
	cursorPersistentRegistry.DeleteIf(ownerSessionID, session)
}

func stopCursorIdleTimerAndSnapshotCleanupIfAvailable(session *cursorInteractiveSession) func() {
	if session == nil || !session.mu.TryLock() {
		return nil
	}
	defer session.mu.Unlock()
	session.idleLease.Stop()
	cleanupFiles := session.cleanupFiles
	session.cleanupFiles = nil
	return cleanupFiles
}

func registerCursorInteractiveSession(ownerSessionID, tmuxSessionName string) {
	ownerSessionID = strings.TrimSpace(ownerSessionID)
	tmuxSessionName = strings.TrimSpace(tmuxSessionName)
	if ownerSessionID == "" || tmuxSessionName == "" {
		return
	}
	tmuxinput.MarkStartingForOwner(tmuxSessionName, ownerSessionID)
	cursorInteractiveRegistry.Set(ownerSessionID, tmuxSessionName)
}

func unregisterCursorInteractiveSession(ownerSessionID, tmuxSessionName string) {
	stopCursorRetainedControls(tmuxSessionName)
	cursorInteractiveRegistry.DeleteIf(ownerSessionID, tmuxSessionName)
	tmuxinput.RemoveReadiness(tmuxSessionName)
}

func activeCursorInteractiveSession(ownerSessionID string) (string, bool) {
	sessionName, ok := cursorInteractiveRegistry.Get(ownerSessionID)
	return sessionName, ok && strings.TrimSpace(sessionName) != ""
}

func SendCursorInteractiveInput(ctx context.Context, ownerSessionID, message string) error {
	sessionName, ok := activeCursorInteractiveSession(ownerSessionID)
	if !ok {
		return fmt.Errorf("no active Cursor interactive session registered for owner session %s", ownerSessionID)
	}
	since := time.Now()
	stashCursorDurableReceiptForSend(ownerSessionID, message, since)
	session, retained := cursorPersistentRegistry.Get(ownerSessionID)
	if !retained || session == nil {
		return cursorConfirmLiveSubmitAfterPaneError(ctx, ownerSessionID, message, sendCursorLiveInputToTmux(ctx, sessionName, message))
	}
	retention := cursorInteractiveIdleTimeout()
	if !session.persistent {
		retention = llmtypes.TmuxKillDelay
	}
	if retention <= 0 || !session.idleLease.Arm(retention, func() {
		closeCursorPersistentSession(session.ownerSessionID, "idle timeout", nil)
	}) {
		return fmt.Errorf("Cursor session for owner %s is expiring; retry to resume it in a fresh tmux session", ownerSessionID)
	}
	// Install the boundary before typing; otherwise a fast reply can commit
	// before the completion watcher starts. Restore it if delivery fails.
	session.retainedMu.Lock()
	previous := session.retainedInput
	boundary := newCursorRetainedInput(session.resolveRetainedStoreLocked(), message)
	session.retainedInput = boundary
	primeCursorRetainedProgress(ownerSessionID, boundary)
	session.retainedMu.Unlock()
	if err := cursorConfirmLiveSubmitAfterPaneError(ctx, ownerSessionID, message, sendCursorLiveInputToTmux(ctx, sessionName, message)); err != nil {
		session.retainedMu.Lock()
		if session.retainedInput == boundary {
			session.retainedInput = previous
		}
		session.retainedMu.Unlock()
		return err
	}
	return nil
}

func cursorAmbientAPIKeyEnv(env []string, ambientKey string, opts *llmtypes.CallOptions) []string {
	ambientKey = strings.TrimSpace(ambientKey)
	if ambientKey == "" || llmtypes.CodingAgentScopeDeclared(opts) || len(llmtypes.ProviderAccountEnvironment(opts)) > 0 {
		return nil
	}
	for _, entry := range env {
		if strings.HasPrefix(entry, "CURSOR_API_KEY=") {
			return nil
		}
	}
	return []string{"CURSOR_API_KEY=" + ambientKey}
}

func cursorConfirmLiveSubmitAfterPaneError(ctx context.Context, ownerSessionID, message string, submitErr error) error {
	if submitErr == nil {
		return nil
	}
	ack, err := AwaitCursorInputDurable(ctx, ownerSessionID, message, 0)
	if err == nil && ack.Outcome == CursorDurableAckConfirmed {
		return nil
	}
	if err == nil && ack.Outcome == CursorDurableAckUnflushed {
		return &CursorInputUnflushedError{OwnerSessionID: ownerSessionID, Latency: ack.Latency}
	}
	return fmt.Errorf("%w; Cursor delivery unconfirmed by store: %w", submitErr, err)
}

func cursorAutoApproveWebSearchFromOptions(opts *llmtypes.CallOptions) bool {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return false
	}
	enabled, _ := opts.Metadata.Custom[MetadataKeyAutoApproveWebSearch].(bool)
	return enabled
}

func startCursorTmuxSession(ctx context.Context, sessionName string, args []string, env, unset []string, scrub *shelllaunch.ScopeScrub, workingDir string) error {
	if workingDir == "" {
		workingDir = cursorMustGetwd()
	}
	shellCommand := "cd " + cursorShellQuote(workingDir) + " && exec " + cursorShellJoin(args)
	var cleanupLaunchScript func()
	if len(env) > 0 || len(unset) > 0 || scrub != nil {
		var err error
		// CommandWithFinalEnv (not CommandWithEnv): the unset list must run
		// inside the launch script, after shell startup files, so an ambient
		// credential cannot be restored by a profile and cannot survive from
		// the tmux server environment.
		shellCommand, cleanupLaunchScript, err = shelllaunch.CommandWithScopedEnv(args, workingDir, env, unset, scrub)
		if err != nil {
			return fmt.Errorf("failed to prepare Cursor launch environment: %w", err)
		}
	} else {
		cleanupLaunchScript = func() {}
	}
	tmuxArgs := []string{"new-session", "-d", "-s", sessionName}
	tmuxArgs = append(tmuxArgs, tmuxsize.Args()...)
	tmuxArgs = append(tmuxArgs, shellCommand)
	tmuxArgs = tmuxlaunch.WithHistoryLimit(tmuxArgs, tmuxexec.DefaultHistoryLimit)
	if err := runCursorCommand(ctx, nil, "tmux", tmuxArgs...); err != nil {
		cleanupLaunchScript()
		return fmt.Errorf("failed to start Cursor interactive session %q: %w", sessionName, err)
	}
	_ = runCursorCommand(ctx, nil, "tmux", "set-option", "-t", sessionName, "remain-on-exit", "on")
	if err := runCursorCommand(ctx, nil, "tmux", "set-option", "-t", sessionName, "history-limit", tmuxexec.DefaultHistoryLimit); err != nil {
		return fmt.Errorf("failed to configure Cursor tmux history for session %q: %w", sessionName, err)
	}
	// Pin the window size to manual so the detached session keeps the size we
	// launched at instead of collapsing to default-size (80x24), which reflows
	// the TUI into half-width and makes the captured pane unreadable.
	_ = runCursorCommand(ctx, nil, "tmux", "set-option", "-t", sessionName, "window-size", "manual")
	_ = runCursorCommand(ctx, nil, "tmux", "set-option", "-t", sessionName, "focus-events", "on")
	return nil
}

func waitForCursorPrompt(ctx context.Context, sessionName string, streamChan chan<- llmtypes.StreamChunk, streamTerminalScreen bool) error {
	return waitForCursorPromptWithBootBanner(ctx, sessionName, streamChan, streamTerminalScreen, cursorBootBannerPromptGrace, false)
}

func waitForCursorLiveInputPrompt(ctx context.Context, sessionName string) error {
	return waitForCursorPromptWithBootBanner(ctx, sessionName, nil, false, cursorBootBannerPromptGrace, true)
}

func waitForCursorRestoredPrompt(ctx context.Context, sessionName string, streamChan chan<- llmtypes.StreamChunk, streamTerminalScreen bool) error {
	return waitForCursorPromptWithBootBanner(ctx, sessionName, streamChan, streamTerminalScreen, cursorRestoredBootBannerPromptGrace, false)
}

func waitForCursorPromptWithBootBanner(ctx context.Context, sessionName string, streamChan chan<- llmtypes.StreamChunk, streamTerminalScreen bool, bootBannerGrace time.Duration, allowBusyFollowup bool) error {
	deadline, cancel := context.WithTimeout(ctx, cursorInteractivePromptWait())
	defer cancel()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var trustSubmitted bool
	var trustSubmittedAt time.Time
	var lastMCPServerApprovalAt time.Time
	var lastTerminalSnapshot string
	var lastTerminalStreamedAt time.Time
	// Debounce the ready signal: require it to hold across two
	// consecutive captures (~400ms). Single-tick readiness has
	// false-positived on cursor's cold-start banner where the
	// placeholder paints before the input field is interactive.
	var consecutiveReadyTicks int
	var bootBannerReadySince time.Time
	for {
		select {
		case <-deadline.Done():
			captured, _ := captureCursorPane(context.Background(), sessionName)
			if hasCursorAuthPrompt(captured) {
				return cursorAuthPromptError(captured)
			}
			if strings.TrimSpace(captured) != "" {
				return fmt.Errorf("timed out waiting for Cursor Agent CLI prompt; latest pane:\n%s", captured)
			}
			return fmt.Errorf("timed out waiting for Cursor Agent CLI prompt")
		case <-ticker.C:
			captured, err := captureCursorPane(deadline, sessionName)
			if err != nil {
				if isCursorTmuxSessionLostError(err) {
					return fmt.Errorf("Cursor Agent CLI tmux session ended while waiting for prompt: %w", err)
				}
				consecutiveReadyTicks = 0
				continue
			}
			if streamChan != nil && streamTerminalScreen {
				if time.Since(lastTerminalStreamedAt) >= time.Second && streamCursorTerminalSnapshot(ctx, sessionName, streamChan, &lastTerminalSnapshot) {
					lastTerminalStreamedAt = time.Now()
				}
			}
			visible := cursorVisiblePaneText(captured)
			if hasCursorAuthPrompt(visible) {
				return cursorAuthPromptError(captured)
			}
			if hasCursorTrustPrompt(visible) && !trustSubmitted {
				if err := sendCursorReadinessControl(deadline, sessionName, allowBusyFollowup, hasCursorTrustPrompt, cursorTrustPromptResponse(visible)); err != nil {
					continue
				}
				trustSubmitted = true
				trustSubmittedAt = time.Now()
				consecutiveReadyTicks = 0
				bootBannerReadySince = time.Time{}
				continue
			}
			if hasCursorMCPServerApprovalPrompt(visible) {
				if lastMCPServerApprovalAt.IsZero() || time.Since(lastMCPServerApprovalAt) >= time.Second {
					if err := sendCursorReadinessControl(deadline, sessionName, allowBusyFollowup, hasCursorMCPServerApprovalPrompt, cursorMCPServerApprovalResponse(visible)); err == nil {
						lastMCPServerApprovalAt = time.Now()
					}
				}
				consecutiveReadyTicks = 0
				bootBannerReadySince = time.Time{}
				continue
			}
			// Do not trust Cursor's underlying composer while the trust action is
			// still settling. The transition text can remain painted as stale TUI
			// chrome even after completion, so disappearance alone is not a usable
			// signal; the bounded grace plus the existing stable-ready debounce is.
			if trustSubmitted && !cursorWorkspaceTrustReadyGraceElapsed(trustSubmittedAt, time.Now()) {
				consecutiveReadyTicks = 0
				bootBannerReadySince = time.Time{}
				continue
			}
			if allowBusyFollowup && hasCursorBusyFollowupPrompt(visible) {
				return nil
			}
			cleaned := strings.ToLower(stripCursorANSI(visible))
			if cursorBootBannerAcceptableAfterGrace(cleaned) {
				consecutiveReadyTicks = 0
				if bootBannerReadySince.IsZero() {
					bootBannerReadySince = time.Now()
					continue
				}
				if time.Since(bootBannerReadySince) >= bootBannerGrace {
					return nil
				}
				continue
			}
			bootBannerReadySince = time.Time{}
			if hasCursorReadyPrompt(captured) {
				consecutiveReadyTicks++
				if consecutiveReadyTicks >= 2 {
					return nil
				}
				continue
			}
			consecutiveReadyTicks = 0
		}
	}
}

func cursorWorkspaceTrustReadyGraceElapsed(submittedAt, now time.Time) bool {
	return !submittedAt.IsZero() && !now.Before(submittedAt.Add(cursorWorkspaceTrustReadyGrace))
}

func sendCursorReadinessControl(ctx context.Context, sessionName string, liveInput bool, visible func(string) bool, key string) error {
	if liveInput {
		_, err := sendCursorControlIfVisible(ctx, sessionName, "cursor-readiness-control", visible, key)
		return err
	}
	// Initial-prompt readiness already runs inside the broker transaction.
	return runCursorCommand(ctx, nil, "tmux", "send-keys", "-t", sessionName, key)
}

func sendCursorInputToTmux(ctx context.Context, sessionName, message string) error {
	return sendCursorInputToTmuxWithReadiness(ctx, sessionName, message, false, false)
}

func sendCursorLiveInputToTmux(ctx context.Context, sessionName, message string) error {
	// Waiting inside the input broker prevents the approval handler from
	// clearing the very modal we are waiting on. Recheck after acquiring the
	// broker, and retry only when no input has been typed yet.
	ctx, cancel := context.WithTimeout(ctx, cursorInteractivePromptWait())
	defer cancel()
	if err := tmuxinput.WaitUntilReady(ctx, sessionName); err != nil {
		return err
	}
	for {
		if err := waitForCursorLiveInputPrompt(ctx, sessionName); err != nil {
			return err
		}
		err := sendCursorInputToTmuxWithReadiness(ctx, sessionName, message, false, true)
		if !errors.Is(err, errCursorComposerChanged) {
			return err
		}
	}
}

func sendCursorInitialPromptToTmux(ctx context.Context, sessionName, message string) error {
	return sendCursorInputToTmuxWithReadiness(ctx, sessionName, message, true, false)
}

func cursorConfirmInitialSubmitAfterPaneError(ctx context.Context, session *cursorInteractiveSession, message string, since time.Time, baseline map[string]struct{}, submitErr error) error {
	if submitErr == nil {
		return nil
	}
	ack, err := pollCursorDurableAck(ctx, message, since, baseline, cursorDurableAckBudget(), cursorDurableAckPoll{
		resolve: func() string { return resolveCursorStoreNoMu(session) },
	})
	if err == nil && ack.Outcome == CursorDurableAckConfirmed {
		return nil
	}
	if err == nil && ack.Outcome == CursorDurableAckUnflushed {
		return &CursorInputUnflushedError{OwnerSessionID: session.ownerSessionID, Latency: ack.Latency}
	}
	return fmt.Errorf("%w; initial Cursor delivery unconfirmed by store: %w", submitErr, err)
}

func sendCursorInputToTmuxWithReadiness(ctx context.Context, sessionName, message string, initialPrompt, liveInput bool) error {
	_, err := tmuxinput.Default.Do(ctx, tmuxinput.Request{
		SessionID:       sessionName,
		Source:          "cursor-cli",
		BypassReadiness: initialPrompt,
	}, func(ctx context.Context) error {
		// `tmuxinput` serializes keystrokes but intentionally does not wait for
		// Cursor's composer. The launch-time readiness check is not sufficient
		// for the first prompt: Cursor can open its MCP approval/transition UI
		// after that check while the bridge is connecting. Re-check immediately
		// before every delivery, including a cold session, so typed input cannot
		// be sent into an overlay and silently dropped.
		var readyErr error
		if liveInput {
			captured, err := captureCursorPane(ctx, sessionName)
			if err != nil {
				return err
			}
			if !hasCursorReadyPrompt(captured) && !hasCursorBusyFollowupPrompt(captured) &&
				!(cursorBootBannerAcceptableAfterGrace(strings.ToLower(stripCursorANSI(cursorVisiblePaneText(captured)))) &&
					!hasCursorAuthPrompt(captured) && !hasCursorTrustPrompt(captured) &&
					!hasCursorWebAccessApprovalPrompt(captured) && !hasCursorMCPToolApprovalPrompt(captured) &&
					!hasCursorMCPServerApprovalPrompt(captured) && !hasCursorModeSwitchPrompt(captured) &&
					!hasCursorQueuedFollowupsSendPrompt(captured)) {
				return errCursorComposerChanged
			}
		} else {
			readyErr = waitForCursorPrompt(ctx, sessionName, nil, false)
		}
		if readyErr != nil {
			if initialPrompt {
				return fmt.Errorf("Cursor was not ready to accept initial input: %w", readyErr)
			}
			return fmt.Errorf("Cursor was not ready to accept follow-up input: %w", readyErr)
		}
		err := sendCursorInputToTmuxUnserializedWithMode(ctx, sessionName, message, initialPrompt, liveInput)
		if initialPrompt && isCursorMissingDraftError(err) {
			// Cursor can paint its cold-start composer just before it begins
			// accepting keystrokes. The first visible-draft check correctly
			// rejects that dropped input; do not throw the session away yet.
			// Re-confirm the idle composer and make one bounded retry. Retrying
			// is safe here because the first attempt never appeared in the
			// editor, therefore it could not have been submitted to Cursor.
			if readyErr := waitForCursorPrompt(ctx, sessionName, nil, false); readyErr != nil {
				return fmt.Errorf("Cursor initial prompt was not ready after dropped input: %w", readyErr)
			}
			err = sendCursorInputToTmuxUnserializedWithMode(ctx, sessionName, message, initialPrompt, liveInput)
		}
		return err
	})
	return err
}

func isCursorMissingDraftError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "typed Cursor input did not appear in the prompt before submit")
}

func sendCursorInputToTmuxUnserialized(ctx context.Context, sessionName, message string, preferAtomic bool) error {
	return sendCursorInputToTmuxUnserializedWithMode(ctx, sessionName, message, preferAtomic, false)
}

func sendCursorInputToTmuxUnserializedWithMode(ctx context.Context, sessionName, message string, preferAtomic, liveInput bool) error {
	// [LATENCY_DEBUG] timing — see codexcli's equivalent function for why this
	// matters: how long delivery + submit-confirmation actually took, broken
	// out from the model's own thinking time.
	start := time.Now()
	message = strings.TrimRight(message, "\r\n")
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("Cursor interactive input is empty")
	}
	err := typeCursorInputToTmuxWithDeliveryMode(ctx, sessionName, message, preferAtomic, liveInput)
	log.Printf("[LATENCY_DEBUG] cursor tmux delivery | session=%s confirmed=%dms err=%v",
		sessionName, time.Since(start).Milliseconds(), err)
	return err
}

func typeCursorInputToTmux(ctx context.Context, sessionName, message string) error {
	return typeCursorInputToTmuxWithMode(ctx, sessionName, message, false)
}

func typeCursorInputToTmuxWithMode(ctx context.Context, sessionName, message string, preferAtomic bool) error {
	return typeCursorInputToTmuxWithDeliveryMode(ctx, sessionName, message, preferAtomic, false)
}

func typeCursorInputToTmuxWithDeliveryMode(ctx context.Context, sessionName, message string, preferAtomic, liveInput bool) error {
	transport := "literal"
	writeDraft := writeCursorVisibleDraftToTmux
	// Live input goes to a busy Cursor whose editor often does not redraw the typed draft where the scraper reads it;
	// the literal path then never pressed Enter and left the text in the editor, where it went out with the next
	// message and the person saw "Not delivered" for a message Cursor did answer. A paste is one
	// acknowledged tmux operation and needs no visual receipt.
	if liveInput || cursorInputNeedsAtomicPaste(message, preferAtomic) {
		transport = "atomic-paste"
		writeDraft = pasteCursorDraftToTmux
	}
	lineCount := strings.Count(message, "\n") + 1
	runeCount := utf8.RuneCountInString(message)
	log.Printf("[LATENCY_DEBUG] cursor tmux input start | session=%s transport=%s runes=%d lines=%d",
		sessionName, transport, runeCount, lineCount)
	if err := writeDraft(ctx, sessionName, message); err != nil {
		return fmt.Errorf("failed to type input into Cursor interactive session: %w", err)
	}
	if transport == "atomic-paste" && cursorPasteSubmitDelay > 0 {
		// Cursor aggregates keys that arrive right after a bracketed paste into
		// that paste: Enter 137 ms later became two pasted newlines and the
		// message never sent (Cursor log, 2026-10-23 "paste-event aggregated").
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(cursorPasteSubmitDelay):
		}
	}
	// A live-input paste is acknowledged by tmux; waiting for a visual receipt on a busy Cursor only delays it.
	draftVisible := liveInput || waitForCursorInputDraftVisibleWithMode(ctx, sessionName, message, 5*time.Second, liveInput)
	if !draftVisible {
		// Say what the screen was doing (never its text), so a stall explains itself in the log.
		if captured, capErr := captureCursorPane(context.WithoutCancel(ctx), sessionName); capErr == nil {
			log.Printf("[CURSOR_DRAFT_MISS] session=%s transport=%s live_input=%t runes=%d lines=%d since_last_accepted_ms=%d pane=%s",
				sessionName, transport, liveInput, runeCount, lineCount, cursorSinceLastAcceptedMS(sessionName), cursorPaneStateSummary(captured))
		} else {
			log.Printf("[CURSOR_DRAFT_MISS] session=%s transport=%s live_input=%t runes=%d lines=%d since_last_accepted_ms=%d pane=unreadable(%v)",
				sessionName, transport, liveInput, runeCount, lineCount, cursorSinceLastAcceptedMS(sessionName), capErr)
		}
	}
	if !draftVisible && transport != "atomic-paste" {
		return fmt.Errorf("typed Cursor input did not appear in the prompt before submit (transport=%s runes=%d lines=%d)", transport, runeCount, lineCount)
	}
	if !draftVisible {
		// Deep restored panes can retain enough scrollback that the active-editor
		// scraper cannot identify the pasted draft, even though tmux accepted the
		// atomic buffer and Cursor displays it. Atomic paste is a single,
		// acknowledged tmux operation, so a missing visual receipt is not a reason
		// to suppress Enter. Literal multi-command typing keeps the strict guard.
		log.Printf("[LATENCY_DEBUG] cursor atomic paste has no visual draft receipt; submitting acknowledged buffer | session=%s runes=%d lines=%d",
			sessionName, runeCount, lineCount)
	}
	args := append([]string{"send-keys", "-t", sessionName}, cursorSubmitKeys()...)
	if err := runCursorCommand(ctx, nil, "tmux", args...); err != nil {
		return fmt.Errorf("failed to submit typed input to Cursor interactive session: %w", err)
	}
	if err := ensureCursorInputSubmittedWithMode(ctx, sessionName, message, liveInput); err != nil {
		return err
	}
	noteCursorInputAccepted(sessionName)
	return nil
}

var (
	cursorLastAcceptedMu sync.Mutex
	cursorLastAccepted   = map[string]time.Time{}
)

func noteCursorInputAccepted(sessionName string) {
	cursorLastAcceptedMu.Lock()
	defer cursorLastAcceptedMu.Unlock()
	if len(cursorLastAccepted) > 512 {
		cutoff := time.Now().Add(-24 * time.Hour)
		for name, at := range cursorLastAccepted {
			if at.Before(cutoff) {
				delete(cursorLastAccepted, name)
			}
		}
	}
	cursorLastAccepted[sessionName] = time.Now()
}

func cursorSinceLastAcceptedMS(sessionName string) int64 {
	cursorLastAcceptedMu.Lock()
	defer cursorLastAcceptedMu.Unlock()
	at, ok := cursorLastAccepted[sessionName]
	if !ok {
		return -1
	}
	return time.Since(at).Milliseconds()
}

var cursorPasteSubmitDelay = 600 * time.Millisecond

func cursorSubmitKeys() []string {
	return []string{"C-m", "C-m"}
}

func cursorInputNeedsAtomicPaste(message string, preferAtomic bool) bool {
	return preferAtomic || utf8.RuneCountInString(message) >= cursorAtomicPasteMinRunes ||
		strings.Count(message, "\n")+1 >= cursorAtomicPasteMinLines
}

func pasteCursorDraftToTmux(ctx context.Context, sessionName, message string) error {
	bufferName := "mlp-cursor-input-" + cursorRandomHex(6)
	if err := runCursorCommand(ctx, strings.NewReader(message), "tmux", "load-buffer", "-b", bufferName, "-"); err != nil {
		return fmt.Errorf("failed to load Cursor input into tmux buffer: %w", err)
	}
	defer func() {
		_ = runCursorCommand(context.Background(), nil, "tmux", "delete-buffer", "-b", bufferName)
	}()
	if err := runCursorCommand(ctx, nil, "tmux", "paste-buffer", "-d", "-p", "-r", "-b", bufferName, "-t", sessionName); err != nil {
		return fmt.Errorf("failed to paste Cursor input into interactive session: %w", err)
	}
	return nil
}

func writeCursorVisibleDraftToTmux(ctx context.Context, sessionName, message string) error {
	message = strings.ReplaceAll(message, "\r\n", "\n")
	message = strings.ReplaceAll(message, "\r", "\n")
	lines := strings.Split(message, "\n")
	for lineIndex, line := range lines {
		runes := []rune(line)
		for len(runes) > 0 {
			chunkSize := min(len(runes), cursorVisibleInputChunkRunes)
			chunk := string(runes[:chunkSize])
			if err := runCursorCommand(ctx, nil, "tmux", "send-keys", "-t", sessionName, "-l", "--", chunk); err != nil {
				return err
			}
			runes = runes[chunkSize:]
		}
		if lineIndex < len(lines)-1 {
			if err := runCursorCommand(ctx, nil, "tmux", "send-keys", "-t", sessionName, "C-j"); err != nil {
				return err
			}
		}
	}
	return nil
}

func ensureCursorInputSubmitted(ctx context.Context, sessionName, message string) error {
	return ensureCursorInputSubmittedWithMode(ctx, sessionName, message, false)
}

func ensureCursorInputSubmittedWithMode(ctx context.Context, sessionName, message string, liveInput bool) error {
	// Cursor can finish painting a ready-looking composer before it is actually
	// ready to consume Enter, especially after a cold tmux launch or overlay
	// dismissal. Keep the same draft in place and retry submit over a bounded
	// window; do not re-type the prompt, which would duplicate user input.
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// Verify-then-recover: return as soon as the draft leaves the input field
	// (checked immediately, then every 50ms — the old single 150ms-delayed probe
	// added a fixed 150ms to every send). If the draft is still sitting there
	// after a grace period (the follow-ups menu swallowed the first Enter), send
	// a few recovery Enters and keep verifying until it clears or the deadline
	// hits.
	const recoveryGrace = 400 * time.Millisecond
	const maxSubmitRetries = 3
	const submitRetryInterval = time.Second
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	started := time.Now()
	submitRetries := 0
	var lastSubmitRetryAt time.Time
	var lastModeSwitchRejectAt time.Time
	for {
		captured, err := captureCursorPane(deadline, sessionName)
		if err == nil {
			if !cursorPaneShowsPromptDraftWithMode(captured, message, liveInput) {
				return nil
			}
			if hasCursorModeSwitchPrompt(captured) {
				// Reject at most once a second (matches the sibling
				// response-waiting loop's own re-check cadence) rather than on
				// every 50ms tick, since the reject keystroke itself needs a
				// moment to land and the prompt to clear.
				if lastModeSwitchRejectAt.IsZero() || time.Since(lastModeSwitchRejectAt) >= time.Second {
					lastModeSwitchRejectAt = time.Now()
					if _, err := sendCursorControlIfVisible(deadline, sessionName, "cursor-mode-switch-reject-submit", hasCursorModeSwitchPrompt, "n"); err != nil {
						return fmt.Errorf("failed to reject Cursor mode-switch prompt during submit: %w", err)
					}
					// The original message is still pending, not yet submitted
					// as a real chat turn — give the post-reject recovery-Enter
					// a full fresh grace window instead of racing whatever was
					// left of the original one.
					started = time.Now()
					submitRetries = 0
					lastSubmitRetryAt = time.Time{}
				}
			} else if time.Since(started) >= recoveryGrace && submitRetries < maxSubmitRetries && (lastSubmitRetryAt.IsZero() || time.Since(lastSubmitRetryAt) >= submitRetryInterval) {
				submitRetries++
				lastSubmitRetryAt = time.Now()
				if err := runCursorCommand(deadline, nil, "tmux", "send-keys", "-t", sessionName, "C-m"); err != nil {
					return fmt.Errorf("failed to retry Cursor input submission: %w", err)
				}
			}
		}
		select {
		case <-deadline.Done():
			return fmt.Errorf("Cursor input remained in the prompt after submit retry")
		case <-ticker.C:
		}
	}
}

func sendCursorControlIfVisible(
	ctx context.Context,
	sessionName string,
	source string,
	promptVisible func(string) bool,
	keys ...string,
) (bool, error) {
	handled := false
	_, err := tmuxinput.Default.Do(ctx, tmuxinput.Request{
		SessionID: sessionName,
		Source:    source,
		Priority:  tmuxinput.PriorityInterrupt,
	}, func(ctx context.Context) error {
		captured, err := captureCursorPane(ctx, sessionName)
		if err != nil {
			return err
		}
		if !promptVisible(captured) {
			return nil
		}
		if err := runCursorCommand(ctx, nil, "tmux", append([]string{"send-keys", "-t", sessionName}, keys...)...); err != nil {
			return err
		}
		handled = true
		return nil
	})
	return handled, err
}

func waitForCursorInputDraftVisible(ctx context.Context, sessionName, message string, timeout time.Duration) bool {
	return waitForCursorInputDraftVisibleWithMode(ctx, sessionName, message, timeout, false)
}

func waitForCursorInputDraftVisibleWithMode(ctx context.Context, sessionName, message string, timeout time.Duration, liveInput bool) bool {
	if strings.TrimSpace(message) == "" {
		return false
	}
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		captured, err := captureCursorPane(deadline, sessionName)
		if err == nil && cursorPaneShowsPromptDraftWithMode(captured, message, liveInput) {
			return true
		}
		select {
		case <-deadline.Done():
			return false
		case <-ticker.C:
		}
	}
}

func waitForCursorInteractiveResponseWithStore(ctx context.Context, sessionName, baseline, prompt string, historicalAssistantTexts []string, streamChan chan<- llmtypes.StreamChunk, autoApproveWebSearch bool, streamTerminalScreen bool, storeAnswer func(query string) (string, cursorTrailState)) (string, string, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	waitStartedAt := time.Now()
	firstActivityTimeout := cursorInteractiveFirstActivityTimeout()
	stalePaneBackstop := cursorInteractiveStalePaneBackstop()
	var sawActivity bool
	var idleSince time.Time
	var readyWithoutContentSince time.Time
	var submitRetryCount int
	var lastSubmitRetryAt time.Time
	var lastCaptured string
	var lastTerminalSnapshot string
	var lastTerminalStreamedAt time.Time
	controls := cursorRuntimeControls{autoApproveWebSearch: autoApproveWebSearch}
	var finalAnswerRecoveryCount int
	var heldSubmitNudges int
	// Stale-pane backstop tracking: the raw capture from the previous tick and
	// the time it last changed. This is tracked at the top of every tick,
	// independent of all the branch logic below, so a prompt-detection bug that
	// keeps the loop in a "not ready" branch can never suppress it.
	var backstopPrevCapture string
	var paneUnchangedSince time.Time
	for {
		select {
		case <-ctx.Done():
			captured, _ := captureCursorPane(context.Background(), sessionName)
			return captured, "", ctx.Err()
		case <-ticker.C:
			captured, err := captureCursorPane(ctx, sessionName)
			if err != nil {
				return "", "", err
			}
			delta := cursorCapturedAfterBaseline(captured, baseline)
			if tmuxcontrol.ConsumeForceComplete(sessionName) {
				return captured, "", tmuxcontrol.ErrForceComplete
			}
			// Stale-pane backstop. Independent of hasCursorReadyPrompt and every
			// branch below: if the pane has produced activity and then frozen
			// (byte-identical) for longer than the backstop, the turn is over but
			// completion detection failed to recognize it (e.g. a stale status
			// line or leftover spinner frame holding the pane "not ready"). Extract
			// whatever response is present and return it rather than hang forever.
			// Gated on sawActivity so a never-delivered prompt (pane frozen at
			// baseline) falls through to the first-activity timeout instead.
			if captured != backstopPrevCapture {
				backstopPrevCapture = captured
				paneUnchangedSince = time.Now()
			} else if sawActivity && stalePaneBackstop > 0 && !paneUnchangedSince.IsZero() &&
				time.Since(paneUnchangedSince) >= stalePaneBackstop {
				content := parseCursorInteractiveResponse(captured, baseline, prompt, historicalAssistantTexts)
				if strings.TrimSpace(content) == "" {
					content = forcedCursorInteractiveResponse(captured, baseline, prompt, historicalAssistantTexts)
				}
				if strings.TrimSpace(content) != "" {
					return captured, "", nil
				}
				return captured, "", fmt.Errorf("Cursor Agent CLI pane went unchanged for %s after activity but no ready prompt or visible assistant output was detected; latest pane:\n%s", stalePaneBackstop, captured)
			}
			if streamChan != nil && streamTerminalScreen {
				if time.Since(lastTerminalStreamedAt) >= time.Second && streamCursorTerminalSnapshot(ctx, sessionName, streamChan, &lastTerminalSnapshot) {
					lastTerminalStreamedAt = time.Now()
				}
			}
			if controls.handle(ctx, sessionName, captured) {
				sawActivity = true
				idleSince = time.Time{}
				lastCaptured = captured
				continue
			}
			// Reset idle only when we have activity AND we're not yet
			// at the ready prompt. Cursor's TUI leaves stale status
			// lines ("Running...", "Thinking...") visible for several
			// seconds after the → prompt reappears; those used to
			// keep restarting the idle timer and added 5-10s of
			// avoidable wait to every turn. hasCursorReadyPrompt
			// already handles the "→ visible + stale status" case
			// correctly (returns true), so once we're ready we let
			// the stable-window check drive completion.
			if !hasCursorReadyPrompt(captured) && hasCursorActivity(captured) {
				sawActivity = true
				idleSince = time.Time{}
				lastCaptured = captured
				continue
			}
			if strings.TrimSpace(delta) != "" {
				sawActivity = true
			}
			if !sawActivity {
				// The CLI has shown nothing since the prompt was submitted.
				// Every completion/failsafe path below requires sawActivity, so
				// without this cap a never-delivered prompt spins here forever
				// (the call context has no deadline by default). Fail cleanly so
				// the step surfaces an error instead of hanging.
				if firstActivityTimeout > 0 && time.Since(waitStartedAt) >= firstActivityTimeout {
					captured, _ := captureCursorPane(context.Background(), sessionName)
					return captured, "", fmt.Errorf("Cursor Agent CLI produced no activity within %s of submitting the prompt — the input was likely not delivered to the tmux pane; latest pane:\n%s", firstActivityTimeout, captured)
				}
				idleSince = time.Time{}
				lastCaptured = captured
				continue
			}
			if !hasCursorReadyPrompt(captured) {
				idleSince = time.Time{}
				lastCaptured = captured
				continue
			}
			if captured != lastCaptured {
				lastCaptured = captured
				idleSince = time.Now()
				continue
			}
			if idleSince.IsZero() {
				idleSince = time.Now()
				continue
			}
			if time.Since(idleSince) >= cursorInteractiveStableWindow {
				content := parseCursorInteractiveResponse(captured, baseline, prompt, historicalAssistantTexts)
				storeAwaitingReply := false
				if storeAnswer != nil {
					// P0: the store's trail for this exact query decides. The
					// pane only says the composer is back. A recovery prompt
					// typed while Cursor was still working may never become its
					// own stored turn, so the original query is checked too.
					queries := []string{prompt}
					if finalAnswerRecoveryCount > 0 {
						queries = []string{cursorFinalAnswerRecoveryPrompt, prompt}
					}
					state := cursorTrailNotTaken
					for _, query := range queries {
						answer, st := storeAnswer(query)
						if st == cursorTrailFinished {
							return captured, answer, nil
						}
						if state == cursorTrailNotTaken {
							state = st
						}
					}
					switch {
					case state == cursorTrailPendingTool:
						// A tool call has no result yet: the idle-looking pane is
						// a false idle (slow tool). Keep waiting; never recover.
						idleSince = time.Time{}
						readyWithoutContentSince = time.Time{}
						continue
					case state == cursorTrailAwaitingReply:
						// The last tool returned but no reply followed. Pane text
						// here is pre-tool narration, not an answer.
						content = ""
						storeAwaitingReply = true
					case state == cursorTrailNotTaken && time.Since(waitStartedAt) < cursorStoreIntakeWait:
						// Not taken in yet. If our prompt is still sitting in the
						// composer, its Enter was swallowed (e.g. by the workspace
						// trust screen): resubmit it. Never type a recovery prompt
						// here -- it would be appended to the unsent draft.
						if cursorPaneShowsPromptDraft(captured, prompt) && submitRetryCount < 3 && time.Since(lastSubmitRetryAt) >= 2*time.Second {
							draftVisible := func(current string) bool {
								return cursorPaneShowsPromptDraft(current, prompt)
							}
							if handled, _ := sendCursorControlIfVisible(ctx, sessionName, "cursor-submit-retry", draftVisible, "C-m"); handled {
								submitRetryCount++
								lastSubmitRetryAt = time.Now()
							}
						} else if time.Since(waitStartedAt) >= cursorStoreIntakeNudgeAfter && heldSubmitNudges < 2 && time.Since(lastSubmitRetryAt) >= cursorStoreIntakeNudgeAfter {
							// Cursor 2026.09 can show a submitted prompt in its history
							// yet hold it without starting a turn (the store never
							// records it); the next Enter releases it. The composer is
							// idle and empty here, so Enter adds no text of its own.
							log.Printf("[LATENCY_DEBUG] cursor prompt not taken in by store after %s; nudging held submit | session=%s", time.Since(waitStartedAt).Round(time.Second), sessionName)
							if err := runCursorCommand(ctx, nil, "tmux", "send-keys", "-t", sessionName, "C-m"); err == nil {
								heldSubmitNudges++
								lastSubmitRetryAt = time.Now()
							}
						}
						idleSince = time.Time{}
						readyWithoutContentSince = time.Time{}
						continue
					}
					// Taken in but still streaming (user row only, or assistant
					// prose not yet committed): only a finished trail completes.
					if state == cursorTrailTaken {
						idleSince = time.Time{}
						continue
					}
				}
				if strings.TrimSpace(content) == "" {
					if readyWithoutContentSince.IsZero() {
						readyWithoutContentSince = time.Now()
						continue
					}
					// Cursor can finish the last MCP call and return directly to its
					// composer without emitting a post-tool assistant message. Treating
					// pre-tool narration ("I'll run ...") as the answer produces dirty
					// workflow completion notifications. Once per GenerateContent call,
					// recover by asking the same native session for the missing final
					// answer. The original baseline remains valid: the new User: boundary
					// causes extraction to retain only this recovery turn's response.
					// A spent plan returns to the prompt with only the limit
					// notice: fail as quota exhaustion now, never as empty output.
					if IsCursorUsageLimitText(delta) {
						return captured, "", NewCursorUsageLimitError("", delta, time.Now())
					}
					if finalAnswerRecoveryCount == 0 && (storeAwaitingReply || cursorPaneContainsToolActivity(delta)) &&
						time.Since(readyWithoutContentSince) >= cursorFinalAnswerRecoveryDelay {
						if err := sendCursorInitialPromptToTmux(ctx, sessionName, cursorFinalAnswerRecoveryPrompt); err == nil {
							finalAnswerRecoveryCount++
							readyWithoutContentSince = time.Time{}
							idleSince = time.Time{}
							lastCaptured = ""
							continue
						}
					}
					if cursorPaneShowsPromptDraft(captured, prompt) && submitRetryCount < 3 && time.Since(lastSubmitRetryAt) >= time.Second {
						draftVisible := func(current string) bool {
							return cursorPaneShowsPromptDraft(current, prompt)
						}
						if handled, _ := sendCursorControlIfVisible(ctx, sessionName, "cursor-submit-retry", draftVisible, "C-m"); handled {
							submitRetryCount++
							lastSubmitRetryAt = time.Now()
							idleSince = time.Time{}
							continue
						}
					}
					if time.Since(readyWithoutContentSince) >= 15*time.Second {
						return captured, "", fmt.Errorf("Cursor Agent CLI returned to the prompt without visible assistant output; latest pane:\n%s", captured)
					}
					continue
				}
				return captured, "", nil
			}
		}
	}
}

func parseCursorInteractiveResponse(captured, baseline, echoedUserPrompt string, historicalAssistantTexts []string) string {
	delta := cursorCapturedAfterBaseline(captured, baseline)
	text := extractCursorVisibleAssistantText(delta)
	text = stripCursorEchoedUserPrompt(text, echoedUserPrompt)
	text = stripCursorHistoricalAssistantText(text, historicalAssistantTexts)
	return strings.TrimSpace(text)
}

func forcedCursorInteractiveResponse(captured, baseline, echoedUserPrompt string, historicalAssistantTexts []string) string {
	delta := cursorCapturedAfterBaseline(captured, baseline)
	text := extractCursorVisibleAssistantText(delta)
	text = stripCursorEchoedUserPrompt(text, echoedUserPrompt)
	text = stripCursorHistoricalAssistantText(text, historicalAssistantTexts)
	return strings.TrimSpace(text)
}

func extractCursorVisibleAssistantText(delta string) string {
	lines := strings.Split(stripCursorANSI(delta), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		roleLine := normalizeCursorPaneLineWithoutAssistantLabel(line)
		trimmed := normalizeCursorAssistantTurnLabel(roleLine)
		if isCursorPromptBoundaryLine(trimmed) {
			break
		}
		// "User: …" marks the start of a user turn. Everything previously
		// collected is from an older assistant turn and must be discarded —
		// otherwise a multi-turn pane (where baseline-diff falls back to
		// line-prefix mode and leaves prior turns in the delta) leaks the
		// stale reply into the new turn's extracted text.
		if isCursorUserTurnHeader(roleLine) {
			out = out[:0]
			continue
		}
		if strings.HasPrefix(roleLine, "FINAL_ANSWER_RECOVERY:") {
			out = out[:0]
			continue
		}
		if assistantLine, ok := stripCursorAssistantTurnHeader(roleLine); ok {
			out = out[:0]
			trimmed = assistantLine
			if trimmed == "" {
				continue
			}
		}
		if trimmed == "" {
			// Preserve blank lines as paragraph-break markers (collapse runs),
			// but never start the response with a blank.
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			continue
		}
		// A line beginning with a Braille spinner glyph is the CLI's live/stale
		// generation status (e.g. "⣾ Generating…"); never assistant content, so
		// skip it — otherwise the loop can return "Generating…" as the answer.
		if cursorLineStartsWithSpinner(strings.TrimSpace(trimmed)) {
			continue
		}
		if isCursorToolStatusLine(trimmed) {
			// A tool call is a semantic boundary: narration collected before it
			// is progress, never the final answer. Resetting here also makes an
			// idle prompt with no post-tool prose extract as empty, allowing the
			// response loop to request the missing final answer instead of
			// returning the progress trail.
			out = out[:0]
			continue
		}
		if isCursorTUILine(trimmed) || isCursorBoxDrawingLine(trimmed) {
			continue
		}
		out = append(out, trimmed)
	}
	// Drop trailing blank markers introduced by the input-box gap.
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	// Blank lines between non-blank lines are preserved as "" entries, so the
	// joined output uses "\n\n" between paragraphs — CommonMark renders that as
	// a real paragraph break, while a single "\n" inside a paragraph (a tmux
	// hard-wrap) is treated as a soft break and rendered as a space.
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func isCursorUserTurnHeader(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "User:") && !strings.HasPrefix(trimmed, "user:") {
		return false
	}
	// Require the colon to be followed by whitespace OR end-of-line — distinguishes
	// Cursor's "User: hi" header from prose like "User:enter your username".
	if len(trimmed) == len("User:") {
		return true
	}
	next := trimmed[len("User:")]
	return next == ' ' || next == '\t'
}

func normalizeCursorPaneLineWithoutAssistantLabel(line string) string {
	line = strings.TrimSpace(stripCursorANSI(line))
	line = strings.TrimPrefix(line, "│")
	line = strings.TrimSuffix(line, "│")
	line = strings.TrimSpace(line)
	line = strings.TrimSpace(strings.TrimPrefix(line, "• "))
	return line
}

func normalizeCursorAssistantTurnLabel(line string) string {
	// Cursor labels each assistant turn with a literal "Assistant:" header. Strip
	// it so the kept response reads as plain prose (and matches what the user
	// sees in the chat panel for Claude/Gemini, which have no such label).
	line = strings.TrimSpace(strings.TrimPrefix(line, "Assistant:"))
	return line
}

func stripCursorAssistantTurnHeader(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "assistant:") {
		return "", false
	}
	if len(trimmed) == len("Assistant:") {
		return "", true
	}
	next := trimmed[len("Assistant:")]
	if next != ' ' && next != '\t' {
		return "", false
	}
	return strings.TrimSpace(trimmed[len("Assistant:"):]), true
}

var cursorShellEchoSuffix = regexp.MustCompile(`\s\d+(?:\.\d+)?(?:ms|s)\s*$`)

// cursorFoundCountLine matches the tool-result summary Cursor prints after
// grep/glob/list operations: "Found 33 files", "Found 1,024 matches", etc.

var cursorFoundCountLine = regexp.MustCompile(`^found\s+[\d,]+\s+(files?|matches?|results?|symbols?)\b`)

// cursorMultiToolSummaryLine matches Cursor's per-turn tool-activity summary:
//
//	"Read, grepped, globbed 7 files, 4 greps, 2 globs"
//
// The line lists past-tense verbs followed by counts. Always tool narration,
// never response prose.

var cursorMultiToolSummaryLine = regexp.MustCompile(`^(?:read|grepped|globbed|listed|searched)[,\s].*\b\d+\s+(?:files?|greps?|globs?|matches?|results?|symbols?|reads?|lists?|searches?)\b`)

// cursorEarlierHiddenLine matches Cursor's truncation header on long tool
// transcripts, e.g. "… 10 earlier items hidden" or "... 3 earlier tools hidden".

var cursorEarlierHiddenLine = regexp.MustCompile(`^(?:…|\.\.\.)\s*\d+\s+earlier\s+(?:items?|tools?|results?)\b`)

// cursorReadFileLine matches Cursor's per-file read narration. Cursor truncates
// long paths with `...`, so a real prose line "Read the docs" is unaffected — the
// regex requires a path token and either "lines N-M" or a file extension.

var cursorReadFileLine = regexp.MustCompile(`^read\s+(?:\.\.\.|/|~)\S*\s+(?:lines?\s+\d+(?:-\d+)?|.*\.\w{1,8}\b)`)

func isCursorPromptBoundaryLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	// The → arrow is Cursor's input cursor — the most reliable structural
	// boundary. In the delta (after baseline stripping), it only appears at
	// the bottom after the response completes.
	return strings.HasPrefix(trimmed, "→") ||
		trimmed == ">" ||
		trimmed == "›" ||
		trimmed == "❯" ||
		strings.Contains(lower, "ask (shift+tab") ||
		strings.HasPrefix(lower, "type your message") ||
		strings.Contains(lower, "what can i help") ||
		strings.Contains(lower, "add a follow-up") ||
		strings.Contains(lower, "cursor agent") && strings.Contains(lower, "workspace")
}

func isCursorTUILine(line string) bool {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	if trimmed == "" {
		return true
	}
	return strings.Contains(lower, "ctrl+") ||
		strings.Contains(lower, "esc to") ||
		strings.Contains(lower, "press enter") ||
		strings.Contains(lower, "run everything") ||
		strings.Contains(lower, "ask (shift+tab") ||
		strings.HasPrefix(lower, "v20") ||
		strings.Contains(lower, "try composer") ||
		strings.Contains(lower, "composer") && strings.Contains(lower, "fast") ||
		strings.Contains(trimmed, " · ") ||
		strings.HasPrefix(trimmed, "→ ") ||
		strings.Contains(lower, "cursor agent") ||
		strings.Contains(lower, "cursor") && strings.Contains(lower, "model") ||
		strings.Contains(lower, "workspace:") ||
		strings.Contains(lower, "mode:") ||
		strings.Contains(lower, "approval") ||
		strings.Contains(lower, "permission") ||
		strings.Contains(lower, "pasted text") ||
		strings.HasPrefix(lower, "use /") ||
		strings.HasPrefix(lower, "add a follow-up") ||
		strings.HasPrefix(lower, "auto-run") ||
		// Cursor labels each user turn with a literal "User:" header. It is a
		// structural marker, not response prose, so drop it from extraction.
		strings.HasPrefix(lower, "user:")
}

func isCursorToolStatusLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "thinking") ||
		strings.HasPrefix(lower, "working") ||
		strings.HasPrefix(lower, "running") ||
		strings.HasPrefix(lower, "reading") ||
		strings.HasPrefix(lower, "editing") ||
		strings.HasPrefix(lower, "writing") ||
		strings.HasPrefix(lower, "searching") ||
		strings.HasPrefix(lower, "applying") ||
		strings.HasPrefix(lower, "calling ") ||
		strings.HasPrefix(lower, "called ") ||
		strings.HasPrefix(lower, "executing") ||
		strings.HasPrefix(lower, "globbed ") ||
		strings.HasPrefix(lower, "listed ") ||
		strings.HasPrefix(lower, "grepped ") ||
		strings.HasPrefix(lower, "api-bridge ") ||
		strings.Contains(lower, "mcp") && strings.Contains(lower, "tool") ||
		strings.Contains(lower, "shell(") ||
		strings.Contains(lower, `"stdout"`) ||
		strings.Contains(lower, `"stderr"`) ||
		strings.Contains(lower, `"exit_code"`) {
		return true
	}
	// Tool-result summary lines: "Found N files", "Found N matches", …
	if cursorFoundCountLine.MatchString(lower) {
		return true
	}
	// Combined per-turn tool-activity summary, truncation header, per-file read
	// narration — all are tool transcript, never response prose.
	if cursorMultiToolSummaryLine.MatchString(lower) ||
		cursorEarlierHiddenLine.MatchString(lower) ||
		cursorReadFileLine.MatchString(lower) {
		return true
	}
	// Shell-tool command echo: starts with "$ " and ends with a duration
	// suffix like "407ms" or "1.2s" — distinguishes the tool transcript from a
	// markdown code block that happens to begin with "$".
	if strings.HasPrefix(trimmed, "$ ") && cursorShellEchoSuffix.MatchString(lower) {
		return true
	}
	// Truncation marker that closes a tool-output block:
	//   "… truncated (36 more lines) · ctrl+o to expand"
	// (Already filtered by isCursorTUILine's " · " rule in the common case;
	// handle the no-middot variant defensively.)
	if strings.Contains(lower, "truncated") &&
		(strings.Contains(lower, "more lines") || strings.Contains(lower, "more line")) {
		return true
	}
	return false
}

func cursorPaneContainsToolActivity(delta string) bool {
	for _, line := range strings.Split(stripCursorANSI(delta), "\n") {
		line = normalizeCursorAssistantTurnLabel(normalizeCursorPaneLineWithoutAssistantLabel(line))
		if isCursorToolStatusLine(line) {
			return true
		}
	}
	return false
}

func isCursorBoxDrawingLine(line string) bool {
	if line == "" {
		return true
	}
	for _, r := range line {
		if strings.ContainsRune("─━▀▄▁▂▃▅▆▇█▌▐▝▜▗▟▘▛▙▚▞▖╭╮╰╯│┌┐└┘├┤┬┴┼╞╪╡╘╧╛╔╗╚╝═║╠╣╦╩╬╌╍╎╏┄┅┆┇┈┉┊┋ ", r) {
			continue
		}
		return false
	}
	return true
}

func stripCursorEchoedUserPrompt(text, prompt string) string {
	text = strings.TrimSpace(text)
	prompt = strings.TrimSpace(prompt)
	if text == "" || prompt == "" {
		return text
	}
	// Preserve raw lines (including blank-line paragraph markers) for the
	// returned text, and compute a parallel non-empty view for prompt matching.
	rawLines := strings.Split(text, "\n")
	textNonEmpty := make([]string, 0, len(rawLines))
	rawIndexFor := make([]int, 0, len(rawLines))
	for i, line := range rawLines {
		if strings.TrimSpace(line) != "" {
			textNonEmpty = append(textNonEmpty, line)
			rawIndexFor = append(rawIndexFor, i)
		}
	}
	promptLines := nonEmptyCursorLines(prompt)
	if len(textNonEmpty) == 0 || len(promptLines) == 0 {
		return text
	}
	// An echoed user prompt is structurally a leading block. Searching every
	// text/prompt offset falsely deletes legitimate exact answers when the user
	// instruction contains an example or required output near its end (for
	// example, "return exactly TOKEN / STATUS: COMPLETED"). Match only the
	// beginning of both sequences; Cursor uses [Pasted text] for long prompts,
	// while short literal echoes appear in full at the start of the pane delta.
	bestStart := 0
	bestLen := 0
	for bestLen < len(textNonEmpty) && bestLen < len(promptLines) &&
		cursorPromptLinesEqual(textNonEmpty[bestLen], promptLines[bestLen]) {
		bestLen++
	}
	if bestLen < 2 && !(len(promptLines) == 1 && bestLen == 1) {
		// The strict whole-LINE match failed. This also happens when the prompt
		// is a single long logical line that Cursor's TUI word-wraps across
		// several pane rows at whitespace boundaries — none of the wrapped
		// fragments equals the full prompt line, so the loop above never
		// advances past 0. Reproduced live (real cursor-agent 2026.07.23):
		// a single long prompt line leaked into the returned answer IN FULL,
		// not just a fragment, because this function gave up entirely rather
		// than partially matching. Try reconstructing the wrapped fragments back
		// into one logical line (Cursor wraps at spaces, not mid-word, so
		// joining with a single space and collapsing whitespace is safe) and
		// compare that against the same whitespace-collapsed prompt.
		if reconLen, ok := cursorWrappedPromptEchoSpan(textNonEmpty, promptLines); ok {
			bestStart, bestLen = 0, reconLen
		} else {
			return text
		}
	}
	// If the matched run covers the ENTIRE extracted text, this is not a leaked
	// prompt echo — it means the model's answer legitimately reproduces the
	// prompt verbatim (e.g. "reply with exactly: TOKEN" tasks). Stripping it
	// would yield an empty, failed extraction, so keep the answer intact. This
	// mirrors stripCodexEchoedUserPrompt's identical guard, which cursor's
	// version was missing (found while adding the wrap-reconstruction fallback
	// above and testing it against exactly this case).
	if bestStart == 0 && bestLen == len(textNonEmpty) {
		return text
	}
	// Translate the non-empty match span back to raw-line indices and drop it
	// (keeps paragraph-break blank lines outside the prompt block intact).
	startRaw := rawIndexFor[bestStart]
	endRaw := rawIndexFor[bestStart+bestLen-1] + 1
	out := make([]string, 0, len(rawLines))
	out = append(out, rawLines[:startRaw]...)
	out = append(out, rawLines[endRaw:]...)
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func stripCursorHistoricalAssistantText(text string, historicalAssistantTexts []string) string {
	text = strings.TrimSpace(text)
	if text == "" || len(historicalAssistantTexts) == 0 {
		return text
	}
	for i := len(historicalAssistantTexts) - 1; i >= 0; i-- {
		historical := strings.TrimSpace(historicalAssistantTexts[i])
		if historical == "" {
			continue
		}
		if stripped, ok := stripCursorHistoricalPrefix(text, historical); ok {
			text = strings.TrimSpace(stripped)
			i = len(historicalAssistantTexts)
		}
	}
	return text
}

func stripCursorHistoricalPrefix(text, historical string) (string, bool) {
	if text == historical {
		return "", true
	}
	// A "historical reply" leaks into the new turn's extracted text as a
	// complete leading chunk that ends at a line boundary (because each
	// turn renders on its own line in cursor's pane). A prefix match that
	// runs INTO more characters on the same line is not a leak — it's
	// legitimate new content that happens to start with the same tokens
	// as a prior reply. Without this boundary check, a reply like
	// "WIDGET_A47, WIDGET_B23" got eaten down to ", WIDGET_B23" because
	// "WIDGET_A47" matched a prior turn (observed in
	// TestMultiTurnChatE2E_Cursor before this guard).
	if strings.HasPrefix(text, historical) {
		remainder := text[len(historical):]
		if remainder == "" || strings.HasPrefix(remainder, "\n") {
			return remainder, true
		}
	}
	historicalLines := nonEmptyCursorLines(historical)
	for start := 0; start < len(historicalLines); start++ {
		suffix := strings.Join(historicalLines[start:], "\n")
		if suffix == "" {
			continue
		}
		if text == suffix {
			return "", true
		}
		if strings.HasPrefix(text, suffix) {
			remainder := text[len(suffix):]
			if remainder == "" || strings.HasPrefix(remainder, "\n") {
				return remainder, true
			}
		}
	}
	return text, false
}

func cursorPromptLinesEqual(a, b string) bool {
	a = normalizeCursorPromptLine(a)
	b = normalizeCursorPromptLine(b)
	return a != "" && a == b
}

func cursorWrapWhitespaceNormalize(s string) string {
	return strings.Join(strings.Fields(normalizeCursorPromptLine(s)), " ")
}

func cursorWrappedPromptEchoSpan(textNonEmpty, promptLines []string) (int, bool) {
	target := cursorWrapWhitespaceNormalize(strings.Join(promptLines, " "))
	if target == "" {
		return 0, false
	}
	joined := ""
	maxLines := len(textNonEmpty)
	if maxLines > 64 {
		maxLines = 64
	}
	for i := 0; i < maxLines; i++ {
		frag := cursorWrapWhitespaceNormalize(textNonEmpty[i])
		if frag == "" {
			continue
		}
		if joined == "" {
			joined = frag
		} else {
			joined = joined + " " + frag
		}
		if joined == target {
			return i + 1, true
		}
		if !strings.HasPrefix(target, joined) {
			return 0, false
		}
	}
	return 0, false
}

func normalizeCursorPromptLine(line string) string {
	line = strings.TrimSpace(stripCursorANSI(line))
	line = strings.TrimPrefix(line, "│")
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, ">")
	line = strings.TrimPrefix(line, "›")
	return strings.TrimSpace(line)
}

func nonEmptyCursorLines(text string) []string {
	rawLines := strings.Split(strings.TrimSpace(text), "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func cursorPaneShowsPromptDraft(captured, prompt string) bool {
	return cursorPaneShowsPromptDraftWithMode(captured, prompt, false)
}

func cursorPaneShowsPromptDraftWithMode(captured, prompt string, allowActivity bool) bool {
	// Cursor 2026.08 retains the submitted text next to its `→` marker while a
	// turn is already running (for example: `→ hi` plus `⠘ Working`). That is
	// response-state chrome, not an unsent composer draft. Treating it as a
	// draft caused ensureCursorInputSubmitted to keep pressing Enter until it
	// failed a healthy request.
	if hasCursorActivity(captured) && !allowActivity {
		return false
	}
	visibleLines := strings.Split(strings.ToLower(stripCursorANSI(cursorVisiblePaneText(captured))), "\n")
	lastPromptMarker := -1
	for i := len(visibleLines) - 1; i >= 0; i-- {
		if strings.Contains(visibleLines[i], "→") {
			lastPromptMarker = i
			break
		}
	}
	if lastPromptMarker < 0 {
		return false
	}
	// An explicit empty-composer placeholder is stronger evidence than any
	// substring found in the rows below it. Short replies such as "1" otherwise
	// match the runtime directory or model/status footer while Cursor is busy,
	// causing recovery Enter retries and a false live-input failure.
	markerLine := visibleLines[lastPromptMarker]
	composerLine := strings.TrimSpace(markerLine[strings.Index(markerLine, "→")+len("→"):])
	if hint := strings.Index(composerLine, "ctrl+c to stop"); hint >= 0 {
		composerLine = strings.TrimSpace(composerLine[:hint])
	}
	if composerLine == "add a follow-up" || composerLine == "type your message" {
		return false
	}
	// Only inspect the active editor after the final structural prompt marker.
	// Searching the whole pane mistakes an already-submitted user message in
	// scrollback for a draft that is still waiting in the composer. Do not cap
	// this region to a fixed row count: Cursor hard-wraps long drafts itself, so
	// a valid composer can occupy more than ten terminal rows.
	activeEditor := normalizeCursorDraftProbe(strings.Join(visibleLines[lastPromptMarker:], "\n"))
	// Preserve compatibility with a retained pane created by an older adapter or
	// a user-originated paste. New adapter input remains literal and visible.
	if strings.Contains(activeEditor, "[pasted text #") {
		return true
	}
	probe := cursorDraftTailProbe(prompt, 120)
	return probe != "" && strings.Contains(activeEditor, probe)
}

func cursorDraftTailProbe(prompt string, maxRunes int) string {
	lines := nonEmptyCursorLines(prompt)
	probe := ""
	for i := len(lines) - 1; i >= 0; i-- {
		line := normalizeCursorDraftProbe(lines[i])
		if line == "" {
			continue
		}
		if probe == "" {
			probe = line
		} else {
			probe = line + " " + probe
		}
		runes := []rune(probe)
		if len(runes) >= 32 || i == 0 {
			if maxRunes > 0 && len(runes) > maxRunes {
				return string(runes[len(runes)-maxRunes:])
			}
			return probe
		}
	}
	return ""
}

func normalizeCursorDraftProbe(text string) string {
	text = strings.ReplaceAll(stripCursorANSI(text), "\u00a0", " ")
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

func hasCursorReadyPrompt(captured string) bool {
	visible := cursorVisiblePaneText(captured)
	if hasCursorQueuedFollowupsSendPrompt(visible) {
		return false
	}
	if hasCursorAuthPrompt(visible) {
		return false
	}
	if hasCursorTrustPrompt(visible) {
		return false
	}
	if hasCursorWebAccessApprovalPrompt(visible) {
		return false
	}
	// The MCP-tool approval prompt renders a "→ Run (once)" line whose arrow
	// otherwise looks like a ready input marker — never treat it as ready, or the
	// loop reports a bogus completion while the tool call is still gated.
	if hasCursorMCPToolApprovalPrompt(visible) {
		return false
	}
	if hasCursorMCPServerApprovalPrompt(visible) {
		return false
	}
	if hasCursorModeSwitchPrompt(visible) {
		return false
	}
	cleaned := strings.ToLower(stripCursorANSI(visible))
	if !hasCursorReadyMarker(cleaned) {
		return false
	}
	// Cold-start welcome banner (Cursor v2026.05.24+ Composer 2.5)
	// paints "→ Plan, search, build anything" before the input field
	// is actually interactive. Submitting at that point silently drops
	// the keystrokes. Treat the welcome banner as not-ready; the wait
	// loop continues until the banner state passes.
	if hasCursorBootBanner(cleaned) {
		return false
	}
	// Live generation signals (composing spinner, ctrl+c to stop) mean the
	// turn is still in progress — never treat as ready.
	if hasCursorLiveGenerationActivity(cleaned) {
		return false
	}
	// Cursor leaves stale status lines (Running..., Thinking...) in the pane
	// after a tool finishes. Once the → prompt is visible, stale activity
	// text should not keep the turn open forever.
	if hasCursorActivity(visible) && !strings.Contains(cleaned, "→") {
		return false
	}
	return true
}

func hasCursorBusyFollowupPrompt(captured string) bool {
	visible := cursorVisiblePaneText(captured)
	if hasCursorQueuedFollowupsSendPrompt(visible) || hasCursorAuthPrompt(visible) ||
		hasCursorTrustPrompt(visible) || hasCursorWebAccessApprovalPrompt(visible) ||
		hasCursorMCPToolApprovalPrompt(visible) || hasCursorMCPServerApprovalPrompt(visible) ||
		hasCursorModeSwitchPrompt(visible) {
		return false
	}
	cleaned := strings.ToLower(stripCursorANSI(visible))
	if !hasCursorLiveGenerationActivity(cleaned) && !hasCursorActivity(visible) {
		return false
	}
	for _, rawLine := range strings.Split(cleaned, "\n") {
		line := strings.Join(strings.Fields(rawLine), " ")
		if strings.Contains(line, "→") && strings.Contains(line, "add a follow-up") {
			return true
		}
	}
	return false
}

func PaneReadyForInput(captured string) bool {
	return hasCursorReadyPrompt(captured)
}

func hasCursorQueuedFollowupsSendPrompt(captured string) bool {
	cleaned := strings.ToLower(stripCursorANSI(cursorVisiblePaneText(captured)))
	hasHeader := false
	hasActionFooter := false
	for _, rawLine := range strings.Split(cleaned, "\n") {
		line := strings.Join(strings.Fields(rawLine), " ")
		if strings.Contains(line, "follow-ups") {
			hasHeader = true
		}
		// Cursor 2026.09 calls the same action "steer". Keep the structural
		// header/footer checks so prose cannot submit a user's composer.
		if (strings.Contains(line, "enter send now") || strings.Contains(line, "enter steer")) && strings.Contains(line, "select/edit") {
			hasActionFooter = true
		}
	}
	return hasHeader && hasActionFooter
}

func hasCursorBootBanner(cleaned string) bool {
	if !strings.Contains(cleaned, "cursor agent") {
		return false
	}
	return strings.Contains(cleaned, "plan, search, build anything")
}

func cursorBootBannerAcceptableAfterGrace(cleaned string) bool {
	return hasCursorBootBanner(cleaned) &&
		hasCursorReadyMarker(cleaned) &&
		!hasCursorLiveGenerationActivity(cleaned)
}

func hasCursorLiveGenerationActivity(cleaned string) bool {
	return strings.Contains(cleaned, "ctrl+c to stop") ||
		strings.Contains(cleaned, "composing")
}

func hasCursorReadyMarker(cleaned string) bool {
	// The → arrow is Cursor's structural input cursor — the most reliable
	// signal that the prompt area is visible, regardless of placeholder text.
	for _, line := range strings.Split(cleaned, "\n") {
		if strings.Contains(strings.TrimSpace(line), "→") {
			return true
		}
	}
	return strings.Contains(cleaned, "type your message") ||
		strings.Contains(cleaned, "ask (shift+tab") ||
		strings.Contains(cleaned, "plan, search, build anything") ||
		strings.Contains(cleaned, "what can i help") ||
		strings.Contains(cleaned, "ask me anything") ||
		strings.Contains(cleaned, "message cursor") ||
		strings.Contains(cleaned, "add a follow-up")
}

func hasCursorTrustPrompt(captured string) bool {
	cleaned := strings.ToLower(stripCursorANSI(cursorVisiblePaneText(captured)))
	if strings.Contains(cleaned, "trusting workspace") {
		return false
	}
	return strings.Contains(cleaned, "workspace trust required") ||
		strings.Contains(cleaned, "do you trust the contents of this directory") ||
		strings.Contains(cleaned, "trust") && strings.Contains(cleaned, "workspace") &&
			(strings.Contains(cleaned, "y/n") || strings.Contains(cleaned, "yes") ||
				strings.Contains(cleaned, "[a]") || strings.Contains(cleaned, "[w]"))
}

func hasCursorAuthPrompt(captured string) bool {
	cleaned := strings.ToLower(stripCursorANSI(cursorVisiblePaneText(captured)))
	if strings.TrimSpace(cleaned) == "" {
		return false
	}
	loginInstruction := strings.Contains(cleaned, "cursor-agent login") ||
		strings.Contains(cleaned, "log in to cursor") ||
		strings.Contains(cleaned, "sign in to cursor") ||
		strings.Contains(cleaned, "login to cursor") ||
		strings.Contains(cleaned, "press any key to log in")
	explicitLoggedOut := strings.Contains(cleaned, "not logged in") ||
		strings.Contains(cleaned, "not signed in") ||
		strings.Contains(cleaned, "authentication required") ||
		strings.Contains(cleaned, "please log in") ||
		strings.Contains(cleaned, "please sign in")
	return loginInstruction || explicitLoggedOut && strings.Contains(cleaned, "cursor")
}

func cursorAuthPromptError(captured string) error {
	detail := ""
	if pane := strings.TrimSpace(stripCursorANSI(cursorVisiblePaneText(captured))); pane != "" {
		detail = "latest pane:\n" + pane
	}
	return &llmtypes.CodingAgentAuthRequiredError{
		Provider:     "cursor-cli",
		LoginCommand: "cursor-agent login",
		Detail:       detail,
	}
}

func hasCursorModeSwitchPrompt(captured string) bool {
	cleaned := strings.ToLower(stripCursorANSI(cursorVisiblePaneText(captured)))
	return strings.Contains(cleaned, "switch to agent mode") &&
		(strings.Contains(cleaned, "approve mode switch") ||
			strings.Contains(cleaned, "reject") ||
			strings.Contains(cleaned, "mode switch")) &&
		(strings.Contains(cleaned, "built-in shell/read tools are blocked") ||
			strings.Contains(cleaned, "subagent session") ||
			strings.Contains(cleaned, "mcp bridge"))
}

func hasCursorWebSearchApprovalPrompt(captured string) bool {
	return hasCursorWebAccessApprovalPrompt(captured)
}

func hasCursorWebAccessApprovalPrompt(captured string) bool {
	cleaned := strings.ToLower(stripCursorANSI(cursorVisiblePaneText(captured)))
	return strings.Contains(cleaned, "allow this web search") ||
		strings.Contains(cleaned, "allow search (y)") ||
		strings.Contains(cleaned, "web search:") && strings.Contains(cleaned, "allow") ||
		strings.Contains(cleaned, "allow this web fetch") ||
		strings.Contains(cleaned, "web fetch:") && strings.Contains(cleaned, "allow") ||
		strings.Contains(cleaned, "fetch (y)") && strings.Contains(cleaned, "web fetch") ||
		strings.Contains(cleaned, "open this url") ||
		strings.Contains(cleaned, "open url") && strings.Contains(cleaned, "(y)") ||
		strings.Contains(cleaned, "allow this url") ||
		strings.Contains(cleaned, "allow opening") && strings.Contains(cleaned, "url") ||
		strings.Contains(cleaned, "open link") && strings.Contains(cleaned, "(y)") ||
		hasCursorOpenURLCommandApprovalText(cleaned)
}

func hasCursorOpenURLCommandApprovalText(cleaned string) bool {
	hasURL := strings.Contains(cleaned, "https://") || strings.Contains(cleaned, "http://")
	if !hasURL || !strings.Contains(cleaned, "run this command?") {
		return false
	}
	return strings.Contains(cleaned, "$ open ") ||
		strings.Contains(cleaned, "not in allowlist: open") ||
		strings.Contains(cleaned, "shell(open)")
}

func hasCursorMCPToolApprovalPrompt(captured string) bool {
	cleaned := strings.ToLower(stripCursorANSI(cursorVisiblePaneText(captured)))
	return strings.Contains(cleaned, "run this mcp tool") ||
		strings.Contains(cleaned, "allowlist mcp tool")
}

func hasCursorMCPServerApprovalPrompt(captured string) bool {
	cleaned := strings.ToLower(stripCursorANSI(cursorVisiblePaneText(captured)))
	if strings.Contains(cleaned, "run this mcp tool") ||
		strings.Contains(cleaned, "allowlist mcp tool") {
		return false
	}
	// Keep approval terms structurally tied to an MCP-server line. Whole-pane
	// token co-occurrence is unsafe: a completed turn can contain "available MCP
	// tools / api-bridge" while Cursor's static tip says "enable Plan Mode",
	// which previously looked like an unresolved startup approval forever.
	for _, line := range strings.Split(cleaned, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "mcp server") {
			continue
		}
		if strings.Contains(line, "approval") ||
			strings.Contains(line, "approve") ||
			strings.Contains(line, "allow") ||
			strings.Contains(line, "enable") ||
			strings.Contains(line, "trust") ||
			strings.Contains(line, "continue without") {
			return true
		}
	}
	return false
}

func cursorMCPServerApprovalResponse(captured string) string {
	cleaned := strings.ToLower(stripCursorANSI(captured))
	if strings.Contains(cleaned, "[a]") || strings.Contains(cleaned, "all mcp") || strings.Contains(cleaned, "enable all") {
		return "a"
	}
	return "y"
}

func cursorTrustPromptResponse(captured string) string {
	cleaned := strings.ToLower(stripCursorANSI(captured))
	if strings.Contains(cleaned, "[a]") || strings.Contains(cleaned, "trust this workspace, but don't enable all mcp servers") {
		return "a"
	}
	return "y"
}

func hasCursorActivity(captured string) bool {
	for _, line := range strings.Split(stripCursorANSI(cursorVisiblePaneText(captured)), "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		if lower == "" {
			continue
		}
		// A Braille spinner glyph at the start of a line means the CLI is
		// mid-animation (e.g. "⣾ Generating…"); treat it as activity directly.
		if cursorLineStartsWithSpinner(lower) {
			return true
		}
		// Match status words even when prefixed by a spinner/bullet glyph.
		keyword := cursorActivityKeyword(lower)
		if strings.Contains(lower, "esc to interrupt") ||
			strings.Contains(lower, "ctrl+c to cancel") ||
			strings.Contains(lower, "ctrl+c to stop") ||
			strings.Contains(lower, "composing") ||
			strings.HasPrefix(keyword, "thinking") ||
			strings.HasPrefix(keyword, "working") ||
			strings.HasPrefix(keyword, "running") ||
			strings.HasPrefix(keyword, "generating") ||
			strings.HasPrefix(keyword, "editing") ||
			strings.HasPrefix(keyword, "applying") ||
			strings.HasPrefix(keyword, "calling ") {
			return true
		}
	}
	return false
}

func cursorVisiblePaneText(captured string) string {
	_, rows := tmuxsize.Size()
	if rows <= 0 {
		rows = tmuxsize.DefaultRows
	}
	lines := strings.Split(captured, "\n")
	if len(lines) <= rows {
		return captured
	}
	return strings.Join(lines[len(lines)-rows:], "\n")
}

func streamCursorTerminalSnapshot(ctx context.Context, sessionName string, streamChan chan<- llmtypes.StreamChunk, lastTerminalSnapshot *string) bool {
	snapshot, err := captureCursorPaneForDisplay(ctx, sessionName)
	if err != nil {
		return false
	}
	snapshot = strings.TrimRight(stripCursorANSIPreserveColors(snapshot), "\n")
	if strings.TrimSpace(snapshot) == "" || snapshot == *lastTerminalSnapshot {
		return false
	}
	*lastTerminalSnapshot = snapshot
	select {
	case streamChan <- llmtypes.StreamChunk{
		Type:    llmtypes.StreamChunkTypeTerminal,
		Content: snapshot,
		Metadata: map[string]interface{}{
			"tmux_session":               sessionName,
			"cursor_interactive_session": sessionName,
		},
	}:
		return true
	default:
		return false
	}
}

func interruptCursorInteractiveSession(sessionName string, logger interfaces.Logger) {
	interruptCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := tmuxinput.Default.Do(interruptCtx, tmuxinput.Request{
		SessionID: sessionName,
		Source:    "cursor-cli-internal-interrupt",
		Priority:  tmuxinput.PriorityInterrupt,
	}, func(ctx context.Context) error {
		return runCursorCommand(ctx, nil, "tmux", "send-keys", "-t", sessionName, "Escape")
	})
	if err != nil && logger != nil {
		logger.Debugf("Failed to send Escape to Cursor interactive session %s: %v", sessionName, err)
	}
}

func resetCursorPaneForTurn(ctx context.Context, sessionName string) {
	// Preserve tmux scrollback for browser/UI history. We intentionally do NOT
	// send C-l (0x0C) anymore: Cursor's raw-mode TUI
	// catches that keystroke as "clear display", which wipes the visible
	// chat history the operator is watching in the browser terminal pane.
	// Memory is bounded by the session history-limit, and per-turn parsing is
	// anchored to the captured baseline.
	// Baseline-diff logic in cursorCapturedAfterBaseline tolerates an
	// already-populated pane via LastIndex(captured, baseline).
}

func captureCursorPane(ctx context.Context, sessionName string) (string, error) {
	return tmuxexec.CapturePane(ctx, sessionName, tmuxexec.DefaultScrollbackLines)
}

func captureCursorPaneForDisplay(ctx context.Context, sessionName string) (string, error) {
	// -e preserves ANSI SGR (color, bold, dim, etc.) so the frontend can
	// colorize the snapshot via ansi_up. Cursor positioning sequences are
	// stripped by stripCursorANSIPreserveColors before the snapshot leaves
	// the adapter so they don't garble the rendered output.
	// -J joins wrapped lines so the frontend can handle wrapping natively without
	// hard splitting words mid-line.
	return tmuxexec.CapturePaneANSI(ctx, sessionName, tmuxexec.DefaultScrollbackLines)
}

func cursorCapturedAfterBaseline(captured, baseline string) string {
	if baseline == "" {
		return captured
	}
	// Fast path: exact substring match.
	if idx := strings.LastIndex(captured, baseline); idx >= 0 {
		return captured[idx+len(baseline):]
	}
	// Non-breaking space normalization (matches Claude Code adapter).
	normalizedCaptured := strings.ReplaceAll(captured, " ", " ")
	normalizedBaseline := strings.ReplaceAll(baseline, " ", " ")
	if idx := strings.LastIndex(normalizedCaptured, normalizedBaseline); idx >= 0 {
		return normalizedCaptured[idx+len(normalizedBaseline):]
	}
	// Line-based prefix divergence: on multi-turn panes the baseline and
	// captured share the same top lines (header + earlier turns) but diverge
	// where the new turn starts. Find the first line that differs and
	// return everything from that point onward.
	return cursorLinePrefixDelta(normalizedCaptured, normalizedBaseline)
}

func cursorLinePrefixDelta(captured, baseline string) string {
	capturedLines := strings.Split(captured, "\n")
	baselineLines := strings.Split(baseline, "\n")
	maxCompare := len(capturedLines)
	if len(baselineLines) < maxCompare {
		maxCompare = len(baselineLines)
	}
	divergeAt := 0
	for i := 0; i < maxCompare; i++ {
		if strings.TrimSpace(capturedLines[i]) != strings.TrimSpace(baselineLines[i]) {
			break
		}
		divergeAt = i + 1
	}
	// Require at least a few matching lines to trust the prefix.
	if divergeAt < 3 {
		return captured
	}
	return strings.Join(capturedLines[divergeAt:], "\n")
}

func killCursorTmuxSession(ctx context.Context, sessionName string) error {
	if strings.TrimSpace(sessionName) == "" {
		return nil
	}
	// Reap the pane process trees (CLI + spawned MCP node subprocesses) before
	// killing the session — kill-session only SIGHUPs the pane process, so the
	// children would otherwise orphan and leak.
	tmuxcontrol.ReapSessionProcessTree(ctx, sessionName)
	if err := runCursorCommand(ctx, nil, "tmux", "kill-session", "-t", sessionName); err != nil {
		if isCursorTmuxSessionLostError(err) {
			return nil
		}
		return err
	}
	return nil
}

func isCursorTmuxSessionLostError(err error) bool {
	if err == nil {
		return false
	}
	if llmtypes.IsCodingAgentTmuxSessionLostError(err) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "no server running") ||
		strings.Contains(lower, "can't find pane") ||
		strings.Contains(lower, "can't find session") ||
		strings.Contains(lower, "no current target")
}

func cursorInteractiveSessionPrefix() string {
	prefix := strings.TrimSpace(os.Getenv(EnvCursorInteractiveSessionPrefix))
	if prefix == "" {
		prefix = "mlp-cursor-cli-int"
	}
	return sanitizeCursorTmuxSessionName(prefix)
}

func newCursorTmuxSessionName() string {
	return sanitizeCursorTmuxSessionName(fmt.Sprintf("%s-%d-%s", cursorInteractiveSessionPrefix(), time.Now().UnixNano(), cursorRandomHex(4)))
}

func cursorInteractiveTimeout() time.Duration {
	return cursorDurationFromEnvAllowZero(EnvCursorInteractiveTimeoutSeconds, defaultCursorInteractiveTimeout)
}

func cursorInteractiveCallContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := cursorInteractiveTimeout()
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func cursorInteractiveFirstActivityTimeout() time.Duration {
	return cursorDurationFromEnv(EnvCursorInteractiveFirstActivityTimeoutSeconds, defaultCursorInteractiveFirstActivityTimeout)
}

func cursorInteractiveStalePaneBackstop() time.Duration {
	return cursorDurationFromEnv(EnvCursorInteractiveStalePaneBackstopSeconds, defaultCursorInteractiveStalePaneBackstop)
}

func cursorBrailleSpinner(r rune) bool { return r >= 0x2800 && r <= 0x28FF }

// cursorLineStartsWithSpinner reports whether the trimmed, lowercased line begins
// with an animated Braille spinner glyph.

func cursorLineStartsWithSpinner(lower string) bool {
	for _, r := range lower {
		return cursorBrailleSpinner(r)
	}
	return false
}

func cursorActivityKeyword(lower string) string {
	return strings.TrimLeftFunc(lower, func(r rune) bool { return !unicode.IsLetter(r) })
}

func cursorInteractiveRetention() time.Duration {
	return tmuxlaunch.Retention(defaultCursorInteractiveRetention)
}

func cursorInteractivePromptWait() time.Duration {
	return tmuxlaunch.PromptWait(EnvCursorInteractivePromptWaitSeconds)
}

func cursorInteractiveStreamTmuxScreenEnabled(opts *llmtypes.CallOptions) bool {
	if opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		if v, ok := opts.Metadata.Custom[MetadataKeyStreamTmuxScreen].(bool); ok {
			return v
		}
	}
	return true
}

func cursorDurationFromEnvAllowZero(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func runCursorCommand(ctx context.Context, stdin io.Reader, name string, args ...string) error {
	_, err := runCursorCommandOutput(ctx, stdin, name, args...)
	return err
}

func runCursorCommandOutput(ctx context.Context, stdin io.Reader, name string, args ...string) (string, error) {
	return tmuxexec.RunCommandOutput(ctx, stdin, nil, name, args...)
}

func cursorShellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = cursorShellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func cursorMustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func intPtrFromInt(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

func cursorRandomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func sanitizeCursorTmuxSessionName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "cursor"
	}
	var b strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func stripCursorANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inEscape {
			if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') {
				inEscape = false
			}
			continue
		}
		if ch == 0x1b {
			inEscape = true
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}

func stripCursorANSIPreserveColors(s string) string {
	return paneview.StripANSIPreserveColors(s)
}

func cursorPaneStateSummary(captured string) string {
	visible := cursorVisiblePaneText(captured)
	cleaned := stripCursorANSI(visible)
	flags := []struct {
		name string
		on   bool
	}{
		{"ready", hasCursorReadyPrompt(captured)},
		{"generating", hasCursorLiveGenerationActivity(cleaned)},
		{"activity", hasCursorActivity(captured)},
		{"busy_followup", hasCursorBusyFollowupPrompt(visible)},
		{"queued_send", hasCursorQueuedFollowupsSendPrompt(visible)},
		{"boot", hasCursorBootBanner(cleaned)},
		{"trust", hasCursorTrustPrompt(visible)},
		{"auth", hasCursorAuthPrompt(visible)},
		{"mode_switch", hasCursorModeSwitchPrompt(visible)},
		{"web_approval", hasCursorWebSearchApprovalPrompt(visible) || hasCursorWebAccessApprovalPrompt(visible)},
		{"mcp_approval", hasCursorMCPToolApprovalPrompt(visible) || hasCursorMCPServerApprovalPrompt(visible)},
	}
	var on []string
	for _, f := range flags {
		if f.on {
			on = append(on, f.name)
		}
	}
	if len(on) == 0 {
		on = []string{"none"}
	}
	lines := strings.Split(strings.TrimRight(captured, "\n"), "\n")
	return fmt.Sprintf("states=%s composer=%s lines=%d bytes=%d", strings.Join(on, ","), cursorComposerState(visible), len(lines), len(captured))
}

func cursorComposerState(visible string) string {
	lines := strings.Split(strings.ToLower(stripCursorANSI(visible)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		idx := strings.Index(lines[i], "→")
		if idx < 0 {
			continue
		}
		line := strings.TrimSpace(lines[i][idx+len("→"):])
		if hint := strings.Index(line, "ctrl+c to stop"); hint >= 0 {
			line = strings.TrimSpace(line[:hint])
		}
		if line == "" || line == "add a follow-up" || line == "type your message" {
			return "empty"
		}
		return "nonempty"
	}
	return "absent"
}

const (
	// Cursor's editor can drop part of a large burst made from many independent
	// tmux send-keys calls. Keep ordinary prompts literal and readable, but use
	// one bracketed tmux paste when the payload would require enough calls to
	// become unreliable. Cursor renders that payload as a single pasted-text
	// marker and expands it when the turn is submitted.
	cursorAtomicPasteMinRunes = 2048
	cursorAtomicPasteMinLines = 16
)

func closeCursorTmuxForOwner(ownerSessionID, reason string) {
	closeCursorPersistentSession(ownerSessionID, reason, nil)
}

func cleanupCursorTmuxSessions(ctx context.Context) error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil
	}
	sessions := cursorPersistentRegistry.Drain()

	var failures []string
	for _, session := range sessions {
		cleanupFiles := stopCursorIdleTimerAndSnapshotCleanupIfAvailable(session)
		unregisterCursorInteractiveSession(session.ownerSessionID, session.tmuxSessionName)
		if cleanupFiles != nil {
			cleanupFiles()
		}
		if err := killCursorTmuxSession(ctx, session.tmuxSessionName); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("failed to clean up Cursor interactive sessions: %s", strings.Join(failures, "; "))
	}
	return nil
}

const cursorVisibleInputChunkRunes = 160

const cursorStoreIntakeWait = 90 * time.Second

const cursorStoreIntakeNudgeAfter = 10 * time.Second

func createCursorTmuxChat(ctx context.Context, opts *llmtypes.CallOptions, cwd string, env []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cursor-agent", "--workspace", cwd, "--trust", "create-chat")
	cmd.Dir = cwd
	cmd.Env = append(llmtypes.MergeCodingAgentSecretEnvironment(buildCursorStructuredEnv(""), opts), env...)
	cleanup, err := clisandbox.LandlockCmd(opts.CLISecurity, cmd, cwd, cursorLandlockReads(opts, cmd.Args, cwd), nil)
	if err != nil {
		return "", err
	}
	defer cleanup()
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("create Cursor terminal conversation: %w", err)
	}
	id := strings.TrimSpace(string(out))
	if !regexp.MustCompile(`^[a-fA-F0-9-]{36}$`).MatchString(id) {
		return "", fmt.Errorf("Cursor create-chat returned no valid conversation ID")
	}
	return id, nil
}

// Retained processes must be replaced when credentials, policy, model or cwd change.
func cursorTmuxScopeFingerprint(opts *llmtypes.CallOptions, model string) string {
	return clisandbox.Fingerprint(opts.CLISecurity) + ":" + llmtypes.CodingAgentScopeFingerprint(opts) + ":" + cursorWorkingDirFromOptions(opts) + ":" + model
}
