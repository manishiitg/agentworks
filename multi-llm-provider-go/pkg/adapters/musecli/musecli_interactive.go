package musecli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/internal/slotfs"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/tmuxlaunch"
)

// Interactive (tmux) lane for muse-cli. v1 covers session lifecycle —
// launch, settle detection, prompt submit, teardown — behind an explicit
// opt-in (WithTmuxTransport). Turn completion and text extraction live in
// musecli_interactive_turn.go. The exec --json lane stays the default until
// the tmux lane is certified; nothing routes here unless the caller asks.
//
// Transport truth, enforced by tests on every layer: the adapter runs
// exec-only unless this lane is explicitly requested, even though the
// provider contract declares the tmux transport.

// MetadataKeyMuseTmuxTransport explicitly routes one turn through the tmux
// lane. No other option implies it: interactive-session metadata alone still
// runs exec (orchestrators attach those for bookkeeping on both lanes).
const MetadataKeyMuseTmuxTransport = "muse_tmux_transport"

// WithTmuxTransport routes one turn through the interactive tmux lane:
// boot (or reuse) a `muse` TUI in tmux, submit the prompt, wait for
// completion, extract the final text. Requires tmux and the muse binary.
func WithTmuxTransport(enabled bool) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyMuseTmuxTransport] = enabled
	}
}

func museTmuxTransportRequested(opts *llmtypes.CallOptions) bool {
	if opts == nil || opts.Metadata == nil {
		return false
	}
	requested, _ := opts.Metadata.Custom[MetadataKeyMuseTmuxTransport].(bool)
	return requested
}

// museTmuxSessionPrefix namespaces our panes so a kill can never touch a
// session it did not create.
const museTmuxSessionPrefix = "mlp-muse-"

func museRandomSessionSuffix() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func museTmuxSessionName(owner string) string {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		owner = "bounded-" + museRandomSessionSuffix()
	}
	var clean strings.Builder
	for _, r := range strings.ToLower(owner) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			clean.WriteRune(r)
		default:
			clean.WriteRune('-')
		}
	}
	name := museTmuxSessionPrefix + strings.Trim(clean.String(), "-")
	if len(name) > 60 {
		name = name[:60]
	}
	return name + "-" + museRandomSessionSuffix()
}

// Recognize the active native question widget, not mentions in old output.
func musePendingUserInputError(pane string) error {
	start := museQuestionWidgetStart(pane)
	if start < 0 {
		return nil
	}
	// Collapse whitespace: a narrow pane wraps the hint line ("Esc\ninterrupt").
	widget := strings.Join(strings.Fields(strings.ToLower(pane[start:])), " ")
	interrupt := museEscInterruptRE.MatchString(widget)
	question := (strings.Contains(widget, "enter to select") || museCheckboxWidget(widget)) && interrupt
	review := strings.Contains(widget, "review answers before submit") && strings.Contains(widget, "enter to edit or submit") && strings.Contains(widget, "esc to go back")
	if !question && !review {
		return nil
	}
	return &llmerrors.Error{Kind: llmerrors.KindUserInputRequired, Provider: "muse-cli", Err: fmt.Errorf("Muse is waiting for your answer. Open its terminal and answer or dismiss the question before continuing:\n%s", strings.TrimSpace(pane[start:]))}
}

// Muse animates active tool rows through several diamond glyphs (◆, ◈, ◇).
// Match the symbol category rather than a single animation frame, and use
// the latest heading so older scrollback cannot win.
// The hint reads "Esc to interrupt" (single choice) or "Esc interrupt"
// (checkbox form), on whitespace-collapsed text.
var museEscInterruptRE = regexp.MustCompile(`esc (to )?interrupt`)

// museCheckboxWidget reports Muse's multi-select question ("Enter to toggle ·
// Submit row to continue"), on lowercased text.
func museCheckboxWidget(widget string) bool {
	return strings.Contains(widget, "enter to toggle") && strings.Contains(widget, "submit")
}

var museQuestionHeadingRE = regexp.MustCompile(`(?mi)^[ \t]*\p{So}[ \t]+request user input\b`)
var museToolRowRE = regexp.MustCompile(`(?m)^[ \t]*\p{So}[ \t]+`)

func museQuestionWidgetStart(pane string) int {
	locations := museQuestionHeadingRE.FindAllStringIndex(pane, -1)
	if len(locations) == 0 {
		return -1
	}
	return locations[len(locations)-1][0]
}

// musePaneShowsBlockingGate recognizes a gate surface, not authentication
// words in the conversation. In particular, "log in to <another service>"
// in an assistant answer must never prevent steering the Muse composer.
func musePaneShowsBlockingGate(pane string) bool {
	lower := strings.ToLower(pane)
	lines := strings.Split(lower, "\n")
	// Match complete prompt instructions at line starts. A composer can also
	// be drawn beneath a native dialog, so its presence is not a gate bypass.
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "do you trust ") ||
			strings.HasPrefix(line, "workspace is untrusted") ||
			strings.HasPrefix(line, "this workspace is untrusted") ||
			strings.HasPrefix(line, "run `muse login`") ||
			strings.HasPrefix(line, "run muse login") ||
			strings.HasPrefix(line, "please log in to meta") {
			return true
		}
	}
	return false
}

// museHooksReviewScreen reports the screen Muse shows after it updates itself while an installed plugin has new or
// changed hooks ("Review plugin hooks / N new or changed hooks need your trust", options Review hooks / Trust all and
// continue / Continue without trusting; Esc continues without trusting). It blocks the launch until answered.
func museHooksReviewScreen(pane string) bool {
	lower := strings.ToLower(pane)
	return strings.Contains(lower, "review plugin hooks") && strings.Contains(lower, "need your trust")
}

// museMuseHooksReviewError is what the person reads when the hooks screen would not clear. It never retries: the same
// screen would only multiply the wait.
func museHooksReviewError() error {
	return &llmerrors.Error{Kind: llmerrors.KindUserInputRequired, Provider: "muse-cli", Err: fmt.Errorf("Muse is asking you to review new plugin hooks before it can start. Open Muse once in a terminal and choose, then retry. Your message is saved.")}
}

// museScreenCategory names what a pane that never settled was showing, in words a person can use.
func museScreenCategory(pane string) string {
	lower := strings.ToLower(pane)
	switch {
	case museHooksReviewScreen(pane):
		return "a plugin hooks review screen"
	case musePaneShowsBlockingGate(pane):
		return "a trust or sign-in prompt"
	case strings.Contains(lower, "update") && strings.Contains(lower, "muse code"):
		return "an update screen"
	case strings.TrimSpace(pane) == "":
		return "a blank screen"
	default:
		return "a screen it did not recognise"
	}
}

// museTUISufficientlySettled pins the exact ready markers observed live
// (fresh_launch green 2026-09-10): the "Muse Code <version>" banner, the
// "❯" input prompt, and the "<provider> · <workdir>" status line (meta
// shows "<model> · <effort> · <workdir>", echo shows "echo · <workdir>").
// No gate may be present. Asserting on real markers, not line counts.
// Boot-only: long answers scroll the banner off, so turn-completion waits
// use museTUIAtPrompt instead.
func museTUISufficientlySettled(pane string) bool {
	if musePaneShowsBlockingGate(pane) {
		return false
	}
	return strings.Contains(pane, "Muse Code") &&
		strings.Contains(pane, "❯") &&
		strings.Contains(pane, " · ")
}

// museTUIAtPrompt reports whether the pane shows a live TUI at its input
// prompt, without requiring the boot banner (which scrolls off during long
// turns — proven live 2026-09-10 when a completed 300-char-line answer sat
// under a fresh ❯ for five minutes while the banner-gated waiter timed
// out). The prompt glyph plus the "<a> · <b>" status line is the idle
// shape; streaming panes share it, so completion also requires stability
// and log quiet. A running tool can have both while waiting for its result.
func museTUIAtPrompt(pane string) bool {
	if musePaneShowsBlockingGate(pane) || musePaneHasRunningTool(pane) {
		return false
	}
	return strings.Contains(pane, "❯") && strings.Contains(pane, " · ")
}

// Only inspect the latest rendered block: an older tool's running text in
// scrollback must not hide a later completed response. Live-input steering
// intentionally does not use this idle check; the composer works mid-tool.
func musePaneHasRunningTool(pane string) bool {
	locations := museToolRowRE.FindAllStringIndex(pane, -1)
	if len(locations) == 0 {
		return false
	}
	start := locations[len(locations)-1][0]
	block := strings.ToLower(pane[start:])
	return strings.Contains(block, "— running (") && strings.Contains(block, "esc to interrupt")
}

func museTmuxCapturePane(ctx context.Context, session string) (string, error) {
	out, err := exec.CommandContext(ctx, "tmux", "capture-pane", "-p", "-t", session).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func museTmuxSessionAlive(ctx context.Context, session string) bool {
	return exec.CommandContext(ctx, "tmux", "has-session", "-t", session).Run() == nil
}

func museKillTmuxSession(ctx context.Context, session string) {
	_ = exec.CommandContext(ctx, "tmux", "kill-session", "-t", session).Run()
}

// museLaunchTUI boots `muse --trust-workspace` in a detached tmux session.
// --trust-workspace keeps launch about launch, not gate UX: trust gates are
// CertTrustAuthPrompts territory, and the settle waiter below fails loudly
// if the flag ever stops working.
// museTUIApprovalArgv is the approval posture for every MCP-mounted TUI,
// persistent or bounded: --disable-approval alone does NOT suppress MCP-tool
// approval (proven live 2026-09-10, a mounted turn calling
// mcp__api_bridge__execute_shell_command parked in approval_wait.effect.started
// and hung the full 8-minute ctx while --disable-approval was passed).
// --approval-mode never is the flag that actually covers MCP tools; both are
// passed (autonomous coding-agent operation, same posture as the
// codex/claude adapters). One helper so the two launch paths cannot drift.
func museTUIApprovalArgv() []string {
	return []string{"--disable-approval", "--approval-mode", "never"}
}

// museNativeContainmentArgv is defense in depth for bridge-routed turns.
// The PreToolUse hook is the primary execution allowlist; these first-class
// switches keep native shell and filesystem writes unavailable even if hook
// loading or matching ever regresses.
func museNativeContainmentArgv() []string {
	return []string{"--disable-shell", "--disable-write"}
}

// museToolArgv is the native-tool part of a Muse launch. With an allowlist
// (bridge-only) its shell and writes are disabled. Without one (Full CLI) it
// runs with --yolo: no approval prompts and no Muse sandbox, like Claude's
// full mode. AgentWorks' confinement (Landlock, Seatbelt) is the boundary when
// it applies; Muse's own Bubblewrap sandbox cannot start inside the lock and
// the slot's user namespace, and the footer notice its failed probe left kept
// the TUI from ever settling (Excellence 2026-10-03: the first prompt was never
// typed in). Unconfined (a person's own Mac) Muse runs with the person's
// rights, as every CLI does in full mode there.
func museToolArgv(toolAllowlistSet bool) []string {
	if toolAllowlistSet {
		return museNativeContainmentArgv()
	}
	return []string{"--yolo"}
}

// museEffortArgv passes the chat's reasoning effort to an interactive (TUI)
// launch, as the exec lane already does: without it a project set to "max"
// ran at Muse's default (high). It is a top-level option, so it also applies
// to `muse resume`. An unknown value is an error, never silently dropped.
func museEffortArgv(ctx context.Context) ([]string, error) {
	effort, err := museExecEffort(museAccount(ctx).opts)
	if err != nil || effort == "" {
		return nil, err
	}
	return []string{"--reasoning-effort", effort}, nil
}

// museLaunchTUI boots one bounded (one-turn) TUI and returns its isolated
// config cleanup. mcpJSON mirrors the persistent lane: a mounted turn carries
// museTUIApprovalArgv. Unmounted turns boot bare.
func museLaunchTUI(ctx context.Context, workdir, session, provider, mcpJSON string, toolAllowlist []string) (func(), error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil, fmt.Errorf("tmux not found in PATH; muse-cli tmux mode requires tmux: %w", err)
	}
	if _, err := exec.LookPath("muse"); err != nil {
		return nil, fmt.Errorf("muse CLI not in PATH: %w", err)
	}
	configHome, cleanup, err := museAccountConfig(ctx, strings.TrimSpace(mcpJSON), toolAllowlist)
	if err != nil {
		return nil, err
	}
	argv := []string{"env", "XDG_CONFIG_HOME=" + configHome, "XDG_DATA_HOME=" + museAccountDataHome(ctx), "muse", "--trust-workspace", "--provider", provider}
	if strings.TrimSpace(mcpJSON) != "" {
		argv = append(argv, museTUIApprovalArgv()...)
	}
	argv = append(argv, museToolArgv(toolAllowlist != nil)...)
	effortArgv, err := museEffortArgv(ctx)
	if err != nil {
		cleanup()
		return nil, err
	}
	argv = append(argv, effortArgv...)
	shell, cleanupLaunch, err := museAccountLaunch(ctx, argv, workdir)
	if err != nil {
		cleanup()
		return nil, err
	}
	defer func() { time.AfterFunc(30*time.Second, cleanupLaunch) }()
	launch := exec.CommandContext(ctx, "tmux", append([]string{"new-session", "-d", "-s", session,
		"-x", "200", "-y", "50", "-c", workdir}, shell)...)
	launch.Env = tmuxlaunch.CleanClientEnv()
	if out, err := launch.CombinedOutput(); err != nil {
		cleanup()
		return nil, fmt.Errorf("tmux new-session: %w\n%s", err, out)
	}
	return cleanup, nil
}

// museWaitSettled polls a freshly launched pane until the TUI settles, a
// blocking gate appears, or the deadline passes. It deliberately requires
// the boot banner; reused and native-resumed sessions must call museWaitAtPrompt because a long
// conversation scrolls that banner out of the pane.
func museWaitSettled(ctx context.Context, session string, timeout time.Duration) (string, error) {
	return museWaitForReadyPane(ctx, session, timeout, museTUISufficientlySettled, "muse TUI to settle")
}

// museWaitAtPrompt waits for a reused TUI to become ready for another turn.
// Unlike the fresh-boot waiter it only requires the idle prompt/status chrome,
// which remains visible after the startup banner has scrolled away.
func museWaitAtPrompt(ctx context.Context, session string, timeout time.Duration) (string, error) {
	return museWaitForReadyPane(ctx, session, timeout, museTUIAtPrompt, "muse TUI to return to the prompt")
}

func museWaitForReadyPane(ctx context.Context, session string, timeout time.Duration, ready func(string) bool, waitDescription string) (string, error) {
	deadline := time.Now().Add(timeout)
	var pane string
	hooksEsc := 0
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if !museTmuxSessionAlive(ctx, session) {
			if hint := slotfs.LatestLaunchStderrLine(2 * time.Minute); hint != "" {
				// A session that dies at once is usually a command that never started
				// (not found, permission denied); the slot launcher records why.
				return "", fmt.Errorf("muse tmux session %q died while waiting for %s (launcher: %s)", session, waitDescription, hint)
			}
			return "", fmt.Errorf("muse tmux session %q died while waiting for %s", session, waitDescription)
		}
		p, err := museTmuxCapturePane(ctx, session)
		if err != nil {
			return "", fmt.Errorf("capture pane: %w", err)
		}
		pane = p
		pending, err := museHandlePendingQuestion(ctx, session, pane)
		if err != nil {
			return "", err
		}
		if pending {
			if timeout > 0 && time.Now().After(deadline) {
				return "", musePendingUserInputError(pane)
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
			continue
		}
		if ready(pane) && musePaneStable(ctx, session, pane) {
			return pane, nil
		}
		if museHooksReviewScreen(pane) {
			// Esc is the screen's own "continue without trusting": it grants nothing and runs no hooks.
			if hooksEsc >= 2 {
				log.Printf("[TURN] stalled cause=muse_hooks_review provider=muse-cli session=%s action=esc result=not_cleared", session)
				return "", museHooksReviewError()
			}
			hooksEsc++
			out, err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", session, "Escape").CombinedOutput()
			log.Printf("[TURN] gate=muse_hooks_review provider=muse-cli session=%s action=esc attempt=%d result=%s", session, hooksEsc, museSendKeysResult(err, out))
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(1500 * time.Millisecond):
			}
			continue
		}
		if musePaneShowsBlockingGate(pane) {
			return "", fmt.Errorf("muse TUI blocked on trust/auth gate (trust_auth_prompts cert territory); pane:\n%s", pane)
		}
		if timeout > 0 && time.Now().After(deadline) {
			category := museScreenCategory(pane)
			log.Printf("[TURN] stalled cause=launch_timeout provider=muse-cli session=%s screen=%q", session, category)
			err := fmt.Errorf("Muse did not start in %s: it was showing %s. Your message is saved; press Retry. (timed out waiting for %s; latest pane:\n%s)", timeout.Round(time.Second), category, waitDescription, pane)
			if museSameLaunchScreenTwice(pane) {
				// A screen that did not clear after one retry will not clear after four more.
				return "", &llmerrors.Error{Kind: llmerrors.KindUserInputRequired, Provider: "muse-cli", Err: err}
			}
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("settle wait canceled: %w", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// musePaneStable reports whether the pane content stops changing across a
// short window: chrome can render seconds before the input loop accepts
// submissions (observed live: an Enter sent right after first render is
// swallowed). Settling requires markers AND stability.
func musePaneStable(ctx context.Context, session, pane string) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(time.Second):
	}
	again, err := museTmuxCapturePane(ctx, session)
	return err == nil && again == pane
}

// musePaneSettledForSubmit is the pre-Enter settle for a draft that is
// already confirmed visible in a TUI past its boot readiness. The fixed 1s
// window of musePaneStable (a boot guard) put every Muse send over the 1s
// retained-delivery P0 envelope (PLAT-102). Here the draft is done once the
// pane is unchanged for ~120ms, capped at the same 1s; a swallowed Enter is
// still caught and retried by museEnterTookEffect.
func musePaneSettledForSubmit(ctx context.Context, session, pane string) bool {
	const (
		poll   = 50 * time.Millisecond
		window = 120 * time.Millisecond
		cap    = time.Second
	)
	deadline := time.Now().Add(cap)
	last, lastChange := pane, time.Now()
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(poll):
		}
		current, err := museTmuxCapturePane(ctx, session)
		if err != nil {
			return false
		}
		if current != last {
			last, lastChange = current, time.Now()
			continue
		}
		if time.Since(lastChange) >= window {
			return true
		}
	}
	return false
}

// museVisibleInputChunkRunes bounds each literal tmux key injection. tmux
// rejects a single oversized send-keys argument ("command too long",
// reproduced live with a 100KB builder prompt), so prompts are typed in
// bounded literal chunks with embedded newlines sent as Ctrl+J (newline
// without submit).
const museVisibleInputChunkRunes = 160

const (
	// Prompts at or above these sizes go through one atomic tmux buffer
	// paste instead of dozens of rapid send-keys calls: same thresholds as
	// cursor, whose editor demonstrably drops parts of large literal bursts.
	museAtomicPasteMinRunes = 2048
	museAtomicPasteMinLines = 16
)

// museSendPrompt types the prompt into the TUI pane and submits it, retrying
// the submit (not just the typing) a bounded number of times.
//
// Confirming the draft is visible (writeVisibleDraftAndConfirm) is not
// enough: observed live 2026-09-10, a fully visible, confirmed "hi" sat
// unsubmitted at the prompt indefinitely after Enter -- tmux send-keys
// reported success, but the TUI never acted on it (no reply, no streaming,
// pane byte-for-byte unchanged). museWaitIntake's downstream Enter-resend
// loop couldn't recover it: that loop only resends a bare Enter into
// whatever state the pane is already in, so a keystroke genuinely dropped
// by the TUI just gets dropped again. This is the same render-race class
// museWaitSettled already guards at boot ("an Enter sent right after first
// render is swallowed"); submission needs the equivalent guard.
func museSendPrompt(ctx context.Context, session, prompt string) error {
	prompt = museTerminalPrompt(prompt)
	const maxSubmitAttempts = 3
	start := time.Now()
	for attempt := 1; ; attempt++ {
		stage := time.Now()
		if err := museWaitLiveInputComposer(ctx, session); err != nil {
			return err
		}
		// A resume boots through an "Initializing runtime" window where
		// keystrokes are swallowed; wait it out before typing.
		museWaitInitializingClear(ctx, session)
		composer := time.Since(stage)
		stage = time.Now()
		if musePromptNeedsAtomicPaste(prompt) {
			if err := pasteMuseDraftToTmux(ctx, session, prompt); err != nil {
				return err
			}
		} else if err := writeVisibleDraftAndConfirm(ctx, session, prompt); err != nil {
			return err
		}
		draft := time.Since(stage)
		beforePane, err := museTmuxCapturePane(ctx, session)
		if err != nil {
			return fmt.Errorf("capture pane before Enter: %w", err)
		}
		// Give the just-confirmed draft a beat to finish rendering before
		// firing Enter into it -- the same guard museWaitSettled applies at
		// boot, applied here at submit time.
		stage = time.Now()
		settled := musePaneSettledForSubmit(ctx, session, beforePane)
		settle := time.Since(stage)
		enterArgs := append([]string{"send-keys", "-t", session}, museSubmitKeys()...)
		enter := exec.CommandContext(ctx, "tmux", enterArgs...)
		if out, err := enter.CombinedOutput(); err != nil {
			return fmt.Errorf("tmux send-keys submit: %w\n%s", err, out)
		}
		stage = time.Now()
		took := museEnterTookEffect(ctx, session, beforePane)
		log.Printf("[LATENCY_DEBUG] muse submit | session=%s attempt=%d composer=%dms draft=%dms settle=%dms settled=%v enter_confirm=%dms took=%v total=%dms runes=%d",
			session, attempt, composer.Milliseconds(), draft.Milliseconds(), settle.Milliseconds(), settled, time.Since(stage).Milliseconds(), took, time.Since(start).Milliseconds(), utf8.RuneCountInString(prompt))
		if took {
			// Durable-ack P0 (docs/refactor/durable_ack_p0.md in the builder, PLAT-352): attempt once, report the truth. Once Enter took effect nothing is
			// typed or submitted again from here, and the pane never decides delivery. A footer notice ("Message not sent -- another run is still
			// starting") also changes the pane; it is only logged. Recovery from a genuine refusal lives in museConfirmSubmitOrResubmit, which resubmits
			// only for the exact unsent bottom draft plus a still-absent intake record. A stale notice over accepted input sends nothing, and
			// recovery presses Enter without clearing/retyping. museWaitIntake observes the native session.jsonl for the intake record, with
			// its slow-start allowance, and says "unconfirmed" if it never comes.
			if pane, paneErr := museTmuxCapturePane(ctx, session); paneErr == nil && museRejectedNotice(pane) {
				log.Printf("[MUSE_SUBMIT_NOTICE] session=%s Muse showed \"Message not sent\"; not retyping, waiting for durable intake", session)
			}
			return nil
		}
		if attempt >= maxSubmitAttempts {
			return fmt.Errorf("muse TUI did not act on Enter after %d submit attempts; the keystroke may have been swallowed mid-render", maxSubmitAttempts)
		}
		clear := exec.CommandContext(ctx, "tmux", "send-keys", "-t", session, "C-u")
		if out, err := clear.CombinedOutput(); err != nil {
			return fmt.Errorf("tmux clear input before resubmit retry: %w\n%s", err, out)
		}
	}
}

// museRejectedNotice reports Muse's footer notice for a refused message ("Message not sent -- another run is still starting"). It is only logged:
// delivery is decided by the native intake record, never by this text.
func museRejectedNotice(pane string) bool {
	return strings.Contains(strings.ToLower(pane), "message not sent")
}

func museSubmitKeys() []string {
	return []string{"Enter", "Enter"}
}

// museTerminalPrompt removes control characters that a terminal editor treats
// as keyboard commands instead of text. Tabs copied from tables are semantic
// separators, so one ordinary space preserves the prompt while ensuring Muse,
// the visible-draft guard, and transcript discovery all see identical text.
func museTerminalPrompt(prompt string) string {
	prompt = strings.ReplaceAll(prompt, "\r\n", "\n")
	prompt = strings.ReplaceAll(prompt, "\r", "\n")
	return strings.ReplaceAll(prompt, "\t", " ")
}

// museEnterTookEffect reports whether the pane changed within a short
// window after Enter was sent. This is a fast hint, not delivery proof;
// durable intake makes the authoritative decision. Any change counts (cleared input,
// a thinking indicator, streamed text, or an already-complete reply); the
// failure mode this guards is the pane staying byte-for-byte identical to
// before Enter was sent.
func museEnterTookEffect(ctx context.Context, session, beforePane string) bool {
	deadline := time.Now().Add(2 * time.Second)
	for {
		pane, err := museTmuxCapturePane(ctx, session)
		if err == nil && pane != beforePane {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// writeVisibleDraftAndConfirm types the prompt via writeMuseVisibleDraftToTmux
// and waits for it to actually appear in the pane before returning, retyping
// (after clearing the line) a bounded number of times if it doesn't.
//
// pasteMuseDraftToTmux (the large-prompt path) already waits for its own
// collapse/visible markers before its caller submits; this plain-typed path
// had no equivalent check, so a keystroke burst swallowed mid-render (the
// same class of race documented on musePaneStable -- "an Enter sent right
// after first render is swallowed") went straight to museSendPrompt's Enter
// with an empty input box. museWaitIntake's retry loop only re-sends a bare
// Enter, never retypes, so every retry kept submitting nothing for the full
// 60s deadline: "muse TUI never took in the prompt after submit" no matter
// how many Enters it resent. Observed live 2026-09-10 on a trivial one-word
// prompt ("hi"), well under the atomic-paste threshold.
func writeVisibleDraftAndConfirm(ctx context.Context, session, prompt string) error {
	snippet := prompt
	if idx := strings.Index(snippet, "\n"); idx >= 0 {
		snippet = snippet[:idx]
	}
	snippet = strings.TrimSpace(snippet)
	if len([]rune(snippet)) > 40 {
		snippet = string([]rune(snippet)[:40])
	}
	const maxAttempts = 3
	for attempt := 1; ; attempt++ {
		if err := museWaitLiveInputComposer(ctx, session); err != nil {
			return err
		}
		if err := writeMuseVisibleDraftToTmux(ctx, session, prompt); err != nil {
			return err
		}
		if snippet == "" {
			return nil // nothing to verify (a genuinely empty prompt)
		}
		deadline := time.Now().Add(2 * time.Second)
		for {
			pane, err := museTmuxCapturePane(ctx, session)
			if err == nil && musePaneContainsVisibleDraft(pane, snippet) {
				return nil
			}
			if time.Now().After(deadline) {
				break
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("muse type-and-confirm wait canceled: %w", ctx.Err())
			case <-time.After(40 * time.Millisecond):
			}
		}
		if attempt >= maxAttempts {
			return fmt.Errorf("muse TUI did not show the typed prompt after %d attempts; the keystrokes may have been swallowed mid-render", maxAttempts)
		}
		clear := exec.CommandContext(ctx, "tmux", "send-keys", "-t", session, "C-u")
		if out, err := clear.CombinedOutput(); err != nil {
			return fmt.Errorf("tmux clear input before retry: %w\n%s", err, out)
		}
	}
}

// musePaneContainsVisibleDraft compares terminal-visible text rather than raw
// input bytes. A copied table commonly contains tabs, while Muse renders those
// tabs (and wrapped lines) as spaces in tmux capture-pane output. Requiring an
// exact raw substring therefore reports failure even though the complete draft
// is visibly present and ready to submit.
func musePaneContainsVisibleDraft(pane, snippet string) bool {
	if strings.Contains(pane, snippet) {
		return true
	}
	visibleSnippet := strings.Join(strings.Fields(snippet), " ")
	if visibleSnippet == "" {
		return false
	}
	visiblePane := strings.Join(strings.Fields(pane), " ")
	return strings.Contains(visiblePane, visibleSnippet)
}

// musePromptNeedsAtomicPaste reports whether the prompt is large enough that
// literal chunk typing would take enough tmux round-trips to become
// unreliable.
func musePromptNeedsAtomicPaste(prompt string) bool {
	return utf8.RuneCountInString(prompt) >= museAtomicPasteMinRunes ||
		strings.Count(prompt, "\n")+1 >= museAtomicPasteMinLines
}

// pasteMuseDraftToTmux transfers a large prompt through stdin rather than
// argv, then asks tmux to emit it as one bracketed paste. This avoids both
// tmux's command-size rejection and partial delivery from dozens of rapid
// send-keys subprocesses. The deferred delete best-effort cleans the named
// buffer on every path.
//
// Muse collapses a bracketed paste into a "[Pasted Content N chars]"
// attachment whose own chrome instructs "paste again to expand for editing"
// (proven live: a single paste leaves the payload collapsed, a second paste
// expands it into visible text exactly once — never duplicated). So this
// waits for the collapse marker and pastes the same buffer again; when the
// prompt is already visible with no collapse, the second paste is skipped so
// content can never double.
func pasteMuseDraftToTmux(ctx context.Context, session, prompt string) error {
	bufferName := "mlp-muse-input-" + museRandomSessionSuffix()
	load := exec.CommandContext(ctx, "tmux", "load-buffer", "-b", bufferName, "-")
	load.Stdin = strings.NewReader(prompt)
	if out, err := load.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux load-buffer muse input: %w\n%s", err, out)
	}
	defer func() {
		_ = exec.CommandContext(context.Background(), "tmux", "delete-buffer", "-b", bufferName).Run()
	}()
	paste := func(deleteAfter bool) error {
		args := []string{"paste-buffer", "-p", "-r", "-b", bufferName, "-t", session}
		if deleteAfter {
			args = []string{"paste-buffer", "-d", "-p", "-r", "-b", bufferName, "-t", session}
		}
		cmd := exec.CommandContext(ctx, "tmux", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("tmux paste-buffer muse input: %w\n%s", err, out)
		}
		return nil
	}
	if err := paste(false); err != nil {
		return err
	}
	snippet := prompt
	if idx := strings.Index(snippet, "\n"); idx >= 0 {
		snippet = snippet[:idx]
	}
	if len([]rune(snippet)) > 40 {
		snippet = string([]rune(snippet)[:40])
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		pane, err := museTmuxCapturePane(ctx, session)
		if err != nil {
			return fmt.Errorf("tmux capture after muse paste: %w", err)
		}
		if strings.Contains(pane, "Pasted Content") || strings.Contains(pane, "Paste again to expand") {
			return paste(true)
		}
		if snippet != "" && strings.Contains(pane, snippet) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("muse TUI showed neither pasted-content collapse nor prompt text after atomic paste")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("muse paste-expand wait canceled: %w", ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// writeMuseVisibleDraftToTmux types the prompt in bounded literal chunks so
// no single tmux command approaches the size rejection. A "--" separator
// keeps a chunk that starts with "-" from parsing as a flag.
func writeMuseVisibleDraftToTmux(ctx context.Context, session, prompt string) error {
	prompt = strings.ReplaceAll(prompt, "\r\n", "\n")
	prompt = strings.ReplaceAll(prompt, "\r", "\n")
	lines := strings.Split(prompt, "\n")
	for lineIndex, line := range lines {
		runes := []rune(line)
		for len(runes) > 0 {
			chunkSize := min(len(runes), museVisibleInputChunkRunes)
			chunk := string(runes[:chunkSize])
			send := exec.CommandContext(ctx, "tmux", "send-keys", "-t", session, "-l", "--", chunk)
			if out, err := send.CombinedOutput(); err != nil {
				return fmt.Errorf("tmux send-keys prompt chunk: %w\n%s", err, out)
			}
			runes = runes[chunkSize:]
		}
		if lineIndex < len(lines)-1 {
			nl := exec.CommandContext(ctx, "tmux", "send-keys", "-t", session, "C-j")
			if out, err := nl.CombinedOutput(); err != nil {
				return fmt.Errorf("tmux send-keys newline: %w\n%s", err, out)
			}
		}
	}
	return nil
}

func museSendKeysResult(err error, out []byte) string {
	if err != nil {
		return "error:" + strings.TrimSpace(string(out))
	}
	return "sent"
}

var (
	museLaunchScreenMu   sync.Mutex
	museLaunchScreenSeen = map[string]time.Time{}
)

// museSameLaunchScreenTwice reports whether the same stuck screen already timed a launch out in the last ten minutes,
// and remembers this one. The first timeout is retried once; the second ends the turn.
func museSameLaunchScreenTwice(pane string) bool {
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(pane), " ")))
	key := hex.EncodeToString(sum[:8])
	now := time.Now()
	museLaunchScreenMu.Lock()
	defer museLaunchScreenMu.Unlock()
	for k, at := range museLaunchScreenSeen {
		if now.Sub(at) > 10*time.Minute {
			delete(museLaunchScreenSeen, k)
		}
	}
	_, seen := museLaunchScreenSeen[key]
	museLaunchScreenSeen[key] = now
	return seen
}
