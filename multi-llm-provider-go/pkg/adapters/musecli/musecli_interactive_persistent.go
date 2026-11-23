package musecli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
	"log"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/tmuxlaunch"
	"github.com/manishiitg/multi-llm-provider-go/pkg/codingready"
)

// museMCPConfigsEquivalent compares the durable MCP surface, not per-agent
// bridge bookkeeping. A full-turn Agent instance gets a fresh virtual-tool
// trace scope and may get a fresh readiness marker; the already-running bridge
// can continue using its original values because mcpagent routes stale scopes
// to the latest registered scope for the same base session.
func museMCPConfigsEquivalent(current, requested string) bool {
	current = strings.TrimSpace(current)
	requested = strings.TrimSpace(requested)
	if current == requested {
		return true
	}
	if current == "" || requested == "" {
		return false
	}
	normalize := func(raw string) (map[string]interface{}, bool) {
		var config map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &config); err != nil {
			return nil, false
		}
		servers, _ := config["mcpServers"].(map[string]interface{})
		for _, rawServer := range servers {
			server, _ := rawServer.(map[string]interface{})
			env, _ := server["env"].(map[string]interface{})
			delete(env, "MCP_VIRTUAL_SCOPE_ID")
			delete(env, "MCP_READY_FILE")
		}
		return config, true
	}
	left, leftOK := normalize(current)
	right, rightOK := normalize(requested)
	return leftOK && rightOK && reflect.DeepEqual(left, right)
}

// Persistent tmux sessions for muse, mirroring the other coding providers:
// one live TUI per owner session id, reused across turns, killed
// explicitly. This is what the terminal tab attaches to and what mid-turn
// steering injects into. Bounded turns (the default) keep launching and
// tearing down one TUI per turn and never touch this pool.

// musePersistentSession is one pooled live TUI. restoreMCP removes the
// session-private Muse configuration created at launch.
// restoreAgents/agentsContent/agentsProjected are the same retention for a
// projected AGENTS.md system prompt: the file must exist before the TUI
// boots (muse reads project rules at startup), so projection happens on the
// fresh-launch path and the undo runs on kill.
type musePersistentSession struct {
	accountFingerprint string
	accountDataHome    string
	autoAnswer         *museAutoAnswerState
	userChoice         bool
	tmuxName           string
	workdir            string
	mcpJSON            string
	toolAllowlist      []string
	nativeSessionID    string
	// resumeNotePending: this terminal was launched with `muse resume` and the bridge is mounted; the next
	// message carries museResumeBridgeNote once. Guarded by the pool lock.
	resumeNotePending bool
	logPath           string
	// Stop can leave the interrupted prompt in Muse's composer. Record only
	// our own last submission so the next turn can clear that exact draft.
	lastSubmittedPrompt string
	stoppedPrompt       string
	// retainedBaselineSequence is the last durable Muse event that existed
	// before the most recent live-input submission. The retained-turn reader
	// only accepts assistant commits after this cursor, so an older completed
	// reply cannot settle a newly submitted follow-up.
	retainedBaselineSequence int64
	// usageFloorSequence is the last durable event that existed when the transcript was first bound in this process:
	// the retained-turn usage reader only considers runs after it, so a restart never re-reports earlier runs.
	usageFloorSequence int64
	usageFloorSet      bool
	// usageFloorAt is when the transcript was bound; native subagent usage recorded before it is never reported.
	usageFloorAt time.Time
	// pendingDurable holds one pre-send snapshot per recent live-input
	// send so the transcript arbiter and AwaitMuseInputDurable scope
	// their match above the baseline that send could have produced.
	// Guarded by the pool lock, which turns never hold while running.
	pendingDurable   []musePendingDurableAck
	retainedProgress museRetainedProgress
	restoreMCP       func()
	agentsContent    string
	restoreAgents    func()
	agentsProjected  bool
}

// museRecordPersistentTranscript binds the owner-scoped persistent TUI to the
// native Muse transcript discovered by the first real turn. Live-input turns
// reuse this file after the bounded Go call has returned.
func museRecordPersistentTranscript(owner, tmuxName, nativeSessionID, logPath string) {
	key, err := musePersistentKey(owner)
	if err != nil {
		return
	}
	musePersistentPool.Lock()
	defer musePersistentPool.Unlock()
	entry := musePersistentPool.m[key]
	if entry == nil || entry.tmuxName != tmuxName {
		return
	}
	entry.nativeSessionID = strings.TrimSpace(nativeSessionID)
	entry.logPath = strings.TrimSpace(logPath)
	if !entry.usageFloorSet && entry.logPath != "" {
		entry.usageFloorSet = true
		entry.usageFloorSequence = museTranscriptMaxSequence(entry.logPath)
		entry.usageFloorAt = time.Now()
	}
}

var musePersistentPool = struct {
	sync.Mutex
	m map[string]*musePersistentSession
}{m: make(map[string]*musePersistentSession)}

// musePersistentTurns serializes complete turns for one owner. The pool lock
// only protects the entry map; it must not be held while a turn runs. Without
// this owner gate, a follow-up whose durable MCP context changed could kill
// and relaunch the pooled tmux while the preceding call was still finishing
// its transcript, producing a misleading "session died mid-turn" failure.
var musePersistentTurns = struct {
	sync.Mutex
	m map[string]*musePersistentTurnGate
}{m: make(map[string]*musePersistentTurnGate)}

type musePersistentTurnGate struct {
	token chan struct{}
	refs  int
}

func museAcquirePersistentTurn(ctx context.Context, owner string) (func(), error) {
	key, err := musePersistentKey(owner)
	if err != nil {
		return nil, err
	}
	musePersistentTurns.Lock()
	gate := musePersistentTurns.m[key]
	if gate == nil {
		gate = &musePersistentTurnGate{token: make(chan struct{}, 1)}
		gate.token <- struct{}{}
		musePersistentTurns.m[key] = gate
	}
	gate.refs++
	musePersistentTurns.Unlock()

	select {
	case <-ctx.Done():
		musePersistentTurns.Lock()
		gate.refs--
		if gate.refs == 0 {
			delete(musePersistentTurns.m, key)
		}
		musePersistentTurns.Unlock()
		return nil, ctx.Err()
	case <-gate.token:
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			gate.token <- struct{}{}
			musePersistentTurns.Lock()
			gate.refs--
			if gate.refs == 0 {
				delete(musePersistentTurns.m, key)
			}
			musePersistentTurns.Unlock()
		})
	}, nil
}

// musePersistentKey requires an owner: pooling without one would let two
// conversations share (and overhear) a TUI. Fail fast on caller bug.
func musePersistentKey(owner string) (string, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return "", fmt.Errorf("muse-cli persistent tmux requires an owner session id (WithMuseInteractiveSessionID)")
	}
	return owner, nil
}

// museInteractiveSessionPrefix is the tmux session-name prefix for pooled
// muse TUIs, matching the <provider>InteractiveSessionPrefix convention the
// root package's orphan sweep keeps in sync.
func museInteractiveSessionPrefix() string {
	return "mlp-muse-"
}

// musePersistentTmuxName derives a stable, tmux-safe session name from the
// owner so the terminal tab and diagnostics can find the pane
// deterministically.
func musePersistentTmuxName(owner string) string {
	var b strings.Builder
	b.WriteString(museInteractiveSessionPrefix())
	for _, r := range strings.ToLower(owner) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	name := b.String()
	if len(name) > 48 {
		name = name[:48]
	}
	return strings.Trim(name, "-")
}

// museClosePersistentTmux cleans up a pane even when the turn that owned it
// was canceled. A canceled request context cannot run tmux kill-session, and
// leaving the deterministic name behind makes every later resume fail with
// "duplicate session". Only the exact Muse-owned session is targeted.
func museClosePersistentTmux(tmuxName string) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	target := "=" + tmuxName
	if !museTmuxSessionAlive(cleanupCtx, target) {
		return nil
	}
	out, err := exec.CommandContext(cleanupCtx, "tmux", "kill-session", "-t", target).CombinedOutput()
	if err != nil && museTmuxSessionAlive(cleanupCtx, target) {
		return fmt.Errorf("close Muse tmux session %q: %w: %s", tmuxName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// museKillPersistentLocked kills the entry's tmux session and runs its
// retained settings restore. Caller holds the pool lock.
func museKillPersistentLocked(ctx context.Context, entry *musePersistentSession) {
	if entry == nil {
		return
	}
	if entry.autoAnswer != nil {
		entry.autoAnswer.stopped.Store(true)
	}
	_ = museClosePersistentTmux(entry.tmuxName)
	if entry.restoreMCP != nil {
		entry.restoreMCP()
	}
	if entry.restoreAgents != nil {
		entry.restoreAgents()
	}
}

// KillMusePersistentSession tears down one pooled TUI and restores the
// user's settings.json. The orchestrator calls this when the conversation
// ends; unknown owners are a no-op, never an error.
func KillMusePersistentSession(owner string) {
	key, err := musePersistentKey(owner)
	if err != nil {
		return
	}
	musePersistentPool.Lock()
	defer musePersistentPool.Unlock()
	entry := musePersistentPool.m[key]
	delete(musePersistentPool.m, key)
	museKillPersistentLocked(context.Background(), entry)
}

// CleanupMuseCLIInteractiveSessions tears down every pooled muse TUI
// registered by this process, running each entry's retained settings and
// AGENTS.md restores. Same bulk-sweep shape as codex/cursor/pi/claude's
// Cleanup*InteractiveSessions; the workflow P0 harness calls it between
// providers so no live pane leaks across matrix entries. No tmux binary is
// a no-op (returns nil), never an error.
func CleanupMuseCLIInteractiveSessions(ctx context.Context) error {
	closeAllMuseMSPSessions()
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil
	}
	musePersistentPool.Lock()
	entries := make([]*musePersistentSession, 0, len(musePersistentPool.m))
	for _, entry := range musePersistentPool.m {
		entries = append(entries, entry)
	}
	musePersistentPool.m = make(map[string]*musePersistentSession)
	musePersistentPool.Unlock()
	for _, entry := range entries {
		museKillPersistentLocked(ctx, entry)
	}
	return nil
}

// CloseMuseCLIInteractiveSessionForOwner is KillMusePersistentSession under
// the naming convention the other tmux-backed providers use
// (Close<Provider>InteractiveSessionForOwner), so the root package's
// provider-agnostic re-export layer can call it the same way it calls
// pi-cli/cursor-cli/codex-cli/claude-code's. reason is accepted for
// interface parity; muse's teardown (tmux kill-session + settings/AGENTS.md
// restore) doesn't vary by reason the way claude's exit-sequence choice does.
func CloseMuseCLIInteractiveSessionForOwner(owner, reason string) {
	_ = reason
	KillMusePersistentSession(owner)
	CloseMuseMSPSession(owner)
}

// CloseMuseCLIInteractiveSessionByTmux tears down a persistent muse session
// by its tmux session name rather than owner key -- a teardown backstop when
// the owning session ID is unknown or has drifted (e.g. workflow sub-agents
// registered under a step-execution owner the caller can't reconstruct).
// Falls back to a raw kill-session when no pooled entry matches the name, so
// the tmux session never lingers regardless of whether the pool still knows
// about it.
func CloseMuseCLIInteractiveSessionByTmux(tmuxSessionName, reason string) {
	_ = reason
	tmuxSessionName = strings.TrimSpace(tmuxSessionName)
	if tmuxSessionName == "" {
		return
	}
	musePersistentPool.Lock()
	var entry *musePersistentSession
	for owner, candidate := range musePersistentPool.m {
		if candidate != nil && candidate.tmuxName == tmuxSessionName {
			entry = candidate
			delete(musePersistentPool.m, owner)
			break
		}
	}
	musePersistentPool.Unlock()
	if entry != nil {
		museKillPersistentLocked(context.Background(), entry)
		return
	}
	_ = museClosePersistentTmux(tmuxSessionName)
}

// museAcquirePersistentSession returns the live pooled TUI for owner,
// launching it when absent or dead. created reports a fresh launch (the
// caller still waits for settle + MCP readiness on it). A retained entry
// whose workdir moved is relaunched; a retained mount that differs from
// the requested one fails fast rather than running the turn with the
// wrong tools mounted. A retained entry whose projected system prompt
// differs is relaunched too: a running TUI may not re-read AGENTS.md, so
// the file is projected fresh before every boot and never rewritten under
// a live session. Durable MCP changes follow the same lifecycle. Transient
// bridge trace/readiness values do not force a relaunch.
//
// systemPrompt carries the file-only system text when the caller opted into
// project-instruction-only mode; empty disables projection. projectAgents
// reports whether AGENTS.md was projected for THIS turn — only then may the
// caller skip typing the preamble inline. A projection failure is
// best-effort (the turn falls back to inline), never a session-killer.
func museAcquirePersistentSession(ctx context.Context, owner, workdir, provider, modelID, mcpJSON string, toolAllowlist []string, readyFile, systemPrompt string, projectAgents, restoreAgentsFile bool, resumeNativeID string) (*musePersistentSession, bool, error) {
	key, err := musePersistentKey(owner)
	if err != nil {
		return nil, false, err
	}
	wantAgents := projectAgents && strings.TrimSpace(systemPrompt) != ""
	musePersistentPool.Lock()
	entry := musePersistentPool.m[key]
	if entry != nil {
		if entry.accountFingerprint != llmtypes.CodingAgentScopeFingerprint(museAccount(ctx).opts) {
			museKillPersistentLocked(ctx, entry)
			delete(musePersistentPool.m, key)
			entry = nil
		} else if entry.workdir != workdir {
			museKillPersistentLocked(ctx, entry)
			delete(musePersistentPool.m, key)
			entry = nil
		} else if !museTmuxSessionAlive(ctx, entry.tmuxName) {
			museKillPersistentLocked(ctx, entry)
			delete(musePersistentPool.m, key)
			entry = nil
		} else if wantAgents && entry.agentsContent != strings.TrimSpace(systemPrompt) {
			museKillPersistentLocked(ctx, entry)
			delete(musePersistentPool.m, key)
			entry = nil
		} else if strings.Contains(entry.mcpJSON, "mcpbridge") && museBridgeGone(ctx, entry.tmuxName) {
			// Muse drops its MCP connection for good once a tool call hits its own limit, and the bridge exits
			// with it: this session would answer every call with "MCP stdio connection is closed" until someone
			// reset it. Relaunch (native resume keeps the conversation) between turns, before the message is sent.
			museKillPersistentLocked(ctx, entry)
			delete(musePersistentPool.m, key)
			entry = nil
		} else if !museMCPConfigsEquivalent(entry.mcpJSON, mcpJSON) {
			museKillPersistentLocked(ctx, entry)
			delete(musePersistentPool.m, key)
			entry = nil
		} else if (entry.toolAllowlist == nil) != (toolAllowlist == nil) || !slices.Equal(entry.toolAllowlist, toolAllowlist) {
			museKillPersistentLocked(ctx, entry)
			delete(musePersistentPool.m, key)
			entry = nil
		}
	}
	if entry != nil {
		musePersistentPool.Unlock()
		return entry, false, nil
	}
	musePersistentPool.Unlock()

	// Project AGENTS.md BEFORE boot: muse reads project rules at startup,
	// so a file written after launch would miss the first turn.
	var restoreAgents func()
	projected := false
	if wantAgents {
		if restore, perr := writeMuseProjectAgentsFile(workdir, strings.TrimSpace(systemPrompt), restoreAgentsFile); perr != nil {
			// Best-effort: fall back to inline typing below.
		} else {
			restoreAgents, projected = restore, true
		}
	}
	tmuxName := musePersistentTmuxName(owner)
	// The pool is process-local but tmux survives a server restart. A canceled
	// launch can also leave a pane before it is inserted into the pool. In both
	// cases this exact owner name is untracked and must be cleared before a
	// native resume creates its replacement.
	if err := museClosePersistentTmux(tmuxName); err != nil {
		if restoreAgents != nil {
			restoreAgents()
		}
		return nil, false, err
	}
	// A dead entry relaunches here (reuse returned early above), so a
	// caller-supplied native id resumes the conversation instead of
	// starting cold — this is the continuity-after-loss path.
	restore, err := museLaunchPersistentTUI(ctx, tmuxName, workdir, provider, modelID, mcpJSON, toolAllowlist, strings.TrimSpace(resumeNativeID))
	if err != nil {
		if restoreAgents != nil {
			restoreAgents()
		}
		return nil, false, err
	}
	entry = &musePersistentSession{accountFingerprint: llmtypes.CodingAgentScopeFingerprint(museAccount(ctx).opts), accountDataHome: museAccountDataHome(ctx), nativeSessionID: strings.TrimSpace(resumeNativeID), logPath: museSessionLogPath(resumeNativeID, museAccountDataHome(ctx)), autoAnswer: &museAutoAnswerState{}, tmuxName: tmuxName, workdir: workdir, mcpJSON: mcpJSON, toolAllowlist: slices.Clone(toolAllowlist), restoreMCP: restore}
	entry.resumeNotePending = strings.TrimSpace(resumeNativeID) != "" && (museMCPJSONHasBridge(mcpJSON) || sessiontransfer.MuseHandoffWorkspace(museAccountDataHome(ctx), resumeNativeID, workdir))
	if wantAgents {
		// Remember the requested prompt even when projection fell back to
		// inline. Otherwise an oversized prompt would relaunch the same
		// retained terminal on every turn because agentsContent was empty.
		entry.agentsContent = strings.TrimSpace(systemPrompt)
	}
	if projected {
		entry.restoreAgents, entry.agentsProjected = restoreAgents, true
	}
	// A new terminal is not necessarily a new conversation: native resume
	// replays history, which can scroll the startup banner off-screen before
	// our first capture. Keep the banner requirement only for fresh sessions.
	waitReady := museWaitSettled
	if strings.TrimSpace(resumeNativeID) != "" {
		waitReady = museWaitAtPrompt
	}
	if _, err := waitReady(ctx, tmuxName, 90*time.Second); err != nil {
		restore()
		if restoreAgents != nil {
			restoreAgents()
		}
		_ = museClosePersistentTmux(tmuxName)
		return nil, false, err
	}
	if readyFile != "" {
		codingready.WaitForMCPReadyFile(ctx, readyFile, codingready.MCPReadyWait())
	}
	musePersistentPool.Lock()
	musePersistentPool.m[key] = entry
	musePersistentPool.Unlock()
	return entry, true, nil
}

// museLaunchPersistentTUI boots one TUI for pool ownership and returns the
// cleanup of the session-private configuration. resumeNativeID
// relaunches into a previous native session after a backend restart
// (`muse resume <uuid>`); empty starts fresh.
func museLaunchPersistentTUI(ctx context.Context, tmuxName, workdir, provider, modelID, mcpJSON string, toolAllowlist []string, resumeNativeID string) (func(), error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil, fmt.Errorf("tmux not found in PATH; muse-cli tmux mode requires tmux: %w", err)
	}
	if _, err := exec.LookPath("muse"); err != nil {
		return nil, fmt.Errorf("muse CLI not in PATH: %w", err)
	}
	configHome, restore, err := museAccountConfig(ctx, strings.TrimSpace(mcpJSON), toolAllowlist)
	if err != nil {
		return nil, err
	}
	argv := []string{"--trust-workspace", "--provider", provider}
	if sessiontransfer.MuseHandoffWorkspace(museAccountDataHome(ctx), resumeNativeID, workdir) {
		argv = append(argv, "--workspace", workdir)
	}
	if strings.TrimSpace(modelID) != "" {
		argv = append(argv, "--model", strings.TrimSpace(modelID))
	}
	if strings.TrimSpace(mcpJSON) != "" {
		// museTUIApprovalArgv covers mounted turns: MCP-server tools gate
		// on approval while built-in shell tools do not.
		argv = append(argv, museTUIApprovalArgv()...)
	}
	argv = append(argv, museToolArgv(toolAllowlist != nil)...)
	effortArgv, err := museEffortArgv(ctx)
	if err != nil {
		restore()
		return nil, err
	}
	argv = append(argv, effortArgv...)
	cli := append([]string{"env", "XDG_CONFIG_HOME=" + configHome, "XDG_DATA_HOME=" + museAccountDataHome(ctx), "muse"}, argv...)
	if id := strings.TrimSpace(resumeNativeID); id != "" {
		cli = []string{"env", "XDG_CONFIG_HOME=" + configHome, "XDG_DATA_HOME=" + museAccountDataHome(ctx), "muse", "resume", id}
		cli = append(cli, argv...)
	}
	shell, cleanupLaunch, err := museAccountLaunch(ctx, cli, workdir)
	if err != nil {
		restore()
		return nil, err
	}
	defer func() { time.AfterFunc(30*time.Second, cleanupLaunch) }()
	launch := exec.CommandContext(ctx, "tmux", append([]string{"new-session", "-d", "-s", tmuxName, "-x", "200", "-y", "50", "-c", workdir}, shell)...)
	launch.Env = tmuxlaunch.CleanClientEnv()
	if out, err := launch.CombinedOutput(); err != nil {
		if restore != nil {
			restore()
		}
		return nil, fmt.Errorf("tmux new-session: %w\n%s", err, out)
	}
	return restore, nil
}

// musePersistentReadyFile extracts the MCP readiness-marker path the same
// way the bounded lane does.
func musePersistentReadyFile(opts *llmtypes.CallOptions) string {
	if opts == nil || opts.Metadata == nil {
		return ""
	}
	return codingready.MCPReadyFileFromMetadata(opts.Metadata.Custom)
}

// museBridgeGone is true only when the session's bridge is known to be missing; when that cannot be told the
// session is left alone.
func museBridgeGone(ctx context.Context, tmuxName string) bool {
	alive, known := museBridgeAlive(ctx, tmuxName)
	if !known || alive {
		return false
	}
	log.Printf("[muse-cli] the MCP bridge of %s is gone (Muse closed it after a tool-call timeout); relaunching the session with native resume", tmuxName)
	return true
}
