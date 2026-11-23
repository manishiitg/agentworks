package agycli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/tmuxexec"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/tmuxlaunch"
)

// Interactive tmux lane for agy. Turns normally run headless through the
// exec lane; the TUI sidecar exists for live follow-up input, interrupt,
// and reply display. Markers verified live against agy 1.2.7:
//   - ready: bottom bar shows "? for shortcuts" with a ">" prompt
//   - busy: bottom bar shows "esc to cancel"
//   - trust gate (untrusted cwd): "Do you trust the contents of this
//     project?" with "> Yes, I trust this folder" preselected
//   - interrupt: "Interrupted · What should Antigravity CLI do instead?"
//
// Lane notes: turn prompts ride bracketed paste (multiline, proven live);
// follow-up live input uses bracketed paste for multiline prompts. Each sidecar owns one
// persistent TUI-native conversation across its turns.

const (
	agyPaneReadyMarker       = "? for shortcuts"
	agyPaneBusyMarker        = "esc to cancel"
	agyPaneTrustGateMarker   = "Do you trust the contents of this project?"
	agyPaneApprovalMarker    = "Requesting permission for:"
	agyPaneInterruptedMarker = "Interrupted"
)

type agyInteractiveSession struct {
	ownerSessionID  string
	tmuxSessionName string
	workingDir      string
	transcriptHome  string
	createdAt       time.Time
	// model is the --model the sidecar booted with ("" = CLI default).
	model string
	// mountFingerprint identifies the tool surface ("unmounted" or a
	// mounted-<hash>); a changed fingerprint reboots the sidecar.
	mountFingerprint string
	// releaseMounts removes this sidecar's private AGY home after it exits.
	releaseMounts   func()
	releaseToolHook func()
	// turnLeases tracks in-flight turns using this retained sidecar.
	turnLeases atomic.Int32
	// conversationID is the TUI-native conversation, discovered by
	// content match after the first turn; "" until then.
	conversationMu sync.RWMutex
	conversationID string
	// Live-input sends are serialized so each durability receipt snapshots
	// the conversation before its own Enter. The receipt watcher reads the
	// CLI's SQLite user rows after the fast tmux send returns.
	sendMu         sync.Mutex
	durableMu      sync.Mutex
	pendingDurable []agyPendingDurableAck
	retainedMu     sync.Mutex
	retainedState  agyRetainedState
	// compactionScanIdx is one past the newest step already scanned for
	// context compactions (0 = not scanned yet), so each emits once.
	compactionScanIdx atomic.Int64
}

func (session *agyInteractiveSession) getConversationID() string {
	session.conversationMu.RLock()
	defer session.conversationMu.RUnlock()
	return session.conversationID
}

func (session *agyInteractiveSession) setConversationIDIfEmpty(id string) string {
	session.conversationMu.Lock()
	defer session.conversationMu.Unlock()
	if session.conversationID == "" {
		session.conversationID = id
	}
	return session.conversationID
}

var agyInteractiveRegistry = struct {
	sync.Mutex
	sessions map[string]*agyInteractiveSession
}{sessions: map[string]*agyInteractiveSession{}}

func agySanitizeTmuxName(owner string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(owner)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if len(name) > 32 {
		name = name[:32]
	}
	if name == "" {
		name = "owner"
	}
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	return "agy-int-" + name + "-" + hex.EncodeToString(suffix)
}

func activeAgyInteractiveSession(ownerSessionID string) (*agyInteractiveSession, bool) {
	agyInteractiveRegistry.Lock()
	defer agyInteractiveRegistry.Unlock()
	session := agyInteractiveRegistry.sessions[strings.TrimSpace(ownerSessionID)]
	return session, session != nil && strings.TrimSpace(session.tmuxSessionName) != ""
}

// AgyInteractiveSessionActive reports whether the owner has a live sidecar
// registered (booted past pane-ready and not yet closed). Steer rows poll
// this: TurnInFlight fires at turn start (mount phase), well before the
// sidecar exists to receive live input.
func AgyInteractiveSessionActive(ownerSessionID string) bool {
	_, ok := activeAgyInteractiveSession(ownerSessionID)
	return ok
}

func captureAgyPane(ctx context.Context, sessionName string) (string, error) {
	return tmuxexec.CapturePane(ctx, sessionName, 5000)
}

// agyPaneStatusRows excludes scrollback and earlier answer text. Status and
// approval controls render at the bottom of the 50-row sidecar pane.
func agyPaneStatusRows(captured string) string {
	lines := strings.Split(strings.TrimRight(captured, "\n"), "\n")
	if len(lines) > 10 {
		lines = lines[len(lines)-10:]
	}
	return strings.Join(lines, "\n")
}

// PaneReadyForInput reports whether a captured agy pane is settled at the
// prompt: the ready bar is up and no busy/interrupt marker shows.
func PaneReadyForInput(captured string) bool {
	status := agyPaneStatusRows(captured)
	return strings.Contains(status, agyPaneReadyMarker) && !strings.Contains(status, agyPaneBusyMarker)
}

func agyPaneShowsTrustGate(captured string) bool {
	return strings.Contains(agyPaneStatusRows(captured), agyPaneTrustGateMarker)
}

// agyTrustMu serializes ALL settings.json read-modify-write: workspace trust,
// the key-mode flip, AND the permissions.allow edits. The allow edits used
// to ride the mount mutex instead, and the two domains' whole-file writes
// clobbered each other (proven live: concurrent rows leaked trust entries
// while the trust log stayed coherent). Lock order is always mountMu, then
// this mutex — never the reverse — so sharing it cannot deadlock.
var agyTrustMu sync.Mutex

// AgyP0KeyModeEnv opts P0 runs into Gemini API key mode. Without it, agy
// runs stay on stored-login subscription truth even when a key happens to be
// exported (agy ignores the key unless modelProvider flips — which only this
// flag authorizes the test harness to do).
const AgyP0KeyModeEnv = "AGY_P0_KEY_MODE"

// AgyKeyModeRequested reports whether P0 key mode is opted in.
func AgyKeyModeRequested() bool {
	return strings.TrimSpace(os.Getenv(AgyP0KeyModeEnv)) == "1"
}

// agyKeyModeState tracks the key-mode flip across concurrent holders (the
// concurrency e2e flips from two goroutines). Added-ness belongs to the
// FLIP (global), not to any one holder: whichever holder added it may
// release first, so the last holder out removes the flip iff this session
// added it. All fields under agyTrustMu.
var agyKeyModeState = struct {
	holders int
	added   bool
	raw     []byte
}{}

// AgyEnsureKeyMode flips settings.json to modelProvider "gemini" when
// AGY_P0_KEY_MODE=1 (no-op otherwise), so agy bills the run to GEMINI_API_KEY
// instead of the stored-login subscription. A requested-but-keyless flip
// fails loudly, as does fighting a user-set non-gemini provider. The restore
// func removes only the flip this call added; concurrent holders refcount.
// Callers (tests) defer restore per row; production never calls this.
func AgyEnsureKeyMode() (restore func(), err error) {
	noop := func() {}
	if !AgyKeyModeRequested() {
		return noop, nil
	}
	if strings.TrimSpace(os.Getenv("GEMINI_API_KEY")) == "" && strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")) == "" {
		return nil, fmt.Errorf("AGY_P0_KEY_MODE=1 but neither GEMINI_API_KEY nor GOOGLE_API_KEY is set")
	}
	agyTrustMu.Lock()
	defer agyTrustMu.Unlock()
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, fmt.Errorf("agy key-mode home dir: %w", err)
	}
	settingsPath := agySettingsPath(home)
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil, fmt.Errorf("read agy settings: %w", err)
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, fmt.Errorf("parse agy settings: %w", err)
	}
	if current, present := settings["modelProvider"]; present {
		if s, _ := current.(string); s != "gemini" {
			return nil, fmt.Errorf("agy key mode needs modelProvider gemini but settings.json sets %q — refusing to fight user config", s)
		}
		agyKeyModeState.holders++
		return func() { agyReleaseKeyMode(settingsPath) }, nil
	}
	settings["modelProvider"] = "gemini"
	merged, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal agy settings: %w", err)
	}
	if err := agyWriteSettingsAtomic(settingsPath, append(merged, '\n')); err != nil {
		return nil, fmt.Errorf("flip agy modelProvider: %w", err)
	}
	agyKeyModeState.holders++
	if !agyKeyModeState.added {
		agyKeyModeState.added = true
		agyKeyModeState.raw = raw
	}
	return func() { agyReleaseKeyMode(settingsPath) }, nil
}

// agyReleaseKeyMode drops one key-mode hold; the last holder removes the flip
// iff this session added it (a pre-existing modelProvider is never touched),
// restoring raw byte-identical when the flip was the only change.
// Best-effort.
func agyReleaseKeyMode(settingsPath string) {
	agyTrustMu.Lock()
	defer agyTrustMu.Unlock()
	if agyKeyModeState.holders > 0 {
		agyKeyModeState.holders--
	}
	if agyKeyModeState.holders > 0 || !agyKeyModeState.added {
		return
	}
	raw := agyKeyModeState.raw
	agyKeyModeState.added = false
	agyKeyModeState.raw = nil
	current, err := os.ReadFile(settingsPath)
	if err != nil {
		return
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(current, &settings); err != nil {
		return
	}
	if s, _ := settings["modelProvider"].(string); s != "gemini" {
		return
	}
	delete(settings, "modelProvider")
	var before map[string]interface{}
	if json.Unmarshal(raw, &before) == nil {
		// The raw fast path must ALSO match the trust list, not just the
		// non-trust keys: concurrent rows untrust between the flip and this
		// restore, and a blind raw write resurrects their removed entries
		// (proven live: single workdir entry leaked per concurrent run while
		// the trust log stayed coherent). Mismatch falls through to the
		// always-correct reformat below.
		if _, present := before["modelProvider"]; !present && agySettingsEqualExceptTrust(before, settings) {
			beforeTrusted, _ := before["trustedWorkspaces"].([]interface{})
			currentTrusted, _ := settings["trustedWorkspaces"].([]interface{})
			if agyStringListEqual(beforeTrusted, currentTrusted) {
				agyTrustDebugf("keymode restore raw")
				_ = agyWriteSettingsAtomic(settingsPath, raw)
				return
			}
			agyTrustDebugf("keymode restore reformat (trust changed under flip)")
		}
	}
	merged, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return
	}
	_ = agyWriteSettingsAtomic(settingsPath, append(merged, '\n'))
}

// AgyTestAuthMode names the auth mode under test for review facts.
func AgyTestAuthMode() string {
	if AgyKeyModeRequested() {
		return "gemini_api_key (AGY_P0_KEY_MODE; subscription quota exhausted, transport mechanics unaffected)"
	}
	if os.Getenv("GEMINI_API_KEY") != "" {
		return "gemini_api_key (isolated AGY home)"
	}
	return "stored_login (subscription OAuth)"
}

// TrustAgyWorkspaceDir grants the sidecar lane's caller-duty trust for dir:
// agy trust is exact-path (subdirs of a trusted dir are NOT trusted) and a
// sidecar booted in an untrusted cwd fails loudly on the trust gate instead
// of auto-answering it. Callers that only need trust for one session defer
// the returned restore func.
//
// Restore removes ONLY the entry this call added (a pre-existing entry,
// including the user's own, is never touched) and puts settings.json back
// byte-identical when nothing else changed it. Concurrent trust of the SAME
// dir is not supported — callers use distinct dirs per session.
func TrustAgyWorkspaceDir(dir string) (restore func(), err error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("agy trust needs a directory")
	}
	agyTrustMu.Lock()
	defer agyTrustMu.Unlock()
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, fmt.Errorf("agy trust home dir: %w", err)
	}
	settingsPath := agySettingsPath(home)
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil, fmt.Errorf("read agy settings: %w", err)
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, fmt.Errorf("parse agy settings: %w", err)
	}
	trusted, _ := settings["trustedWorkspaces"].([]interface{})
	for _, entry := range trusted {
		if s, _ := entry.(string); s == dir {
			return func() {}, nil
		}
	}
	settings["trustedWorkspaces"] = append(trusted, dir)
	merged, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal agy settings: %w", err)
	}
	if err := agyWriteSettingsAtomic(settingsPath, append(merged, '\n')); err != nil {
		return nil, fmt.Errorf("trust agy workdir: %w", err)
	}
	agyTrustDebugf("trust +%s", dir)
	return func() { agyUntrustWorkspaceDir(settingsPath, raw, dir) }, nil
}

func agyTrustDebugf(format string, args ...interface{}) {
	if os.Getenv("AGY_TRUST_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[agytrust] "+format+"\n", args...)
	}
}

// agyUntrustWorkspaceDir removes dir from trustedWorkspaces, restoring raw
// byte-identical when dir is the only change. Best-effort: trust leftovers
// fail open (an extra trusted scratch dir), never fail a turn.
func agyUntrustWorkspaceDir(settingsPath string, raw []byte, dir string) {
	agyTrustMu.Lock()
	defer agyTrustMu.Unlock()
	current, err := os.ReadFile(settingsPath)
	if err != nil {
		return
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(current, &settings); err != nil {
		return
	}
	trusted, _ := settings["trustedWorkspaces"].([]interface{})
	kept := make([]interface{}, 0, len(trusted))
	removed := false
	for _, entry := range trusted {
		if s, _ := entry.(string); !removed && s == dir {
			removed = true
			continue
		}
		kept = append(kept, entry)
	}
	if !removed {
		agyTrustDebugf("untrust -%s NOT-FOUND", dir)
		return
	}
	settings["trustedWorkspaces"] = kept
	// Byte-identical fast path: dir was the only change since trust.
	var before map[string]interface{}
	if json.Unmarshal(raw, &before) == nil && agySettingsEqualExceptTrust(before, settings) {
		if beforeTrusted, _ := before["trustedWorkspaces"].([]interface{}); agyStringListEqual(beforeTrusted, kept) {
			agyTrustDebugf("untrust -%s fastpath", dir)
			_ = agyWriteSettingsAtomic(settingsPath, raw)
			return
		}
	}
	merged, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return
	}
	agyTrustDebugf("untrust -%s slowpath kept=%d", dir, len(kept))
	_ = agyWriteSettingsAtomic(settingsPath, append(merged, '\n'))
}

func agySettingsEqualExceptTrust(a, b map[string]interface{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		if k == "trustedWorkspaces" {
			continue
		}
		bv, ok := b[k]
		if !ok {
			return false
		}
		aj, _ := json.Marshal(av)
		bj, _ := json.Marshal(bv)
		if string(aj) != string(bj) {
			return false
		}
	}
	return true
}

func agyStringListEqual(a, b []interface{}) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		as, _ := a[i].(string)
		bs, _ := b[i].(string)
		if as != bs {
			return false
		}
	}
	return true
}

func agyTmuxSendKeys(ctx context.Context, sessionName string, args ...string) error {
	full := append([]string{"send-keys", "-t", sessionName}, args...)
	if out, err := exec.CommandContext(ctx, "tmux", full...).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys %s: %w\n%s", sessionName, err, out)
	}
	return nil
}

// waitAgyPaneReady polls until the pane settles at the prompt. A trust gate
// FAILS LOUDLY (muse parity): silently auto-trusting a workspace the user
// has not approved would defeat the gate, so trust is the caller's job and
// the lane reports the pane instead. Readiness needs two consecutive ready
// polls so a mid-render frame with a stale bar cannot pass.
func waitAgyPaneReady(ctx context.Context, sessionName string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	readyStreak := 0
	var last string
	for {
		if time.Now().After(deadline) {
			return last, fmt.Errorf("timed out waiting for agy pane ready; latest pane:\n%s", last)
		}
		pane, err := captureAgyPane(ctx, sessionName)
		if err != nil {
			return last, fmt.Errorf("capture agy pane %s: %w", sessionName, err)
		}
		last = pane
		if agyApprovalMarkerShown(pane) != "" {
			return last, fmt.Errorf("agy pane blocked on a tool approval gate the unattended lane cannot answer; pane:\n%s", pane)
		}
		if agyPaneShowsTrustGate(pane) {
			return last, fmt.Errorf("agy TUI blocked on a workspace trust gate (trust is the caller's job; pre-trust the directory before booting a sidecar); pane:\n%s", pane)
		}
		if PaneReadyForInput(pane) {
			readyStreak++
			if readyStreak >= 2 {
				return pane, nil
			}
		} else {
			readyStreak = 0
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(1500 * time.Millisecond):
		}
	}
}

// waitAgyPromptEcho polls until the submitted prompt's echo appears in the
// pane: the TUI accepted it as a turn. Callers wait for the echo before
// waitAgyPaneReady, so a ready frame in the gap between queued turns cannot
// fake completion of a turn that has not started yet.
func waitAgyPromptEcho(ctx context.Context, sessionName, snippet string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		pane, err := captureAgyPane(ctx, sessionName)
		if err != nil {
			return fmt.Errorf("capture agy pane %s: %w", sessionName, err)
		}
		if strings.Contains(pane, snippet) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for prompt echo %q; latest pane:\n%s", snippet, pane)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// ensureAgyInteractiveSession boots the owner's TUI sidecar when none is
// registered and waits for the prompt.
func ensureAgyInteractiveSession(ctx context.Context, ownerSessionID, workingDir string) (*agyInteractiveSession, error) {
	ownerSessionID = strings.TrimSpace(ownerSessionID)
	if ownerSessionID == "" {
		return nil, fmt.Errorf("agy interactive session needs an owner session id")
	}
	if session, ok := activeAgyInteractiveSession(ownerSessionID); ok {
		return session, nil
	}
	tmuxName := agySanitizeTmuxName(ownerSessionID)
	launch := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", tmuxName,
		"-x", "200", "-y", "50", "-c", workingDir, "agy")
	launch.Env = tmuxlaunch.CleanClientEnv()
	if out, err := launch.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("tmux new-session %s: %w\n%s", tmuxName, err, out)
	}
	session := &agyInteractiveSession{
		ownerSessionID:  ownerSessionID,
		tmuxSessionName: tmuxName,
		workingDir:      workingDir,
		createdAt:       time.Now(),
	}
	agyInteractiveRegistry.Lock()
	agyInteractiveRegistry.sessions[ownerSessionID] = session
	agyInteractiveRegistry.Unlock()
	if _, err := waitAgyPaneReady(ctx, tmuxName, 90*time.Second); err != nil {
		CloseAgyCLIInteractiveSessionForOwner(ownerSessionID, "boot failed")
		return nil, err
	}
	return session, nil
}

// sendAgyInteractiveMessage submits one prompt into the owner's sidecar.
// Multiline input uses bracketed paste so embedded newlines stay one turn.
func sendAgyInteractiveMessage(ctx context.Context, ownerSessionID, message string) error {
	session, ok := activeAgyInteractiveSession(ownerSessionID)
	if !ok {
		return fmt.Errorf("no agy interactive session for owner %q", ownerSessionID)
	}
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("agy interactive input is empty")
	}
	session.sendMu.Lock()
	defer session.sendMu.Unlock()
	conversationID := session.getConversationID()
	if conversationID == "" {
		conversationID = agyConversationIDFromPane(ctx, session.tmuxSessionName)
	}
	baseline := agyConversationMaxIdx(conversationID, session.transcriptHome)
	if strings.Contains(message, "\n") {
		if err := agyPasteToSidecar(ctx, session.tmuxSessionName, message); err != nil {
			return err
		}
	} else {
		if err := agyTmuxSendKeys(ctx, session.tmuxSessionName, "-l", message); err != nil {
			return err
		}
	}
	if err := agyTmuxSendKeys(ctx, session.tmuxSessionName, "Enter"); err != nil {
		return err
	}
	agyStashDurableAck(session, message, conversationID, baseline)
	return nil
}

// CleanupAgyCLIInteractiveSessions kills every agy tmux sidecar registered
// by this process.
func CleanupAgyCLIInteractiveSessions(ctx context.Context) error {
	closeAllAgyACPSessions()
	agyInteractiveRegistry.Lock()
	sessions := make([]*agyInteractiveSession, 0, len(agyInteractiveRegistry.sessions))
	for _, session := range agyInteractiveRegistry.sessions {
		sessions = append(sessions, session)
	}
	agyInteractiveRegistry.sessions = map[string]*agyInteractiveSession{}
	agyInteractiveRegistry.Unlock()
	var errs []string
	for _, session := range sessions {
		if out, err := exec.CommandContext(ctx, "tmux", "kill-session", "-t", session.tmuxSessionName).CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v\n%s", session.tmuxSessionName, err, out))
		}
		agyReleaseSessionMounts(session)
	}
	if len(errs) > 0 {
		return fmt.Errorf("cleanup agy interactive sessions: %s", strings.Join(errs, "; "))
	}
	return nil
}

// CloseAgyCLIInteractiveSessionForOwner tears down the sidecar owned by
// ownerSessionID. No-op when none is registered.
func CloseAgyCLIInteractiveSessionForOwner(ownerSessionID, reason string) {
	CloseAgyACPSession(strings.TrimSpace(ownerSessionID))
	agyInteractiveRegistry.Lock()
	session, ok := agyInteractiveRegistry.sessions[strings.TrimSpace(ownerSessionID)]
	if ok {
		delete(agyInteractiveRegistry.sessions, strings.TrimSpace(ownerSessionID))
	}
	agyInteractiveRegistry.Unlock()
	if !ok {
		return
	}
	_ = exec.CommandContext(context.Background(), "tmux", "kill-session", "-t", session.tmuxSessionName).Run()
	agyReleaseSessionMounts(session)
}

// CloseAgyCLIInteractiveSessionByTmux tears down a sidecar by tmux name.
// No-op when no live session matches.
func CloseAgyCLIInteractiveSessionByTmux(tmuxSessionName, reason string) {
	agyInteractiveRegistry.Lock()
	var doomed []*agyInteractiveSession
	for owner, session := range agyInteractiveRegistry.sessions {
		if session.tmuxSessionName == tmuxSessionName {
			delete(agyInteractiveRegistry.sessions, owner)
			doomed = append(doomed, session)
		}
	}
	agyInteractiveRegistry.Unlock()
	_ = exec.CommandContext(context.Background(), "tmux", "kill-session", "-t", tmuxSessionName).Run()
	for _, session := range doomed {
		agyReleaseSessionMounts(session)
	}
}

// agyReleaseSessionMounts drops a dead session's workspace tool hook and
// mount hold; the last holder unmounts and removes permission entries.
func agyReleaseSessionMounts(session *agyInteractiveSession) {
	if session == nil {
		return
	}
	if session.releaseToolHook != nil {
		release := session.releaseToolHook
		session.releaseToolHook = nil
		release()
	}
	if session.releaseMounts != nil {
		release := session.releaseMounts
		session.releaseMounts = nil
		release()
	}
}

// SendAgyInteractiveInput sends one single-line follow-up to the owner's
// live sidecar. The owner must have a registered session (booted on a
// persistent turn); input sent mid-turn queues behind it.
func SendAgyInteractiveInput(ctx context.Context, sessionID, message string) error {
	return sendAgyInteractiveMessage(ctx, sessionID, message)
}

// SendAgyInteractiveControlKey injects Escape (interrupt the running turn)
// or Enter (confirm the focused gate option) into the owner's sidecar.
func SendAgyInteractiveControlKey(ctx context.Context, sessionID, key string) error {
	session, ok := activeAgyInteractiveSession(sessionID)
	if !ok {
		return fmt.Errorf("no agy interactive session for owner %q", sessionID)
	}
	switch key {
	case "Escape", "Enter":
		return agyTmuxSendKeys(ctx, session.tmuxSessionName, key)
	default:
		return fmt.Errorf("agy interactive control key %q not supported (want Escape|Enter)", key)
	}
}
