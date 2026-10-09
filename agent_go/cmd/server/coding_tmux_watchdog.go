package server

import (
	"bytes"
	"context"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
)

// Coding-CLI rate-limit watchdog.
//
// When an interactive coding CLI hits its provider usage/session limit, it
// prints a limit wall and then parks at an idle prompt. The backend session can
// stay marked "running" with a live-but-useless tmux pane, so the UI refuses
// new input and scheduled runs wedge. Adapter-level detection covers only some
// paths; this watchdog sits above them by watching the panes directly.
const (
	codingWatchdogInterval = 30 * time.Second
	// codingWatchdogConfirmChecks is how many consecutive rate-limited
	// observations are required before force-stopping.
	codingWatchdogConfirmChecks = 2
	// A missing pane can be expected cleanup racing a still-unwinding provider
	// turn. Confirm it across polls so the primary provider/tool error reaches
	// the durable event stream instead of being overwritten by a secondary
	// "tmux pane disappeared" failure.
	codingWatchdogMissingConfirmChecks = 2
	// Only inspect the current tail. Rate-limit text can remain in tmux scrollback
	// after a provider has recovered and resumed useful work.
	codingWatchdogRateLimitTailLines = 80
	// codingWatchdogDeadPaneTailLines caps how much of a crashed pane's
	// captured output the log carries — enough to show the actual crash
	// near the end without flooding the log with a long session's prior
	// scrollback.
	codingWatchdogDeadPaneTailLines = 120
)

var captureTmuxPanePlainForWatchdog = captureTmuxPanePlain
var closeCodingCLITmuxForWatchdog = gracefulCloseCodingCLITmuxByName

type codingWatchdogObservation struct {
	evidence string
	count    int
}

func (api *StreamingAPI) startCodingTmuxRateLimitWatchdog() {
	if api == nil || api.terminalStore == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(codingWatchdogInterval)
		defer ticker.Stop()
		streak := make(map[string]codingWatchdogObservation)
		for range ticker.C {
			api.reapRateLimitedCodingSessionsOnce(streak)
		}
	}()
	log.Printf("[CODING_WATCHDOG] Started coding-CLI rate-limit watchdog (interval=%s, confirm=%d checks)",
		codingWatchdogInterval, codingWatchdogConfirmChecks)
}

func (api *StreamingAPI) reapRateLimitedCodingSessionsOnce(streak map[string]codingWatchdogObservation) {
	// Metadata listing deliberately avoids Store's content reconciliation. Actual
	// tmux pane state below is authoritative for whether a terminal is still live.
	// Process supervision must include terminals dismissed from presentation.
	// ListRaw avoids content reconciliation while retaining those ownership rows.
	snapshots := api.terminalStore.ListRaw("")
	stillLimited := make(map[string]bool)

	for _, snap := range snapshots {
		tmux := strings.TrimSpace(snap.TmuxSession)
		sessionID := strings.TrimSpace(snap.SessionID)
		if tmux == "" || sessionID == "" {
			continue
		}
		// Confirmation belongs to one real pane, not the app session. A workflow
		// session can host several terminals; keying by tmux keeps two limited
		// panes from satisfying a two-tick streak during the same poll.
		watchdogKey := tmux
		switch inspectCodingTmuxPaneState(tmux) {
		case codingTmuxPaneMissing:
			stillLimited[watchdogKey] = true
			observation := streak[watchdogKey]
			if observation.evidence == "tmux-pane-missing" {
				observation.count++
			} else {
				observation = codingWatchdogObservation{evidence: "tmux-pane-missing", count: 1}
			}
			streak[watchdogKey] = observation
			if observation.count < codingWatchdogMissingConfirmChecks {
				log.Printf("[CODING_WATCHDOG] session %s tmux %s is missing (%d/%d) - waiting for owner cleanup to settle",
					sessionID, tmux, observation.count, codingWatchdogMissingConfirmChecks)
				continue
			}
			reason := "tmux pane disappeared unexpectedly"
			// A main pane can vanish because the running turn replaced it:
			// the provider's submit retry starts a fresh pane. Cancelling the
			// session then kills the very retry that could recover (server A SDE
			// crew, 2026-09-27). While a turn is in flight it owns the outcome
			// -- the adapter fails the turn itself if its pane is truly lost --
			// so only retire this terminal record.
			if snap.Active && codingAgentSnapshotIsMainAgent(snap) && api.hasActiveTurnCancel(sessionID) {
				log.Printf("[CODING_WATCHDOG] session %s main tmux %s is gone while a turn is in flight - leaving recovery to the turn", sessionID, tmux)
			} else if snap.Active {
				api.terminalStore.MarkFailed(snap.TerminalID)
				api.reconcileUnexpectedTerminalExit(snap, reason)
			}
			api.terminalStore.MarkProcessClosed(snap.TerminalID, reason)
			if registry := api.ensureTerminalLeaseRegistry(); registry != nil {
				registry.MarkClosed(tmux, reason, time.Now())
			}
			delete(streak, watchdogKey)
			continue
		case codingTmuxPaneDead:
			reason := "tmux pane exited unexpectedly"
			// Captured before the kill below destroys it — this is the only
			// place that ever sees why a coding-CLI process actually died (a
			// codex-cli/claude-code crash, an auth failure, a bridge error);
			// until now that evidence was thrown away and every occurrence
			// started the next investigation from zero. tmux keeps a dead
			// pane's content available (its default remain-on-exit) right up
			// until kill-session actually runs.
			if captured := captureTmuxPanePlainForWatchdog(tmux); strings.TrimSpace(captured) != "" {
				log.Printf("[CODING_WATCHDOG] dead pane output before cleanup | session=%s tmux=%s:\n%s",
					sessionID, tmux, tailLines(captured, codingWatchdogDeadPaneTailLines))
			}
			killCtx, cancel := context.WithTimeout(context.Background(), terminalTmuxActionTimeout)
			killErr := runTmuxKill(killCtx, tmux)
			cancel()
			if killErr != nil && !isMissingTmuxTargetError(killErr) {
				log.Printf("[CODING_WATCHDOG] dead pane cleanup failed session=%s tmux=%s: %v", sessionID, tmux, killErr)
				continue
			}
			if snap.Active {
				api.terminalStore.MarkFailed(snap.TerminalID)
				api.reconcileUnexpectedTerminalExit(snap, reason)
			}
			api.terminalStore.MarkProcessClosed(snap.TerminalID, reason)
			if registry := api.ensureTerminalLeaseRegistry(); registry != nil {
				registry.MarkClosed(tmux, reason, time.Now())
			}
			delete(streak, watchdogKey)
			continue
		case codingTmuxPaneUnknown:
			// A transient tmux command failure is not evidence of completion.
			continue
		}
		captured := captureTmuxPanePlainForWatchdog(tmux)
		evidence := codingWatchdogRateLimitEvidence(captured)
		if evidence == "" {
			continue
		}
		// The CLI's own usage numbers outrank screen text: when they are known
		// and no window is exhausted, limit-looking text on screen is someone's
		// words (a redrawn message, a quoted error), not a wall.
		if known, exhausted := codingWatchdogStructuredUsage(tmux); known && !exhausted {
			if streak[watchdogKey].count == 0 {
				log.Printf("[CODING_WATCHDOG] session %s tmux %s shows limit-like text but the CLI reports usage below its limits - not a wall", sessionID, tmux)
			}
			delete(streak, watchdogKey)
			continue
		}
		stillLimited[watchdogKey] = true
		observation := streak[watchdogKey]
		if observation.evidence == evidence {
			observation.count++
		} else {
			observation = codingWatchdogObservation{evidence: evidence, count: 1}
		}
		streak[watchdogKey] = observation
		if observation.count < codingWatchdogConfirmChecks {
			log.Printf("[CODING_WATCHDOG] session %s tmux %s looks rate-limited (%d/%d) - waiting to confirm",
				sessionID, tmux, observation.count, codingWatchdogConfirmChecks)
			continue
		}

		limitReason := codingWatchdogLimitReason(snap)
		isMainAgent := codingAgentSnapshotIsMainAgent(snap)
		if isMainAgent {
			log.Printf("[CODING_WATCHDOG] main session %s tmux %s parked on a usage/rate-limit wall - failing and canceling session runtime",
				sessionID, tmux)
			// Persist failure before closing panes: stream goroutines may unwind as
			// soon as cancellation starts and must not overwrite this as completed.
			api.reconcileUnexpectedTerminalExit(snap, limitReason)
			delete(streak, watchdogKey)
			continue
		} else {
			log.Printf("[CODING_WATCHDOG] child terminal %s session %s tmux %s parked on a usage/rate-limit wall - closing child only",
				snap.TerminalID, sessionID, tmux)
		}
		closeCodingCLITmuxForWatchdog(tmux, limitReason)
		killCtx, cancel := context.WithTimeout(context.Background(), terminalTmuxActionTimeout)
		_ = runTmuxKill(killCtx, tmux)
		cancel()
		api.terminalStore.MarkFailed(snap.TerminalID)
		api.terminalStore.MarkProcessClosed(snap.TerminalID, limitReason)
		api.reconcileUnexpectedTerminalExit(snap, limitReason)
		if registry := api.ensureTerminalLeaseRegistry(); registry != nil {
			registry.MarkClosed(tmux, "provider usage/rate limit reached", time.Now())
		}
		delete(streak, watchdogKey)
	}

	for watchdogKey := range streak {
		if !stillLimited[watchdogKey] {
			delete(streak, watchdogKey)
		}
	}
}

// codingWatchdogStructuredUsage reports a pane's plan usage from the coding
// CLI's own data (known, and whether a window is exhausted): Claude's
// statusline rate_limits (written after its first response) and Codex's
// rollout rate-limit windows. Other CLIs, and panes without data yet, are
// unknown and fall back to screen text. A var so tests can stub it.
var codingWatchdogStructuredUsage = func(tmux string) (known, exhausted bool) {
	switch {
	case strings.HasPrefix(tmux, "mlp-claude-code-"):
		return claudecode.UsageLimitState(tmux)
	case strings.HasPrefix(tmux, "mlp-codex-cli-"):
		return codexcli.UsageLimitState(tmux)
	}
	return false, false
}

// codingWatchdogRateLimitEvidence returns the normalized visible tail when it
// contains a rate-limit marker. The watchdog confirms this entire value across
// polls, so any new output proves the pane is progressing and resets the check.
// Old rate-limit text outside the current tail is ignored.
func codingWatchdogRateLimitEvidence(content string) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > codingWatchdogRateLimitTailLines {
		lines = lines[len(lines)-codingWatchdogRateLimitTailLines:]
	}
	normalized := make([]string, 0, len(lines))
	hasRateLimit := false
	inNarration := false
	// The tail can start inside a wrapped block whose opening line ("● " reply
	// or "> " user message) is above the window; its indented continuation
	// lines cannot be attributed, so they are not evidence either.
	atWindowStart := true
	for _, raw := range lines {
		line := strings.Join(strings.Fields(raw), " ")
		if line == "" {
			continue
		}
		normalized = append(normalized, strings.ToLower(line))
		// The assistant's own reply text ("● Notion rate-limited that query —
		// retrying") is narration about the work, not the provider's limit
		// wall, and a user message redrawn in the transcript ("> we got rate
		// limited in mcp", or the "❯" input box) is the user's words. Claude
		// prefixes reply blocks with "● ", user messages with ">"/"❯", and
		// indents their wrapped lines by two spaces; tool results ("⎿", where
		// API errors appear) and CLI status lines are still checked. Seen on
		// server A 2026-09-27: a resumed SDE crew pane redrew the user's "we got rate
		// limited" message, the watchdog read it as a limit wall and killed
		// every new turn.
		trimmedLeft := strings.TrimLeft(raw, " ")
		continuation := strings.HasPrefix(raw, "  ") && !strings.HasPrefix(trimmedLeft, "⎿")
		switch {
		case strings.HasPrefix(trimmedLeft, "● "), strings.HasPrefix(trimmedLeft, ">"), strings.HasPrefix(trimmedLeft, "❯"):
			inNarration = true
			atWindowStart = false
			continue
		case (inNarration || atWindowStart) && continuation:
			continue
		default:
			inNarration = false
			atWindowStart = false
		}
		hasRateLimit = hasRateLimit || terminals.DetectRateLimit(line)
	}
	// Claude replaces its original "You've hit your session limit" line with
	// a two-choice interactive menu. The provider wording is then no longer in
	// the visible pane, which used to leave the watchdog (and therefore every
	// product UI) unable to distinguish it from useful ongoing work.
	menu := strings.Join(normalized, "\n")
	if strings.Contains(menu, "stop and wait for limit to reset") && strings.Contains(menu, "upgrade your plan") {
		hasRateLimit = true
	}
	if !hasRateLimit {
		return ""
	}
	return strings.Join(normalized, "\n")
}

func codingWatchdogLimitReason(snapshot terminals.Snapshot) string {
	provider := strings.TrimSpace(snapshot.Status.ProviderLabel)
	if provider == "" {
		provider = "The coding assistant"
	}
	return fmt.Sprintf("%s has reached its usage limit. This request was stopped, so it will not keep working in the background. Try again after the provider limit resets or use another provider.", provider)
}

type codingTmuxPaneState int

const (
	codingTmuxPaneUnknown codingTmuxPaneState = iota
	codingTmuxPaneAlive
	codingTmuxPaneDead
	codingTmuxPaneMissing
)

func inspectCodingTmuxPaneState(tmuxSession string) codingTmuxPaneState {
	tmuxSession = strings.TrimSpace(tmuxSession)
	if tmuxSession == "" {
		return codingTmuxPaneMissing
	}
	ctx, cancel := context.WithTimeout(context.Background(), terminalTmuxActionTimeout)
	defer cancel()
	out, err := runTerminalTmuxOutputCommand(ctx, "display-message", "-p", "-t", tmuxSession, "#{pane_dead}")
	if err != nil {
		if isMissingTmuxTargetError(err) {
			return codingTmuxPaneMissing
		}
		return codingTmuxPaneUnknown
	}
	switch strings.TrimSpace(out) {
	case "0":
		return codingTmuxPaneAlive
	case "1":
		return codingTmuxPaneDead
	default:
		// tmux display-message can return exit 0 with an empty expansion for a
		// missing target while another tmux server/session is alive. Confirm the
		// target explicitly so a disappeared pane cannot remain "unknown" forever.
		if strings.TrimSpace(out) == "" {
			if _, err := runTerminalTmuxOutputCommand(ctx, "has-session", "-t", tmuxSession); isMissingTmuxTargetError(err) {
				return codingTmuxPaneMissing
			}
		}
		return codingTmuxPaneUnknown
	}
}

// tailLines returns the last n lines of s verbatim — unlike
// codingWatchdogRateLimitEvidence, this is for a human reading the log, not
// for evidence comparison across polls.
func tailLines(s string, n int) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func captureTmuxPanePlain(tmuxSession string) string {
	ctx, cancel := context.WithTimeout(context.Background(), terminalTmuxActionTimeout)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "tmux", "capture-pane", "-p", "-S", "-200", "-t", tmuxSession)
	cmd.Env = append(minimalChildEnv(), passthroughChildEnv("TMUX_TMPDIR")...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	return out.String()
}

var runTmuxKill = func(ctx context.Context, tmuxSession string) error {
	return runTerminalTmuxCommand(ctx, "", "kill-session", "-t", tmuxSession)
}
