package musecli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Turn driving for the interactive (tmux) lane. v1 is a bounded single
// turn: boot a TUI, submit one prompt, wait for the pane to re-settle,
// extract the final text from the session transcript, tear the session
// down. Persistent pane reuse across turns is a later slice; requesting it
// fails loudly rather than silently running uncontained single turns.

// museDiscoverSessionSince finds the TUI session log produced after `since`
// with a native intake record containing promptSnippet. It walks the dated session tree
// ($XDG_DATA_HOME/muse/sessions/YYYY/MM/DD/<id>/session.jsonl) and returns
// the session id and log path. Pure filesystem work — unit-tested with
// fixture trees, no CLI.
func museDiscoverSessionSince(dataHome string, since time.Time, promptSnippet, workdir string) (sessionID, logPath string, err error) {
	root := filepath.Join(dataHome, "muse", "sessions")
	var bestPath string
	var bestMod time.Time
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || d.Name() != "session.jsonl" {
			return nil
		}
		// Subagent transcripts live below <id>/subagent/<id>/session.jsonl.
		// Their copied prompts must not be mistaken for the owning TUI turn.
		rel, err := filepath.Rel(root, path)
		if err != nil || len(strings.Split(rel, string(filepath.Separator))) != 5 {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(since) {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if !museLogAcceptedPromptSince(path, since, promptSnippet) {
			return nil
		}
		// Concurrent turns (two pooled TUIs, same dataHome) can send
		// byte-identical prompts: snippet+mtime alone then misattributes
		// one worker's turn to the other's log (proven live: worker 1
		// answered with worker 0's build id). The native log records its
		// TUI's workspace_root in runtime.session.metadata, and pooled
		// workdirs are unique per owner — filter on it when known.
		// A transferred journal deliberately keeps its original metadata. Its
		// owner-bound handoff marker names the only additional workspace we
		// may use for intake discovery; ordinary sessions keep the old check.
		if workdir != "" && !museLogMatchesWorkspace(raw, workdir) && !sessiontransfer.MuseHandoffWorkspace(dataHome, filepath.Base(filepath.Dir(path)), workdir) {
			return nil
		}
		if info.ModTime().After(bestMod) {
			bestMod = info.ModTime()
			bestPath = path
		}
		return nil
	})
	if walkErr != nil {
		return "", "", fmt.Errorf("walk muse sessions: %w", walkErr)
	}
	if bestPath == "" {
		return "", "", fmt.Errorf("no muse session log has a matching intake record since %s", since.Format(time.RFC3339))
	}
	return filepath.Base(filepath.Dir(bestPath)), bestPath, nil
}

// museLogAcceptedPromptSince requires a new, native intake record. A file's
// mtime or an assistant echo of the prompt is not evidence that this send was
// accepted, especially when identical notifications are retried.
type museIntakeRecord struct {
	Sequence    int64  `json:"sequence"`
	RecordedAt  int64  `json:"recorded_at"`
	PayloadType string `json:"payload_type"`
	Payload     struct {
		IntentID     string `json:"intent_id"`
		RefillBlocks []struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		} `json:"refill_blocks"`
		// Muse collapses a long prompt that reaches the TUI as a paste to "[Pasted Content N chars]" in refill_blocks and
		// keeps the real text here, which is what the model received.
		ModelMessages []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"model_messages"`
	} `json:"payload"`
}

func museLogAcceptedPromptSince(path string, since time.Time, snippet string) bool {
	_, _, ok := museAcceptedIntentSince(path, since, snippet)
	return ok
}

// museAcceptedIntentSince returns the accepted intent for this submission.
// The intent ID is also the run ID on ordinary interactive turns; callers
// use it to exclude old turns from completion, final text, and usage.
func museAcceptedIntentSince(path string, since time.Time, snippet string) (string, int64, bool) {
	if snippet == "" {
		return "", 0, false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0, false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !strings.Contains(string(line), `"runtime.user_intent.accepted"`) {
			continue
		}
		var row museIntakeRecord
		if json.Unmarshal(line, &row) != nil || row.PayloadType != "runtime.user_intent.accepted" || row.RecordedAt < since.UnixMicro() {
			continue
		}
		for _, block := range row.Payload.RefillBlocks {
			if block.Kind == "text" && strings.HasPrefix(block.Text, snippet) {
				return row.Payload.IntentID, row.Sequence, true
			}
		}
		// A prompt between Muse's paste-collapse size and the adapter's atomic-paste size (about 1.6k characters, one line) is
		// recorded as a placeholder in refill_blocks, so the text it started with is only in model_messages. Without this every
		// such turn was reported "intake unconfirmed" although Muse ran it (a scheduled run that failed 4 of 5 ticks).
		for _, message := range row.Payload.ModelMessages {
			for _, part := range message.Content {
				if strings.HasPrefix(part.Text, snippet) {
					return row.Payload.IntentID, row.Sequence, true
				}
			}
		}
	}
	return "", 0, false
}

// museLogMatchesWorkspace reports whether raw session.jsonl bytes record
// the given workdir as their TUI's workspace_root. Both sides resolve
// symlinks first (/var vs /private/var on darwin) and compare JSON-escaped,
// the encoding the log actually stores. The compare is case-insensitive:
// on a case-insensitive-but-case-preserving filesystem (macOS APFS default,
// Windows), muse's own path canonicalization can record the true on-disk
// casing (e.g. "agentworks") while the caller passes a differently-cased
// path to the same directory (e.g. "AgentWorks") — proven live: a real
// workdir under ~/Library/Application Support/AgentWorks/... was rejected
// against its own matching session log solely because muse recorded
// "agentworks", causing every turn against that workdir to time out as
// "never took in the prompt" even though muse answered correctly.
func museLogMatchesWorkspace(raw []byte, workdir string) bool {
	dir := strings.TrimSpace(workdir)
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	rawLower := strings.ToLower(string(raw))
	needle := strings.ToLower(`"workspace_root":` + strconv.Quote(dir))
	if strings.Contains(rawLower, needle) {
		return true
	}
	// Fall back to the unresolved path (logs on some platforms store it).
	return strings.Contains(rawLower, strings.ToLower(`"workspace_root":`+strconv.Quote(strings.TrimSpace(workdir))))
}

// museLastAssistantText returns the last AI message text in order.
func museLastAssistantText(messages []llmtypes.MessageContent) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llmtypes.ChatMessageTypeAI {
			continue
		}
		var parts []string
		for _, part := range messages[i].Parts {
			switch c := part.(type) {
			case llmtypes.TextContent:
				parts = append(parts, c.Text)
			case *llmtypes.TextContent:
				if c != nil {
					parts = append(parts, c.Text)
				}
			}
		}
		if text := strings.TrimSpace(strings.Join(parts, "")); text != "" {
			return text
		}
	}
	return ""
}

const (
	// museIntakeWait is how long to wait for Muse to accept a prompt when no
	// Muse session for the workdir has appeared.
	museIntakeWait = 60 * time.Second
	// museSlowStartIntakeWait bounds the wait once Muse has started a session
	// for the workdir. The first prompt of a new session was accepted 66s and
	// 135s after the session started on 2026-09-24/25; a 60s limit failed
	// those turns while Muse went on to run them.
	museSlowStartIntakeWait = 5 * time.Minute
)

// museSessionStartedSince reports whether Muse has written a session log for
// this workdir since turnStart: Muse is running but has not yet accepted the
// prompt. Without a workdir the session cannot be attributed, so it reports
// false and the normal wait applies.
func museSessionStartedSince(dataHome string, since time.Time, workdir string) bool {
	if strings.TrimSpace(workdir) == "" {
		return false
	}
	root := filepath.Join(dataHome, "muse", "sessions")
	found := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if found || walkErr != nil || d.IsDir() || d.Name() != "session.jsonl" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || len(strings.Split(rel, string(filepath.Separator))) != 5 {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(since) {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if museLogMatchesWorkspace(raw, workdir) {
			found = true
		}
		return nil
	})
	return found
}

// museWaitIntake verifies the TUI actually took in the prompt: the native
// session log must show the turn after submit. This is observe-only: a pane
// mismatch cannot justify another Enter while durable intake is uncertain.
func museWaitIntake(ctx context.Context, session string, turnStart time.Time, snippet, workdir string) (nativeSessionID, logPath string, err error) {
	dataHome := museAccountDataHome(ctx)
	waitStart := time.Now()
	intakeDeadline := waitStart.Add(museIntakeWait)
	extended := false
	for {
		id, path, findErr := museDiscoverSessionSince(dataHome, turnStart, snippet, workdir)
		if findErr == nil {
			return id, path, nil
		}
		err = findErr
		if time.Now().After(intakeDeadline) {
			// A new Muse session can take over a minute to accept its first
			// prompt. Once Muse has started a session for this workdir it is
			// up and still initializing: give up now and the turn is marked
			// failed while Muse goes on to run it unseen.
			if !extended && museSessionStartedSince(dataHome, turnStart, workdir) {
				extended = true
				intakeDeadline = waitStart.Add(museSlowStartIntakeWait)
			} else if extended {
				return "", "", fmt.Errorf("muse prompt delivery unconfirmed: muse started a session but did not accept the prompt within %s; last discovery error: %w", museSlowStartIntakeWait, err)
			} else {
				return "", "", fmt.Errorf("muse prompt delivery unconfirmed after durable intake wait; last discovery error: %w", err)
			}
		}
		// A native question can appear before transcript discovery catches up.
		// Handle the blocker, but never resubmit the prompt from this loop.
		pane, captureErr := museTmuxCapturePane(ctx, session)
		if captureErr == nil {
			if _, questionErr := museHandlePendingQuestion(ctx, session, pane); questionErr != nil {
				return "", "", questionErr
			}
		}
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// museLogQuietSince reports whether the native session log has been
// untouched for at least quietFor.
func museLogQuietSince(logPath string, quietFor time.Duration) bool {
	info, err := os.Stat(logPath)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) >= quietFor
}

// museResolveTmuxPrompt decides what gets typed into the tmux pane: the
// legacy inline concatenation (system folded ahead of human) by default, or
// the bare human turn ONLY once AGENTS.md is confirmed to actually carry the
// system prompt (agentsProjected true).
//
// Defaulting to the fold, not to `human` alone, matters: wantAgents is false
// whenever instruction-only mode is off (the default -- see
// WithProjectInstructionOnly), so a caller that starts from `human` and only
// widens to the fold when a fallback condition fires never runs that
// fallback in the off-by-default case, silently dropping every system
// message for every default-mode tmux call. Caught 2026-09-10: no test
// exercised a system message through this lane with instructionOnly left
// unset before this shape existed.
func museResolveTmuxPrompt(system []string, human string, wantAgents, agentsProjected bool) string {
	if wantAgents && agentsProjected {
		return human
	}
	return museInlinePrompt(system, human)
}

// generateContentTmux runs one bounded turn through a fresh TUI session.
func (a *MuseCLIAdapter) generateContentTmux(ctx context.Context, messages []llmtypes.MessageContent, opts *llmtypes.CallOptions) (*llmtypes.ContentResponse, error) {
	ctx = museWithAccount(ctx, opts, a.apiKey)
	ctx = museWithAutoAnswer(ctx, opts)
	persistent := musePersistentInteractiveFromOptions(opts)
	owner := strings.TrimSpace(museInteractiveSessionIDFromOptions(opts))
	launchOnly := llmtypes.CodingProviderLaunchOnlyFromOptions(opts)
	if persistent && owner == "" {
		return nil, fmt.Errorf("muse-cli persistent tmux requires an owner session id (WithMuseInteractiveSessionID)")
	}
	if launchOnly && !persistent {
		return nil, fmt.Errorf("muse-cli launch-only requires the persistent option: a bounded session would die before reuse")
	}
	if persistent {
		releaseTurn, err := museAcquirePersistentTurn(ctx, owner)
		if err != nil {
			return nil, fmt.Errorf("wait for previous muse turn: %w", err)
		}
		defer releaseTurn()
	}
	// Launch-only with an empty prompt is the transport-session handshake:
	// boot (or rebind) the TUI and hand back its handle. Anything else
	// needs a real prompt.
	system, human, err := museSplitPrompt(messages)
	if err != nil && !launchOnly {
		return nil, err
	}
	workdir := strings.TrimSpace(museWorkingDirFromOptions(opts))
	if workdir == "" {
		var err error
		workdir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve working dir for muse tmux lane: %w", err)
		}
	}
	if skills := llmtypes.AttachedSkillsFromOptions(opts); len(skills) > 0 {
		_ = a.ProjectSkills(llmtypes.CLIArtifactDirectory(opts, workdir), skills)
	}
	instructionOnly := museProjectInstructionOnlyFromOptions(opts)
	wantAgents := instructionOnly && len(system) > 0 && !llmtypes.CLIHasPrivateRuntime(opts)
	if note := llmtypes.CLIPrivateSkillInstructions(opts, museSkillsSubdir); note != "" {
		system = append(system, note)
	}

	var prompt string // set below via museResolveTmuxPrompt once launch/acquire decides whether AGENTS.md carries the system prompt
	session := ""
	nativeIDAtLaunch := ""
	if persistent {
		toolAllowlist, toolAllowlistSet := museToolAllowlistFromOptions(opts)
		if !toolAllowlistSet {
			toolAllowlist = nil
		}
		entry, _, err := museAcquirePersistentSession(ctx, owner, workdir,
			a.museExecProvider(), strings.TrimSpace(a.modelID),
			strings.TrimSpace(museMCPConfigFromOptions(opts)), toolAllowlist, musePersistentReadyFile(opts),
			strings.Join(system, "\n\n"), wantAgents, museRestoreProjectFilesFromOptions(opts),
			museResumeSessionIDFromOptions(opts))
		if err != nil {
			return nil, err
		}
		ctx = museBindPersistentAutoAnswer(ctx, entry)
		session = entry.tmuxName
		musePersistentPool.Lock()
		nativeIDAtLaunch = entry.nativeSessionID
		// One-time resume note: only on the first real message after a `muse resume` launch of this terminal.
		if entry.resumeNotePending && strings.TrimSpace(human) != "" {
			entry.resumeNotePending = false
			human = museWithResumeNote(human, true, entry.mcpJSON)
			if sessiontransfer.MuseHandoffWorkspace(entry.accountDataHome, entry.nativeSessionID, workdir) {
				human = nativeHandoffInstructions(human, strings.Join(system, "\n\n"))
			}
		}
		musePersistentPool.Unlock()
		if nativeIDAtLaunch == "" {
			human = museFreshHistoryPrompt(messages, human)
		}
		prompt = museResolveTmuxPrompt(system, human, wantAgents, entry.agentsProjected)
	} else {
		human = museFreshHistoryPrompt(messages, human)
		restoreAgents, projected := projectMuseAgentsForTurn(workdir, system, wantAgents, museRestoreProjectFilesFromOptions(opts))
		if restoreAgents != nil {
			defer restoreAgents()
		}
		prompt = museResolveTmuxPrompt(system, human, wantAgents, projected)
		session = museTmuxSessionName(museInteractiveSessionIDFromOptions(opts))
		toolAllowlist, toolAllowlistSet := museToolAllowlistFromOptions(opts)
		if !toolAllowlistSet {
			toolAllowlist = nil
		}
		restoreMCP, err := museLaunchTUI(ctx, workdir, session, a.museExecProvider(), strings.TrimSpace(museMCPConfigFromOptions(opts)), toolAllowlist)
		if err != nil {
			return nil, err
		}
		// Keep the real tmux pane alive for the shared bounded retention
		// window (llmtypes.TmuxKillDelay) instead of killing it inline, the
		// same pattern claude-code/codex-cli/cursor-cli/pi-cli all use: the
		// periodic pane scraper needs that window to capture a final
		// snapshot into the terminals store, which is what backs the UI's
		// "main terminal" view after the turn completes. Killing
		// synchronously here (the previous behavior) tore the pane down
		// before that scraper cycle could ever run, so muse's UI terminal
		// was permanently empty even on a fully successful turn.
		defer func() {
			time.AfterFunc(llmtypes.TmuxKillDelay, func() {
				museKillTmuxSession(context.Background(), session)
			})
		}()
		if restoreMCP != nil {
			defer restoreMCP()
		}
	}
	if persistent && launchOnly {
		// Acquire already waited for settle (+ MCP readiness on fresh
		// launch). Return the handle so the orchestrator rebinds to this
		// tmux session; the native id is unknown until the first turn.
		gi := &llmtypes.GenerationInfo{}
		llmtypes.AttachCodingProviderSessionHandle(gi, llmtypes.CodingProviderSessionHandle{
			Provider:        "muse-cli",
			Transport:       llmtypes.CodingProviderTransportTmux,
			TmuxSession:     session,
			WorkingDir:      workdir,
			NativeSessionID: nativeIDAtLaunch,
			Model:           strings.TrimSpace(a.modelID),
		})
		return &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{
			Content:        "",
			StopReason:     "completed",
			GenerationInfo: gi,
		}}}, nil
	}

	// Bounded sessions boot above and still have their startup banner.
	// Persistent sessions may still be executing a previous/live-input turn.
	// Let the caller own that wait: a local 90-second timeout would enter the
	// network retry loop while the same native turn continues doing work.
	var readyPane string
	var readyErr error
	if persistent {
		readyPane, readyErr = museWaitAtPrompt(ctx, session, 0)
	} else {
		readyPane, readyErr = museWaitSettled(ctx, session, 90*time.Second)
	}
	if readyErr != nil {
		return nil, readyErr
	}
	var preSubmitLogPath string
	if persistent {
		if err := musePrepareStoppedPrompt(ctx, owner, session, readyPane); err != nil {
			return nil, err
		}
		if key, err := musePersistentKey(owner); err == nil {
			musePersistentPool.Lock()
			if entry := musePersistentPool.m[key]; entry != nil && entry.tmuxName == session {
				entry.lastSubmittedPrompt = museTerminalPrompt(prompt)
				preSubmitLogPath = entry.logPath
				if preSubmitLogPath == "" && entry.nativeSessionID != "" {
					preSubmitLogPath = museSessionLogPath(entry.nativeSessionID, entry.accountDataHome)
				}
			}
			musePersistentPool.Unlock()
		}
	}
	preSubmitSeq := museTranscriptMaxSequence(preSubmitLogPath)
	turnStart := time.Now()
	submitErr := museSendPrompt(ctx, session, prompt)
	// Recover only an exact idle draft with a delayed Enter. The native log
	// still decides acceptance; recovery never clears or retypes the prompt.
	if err := museConfirmSubmitOrResubmit(ctx, museAccountDataHome(ctx), session, prompt, turnStart, workdir); err != nil {
		if persistent {
			museRememberUnconfirmedDraft(owner, session)
		}
		return nil, err
	}
	nativeSessionID, logPath, err := museWaitIntake(ctx, session, turnStart, promptSnippet(prompt), workdir)
	if err != nil {
		// Screen changes are not intake. Record only state, never the prompt,
		// so a retained draft can be distinguished from a missed journal entry.
		if pane, captureErr := museTmuxCapturePane(ctx, session); captureErr == nil {
			draft, composer := museActiveComposerDraft(pane)
			log.Printf("[MUSE_INTAKE_UNCONFIRMED] session=%s composer=%v draft_present=%v refusal_notice=%v initializing=%v", session, composer, draft != "", museRejectedNotice(pane), musePaneInitializing(pane))
		}
		if persistent {
			museRememberUnconfirmedDraft(owner, session)
		}
		if submitErr != nil {
			return nil, fmt.Errorf("muse intake unconfirmed after pane submit error (%w): %w", submitErr, err)
		}
		return nil, err
	}
	llmtypes.SignalPromptAccepted(opts.StreamChan)
	runID, acceptedSeq, ok := museAcceptedIntentSince(logPath, turnStart, promptSnippet(prompt))
	if !ok || runID == "" {
		return nil, fmt.Errorf("muse intake record has no current run ID (session %s)", nativeSessionID)
	}
	if persistent {
		museRecordPersistentTranscript(owner, session, nativeSessionID, logPath)
	}
	// Opt-in transcript streaming: tail the intake-discovered session.jsonl
	// so assistant text + tool starts/ends stream while the turn runs.
	// Started here because the log path is only known once intake finds it,
	// but primed to the pre-submit sequence so a fast answer committed before
	// discovery still streams. Stopped synchronously below —
	// the final flush lands before return, and the adapter never closes
	// StreamChan itself (caller-owned, exec-lane precedent).
	var museStreamState *museTranscriptStreamState
	var museStreamCancel context.CancelFunc
	if opts.StreamChan != nil && (museInteractiveStreamTranscriptEnabled(opts) || museInteractiveStreamTmuxScreenEnabled(opts)) {
		if preSubmitLogPath != logPath {
			preSubmitSeq = 0 // new native session: no prior rows to replay
		}
		streamCtx, cancel := context.WithCancel(ctx)
		museStreamCancel = cancel
		museStreamState = newMuseTranscriptStreamStateAt(logPath, session,
			museInteractiveStreamTranscriptEnabled(opts), museInteractiveStreamTmuxScreenEnabled(opts), preSubmitSeq)
		go museStreamState.run(streamCtx, opts.StreamChan)
	}
	stopMuseStream := func() {
		if museStreamCancel != nil {
			museStreamCancel()
			<-museStreamState.done
			museStreamCancel = nil
		}
	}
	// Tool calls and delegated workflows can legitimately exceed five minutes.
	// Keep observing this submitted turn until completion or caller cancellation;
	// never abandon it on an adapter deadline and retry its prompt.
	after, err := museWaitTurnTerminal(ctx, session, logPath, runID, acceptedSeq, 0)
	if err != nil {
		stopMuseStream()
		return nil, err
	}
	// A background subagent's result arrives after this run ended, in a new
	// inbox-delivery run; that run holds the real answer.
	finalRunID, err := museWaitSubagentContinuation(ctx, session, logPath, runID, acceptedSeq)
	stopMuseStream()
	if err != nil {
		return nil, err
	}
	if finalRunID != runID {
		if pane, captureErr := museTmuxCapturePane(ctx, session); captureErr == nil {
			after = pane
		}
	}

	transcript, ok := readMuseTranscriptMessages(logPath, finalRunID)
	if !ok {
		return nil, fmt.Errorf("read muse TUI transcript at %s", logPath)
	}
	final := museLastAssistantText(transcript)
	if quotaErr := museUsageLimitError(strings.TrimSpace(a.modelID), time.Now(), final); quotaErr != nil {
		return nil, quotaErr
	}
	if strings.TrimSpace(final) == "" {
		return nil, fmt.Errorf("muse TUI turn produced no assistant text (session %s)", nativeSessionID)
	}
	gi := &llmtypes.GenerationInfo{}
	if expected := museResumeSessionIDFromOptions(opts); expected != "" && sessiontransfer.MuseHandoffWorkspace(museAccountDataHome(ctx), expected, workdir) && nativeSessionID != expected {
		return nil, fmt.Errorf("Muse started a different native session after handoff; the saved conversation was preserved")
	}
	llmtypes.AttachCodingProviderSessionHandle(gi, llmtypes.CodingProviderSessionHandle{
		Provider:        "muse-cli",
		Transport:       llmtypes.CodingProviderTransportTmux,
		NativeSessionID: nativeSessionID,
		TmuxSession:     session,
		WorkingDir:      workdir,
		Model:           strings.TrimSpace(a.modelID),
	})
	// Keep a best-effort post-turn pane for terminal presentation and the
	// formatting cert. The native run event, not pane appearance, establishes
	// completion and scopes the returned answer.
	if gi.Additional == nil {
		gi.Additional = map[string]any{}
	}
	gi.Additional["final_tmux_pane"] = after
	resp := &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{
		Content:        final,
		StopReason:     "completed",
		GenerationInfo: gi,
	}}}
	usage, usageOK := readMuseTranscriptUsage(logPath, finalRunID)
	// Native subagents this turn started spent tokens in their own logs; they are part of the turn's cost.
	subagentsPriced := false
	for _, sub := range museSubagentUsage(logPath, turnStart) {
		addMuseUsage(&usage, sub.Usage)
		usageOK, subagentsPriced = true, true
	}
	if usageOK {
		resp.Usage = &usage
		museAttachTurnCost(gi, strings.TrimSpace(a.modelID), &usage)
		if subagentsPriced {
			gi.Additional["usage_rate_assumed"] = "parent" // one event per turn: the subagents' tokens are priced at the turn's model
		}
	}
	return resp, nil
}

// promptSnippet is the discovery anchor: a long-enough slice of the prompt
// that identifies this turn's session log without false-matching.
func promptSnippet(prompt string) string {
	const maxSnippet = 120
	snippet := strings.TrimSpace(museTerminalPrompt(prompt))
	if len(snippet) > maxSnippet {
		end := maxSnippet
		for end > 0 && !utf8.ValidString(snippet[:end]) {
			end--
		}
		snippet = strings.TrimSpace(snippet[:end])
	}
	return snippet
}
