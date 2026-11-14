package codexcli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/pkg/projectfile"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/internal/shelllaunch"
	"github.com/manishiitg/multi-llm-provider-go/internal/stalldiag"
	"github.com/manishiitg/multi-llm-provider-go/internal/tmuxcontrol"
	"github.com/manishiitg/multi-llm-provider-go/internal/tmuxsize"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/paneview"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/sessionlease"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/sessionregistry"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/tmuxexec"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/tmuxlaunch"
	"github.com/manishiitg/multi-llm-provider-go/pkg/codingready"
	"github.com/manishiitg/multi-llm-provider-go/pkg/codingtimeout"
	"github.com/manishiitg/multi-llm-provider-go/pkg/pathidentity"
	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxinput"
)

const (
	// Default to no provider-level turn timeout. Workflow/background callers own
	// their execution deadline; the adapter should not cancel a still-running tmux
	// coding agent before the outer workflow timeout.
	defaultCodexInteractiveTimeout     = 0
	defaultCodexInteractiveIdleTimeout = codingtimeout.DefaultPersistentSessionIdle
	defaultCodexInteractiveRetention   = 30 * time.Minute
	defaultCodexPromptInactivityWait   = 10 * time.Minute
	defaultCodexPromptMaxWait          = 15 * time.Minute // owner, 2026-10-15: a launch that has not started in 15 minutes is stuck
	codexInteractiveStableWindow       = 1200 * time.Millisecond
	codexPromptCandidateStableWindow   = 2 * time.Second
	codexActivityScanNonEmptyLines     = 160

	// Last-resort completion recovery for unattended workflows. Stability alone
	// is not sufficient: the response loop also requires an exact
	// "STATUS: COMPLETED" marker, a returned empty composer, prior turn activity,
	// and no queued input/modal before applying this backstop. Native rollout
	// task_complete events remain the primary path.
	defaultCodexInteractiveStalePaneBackstop = 5 * time.Minute

	EnvCodexInteractiveSessionPrefix            = "CODEX_CLI_INTERACTIVE_SESSION_PREFIX"
	EnvCodexInteractiveTimeoutSeconds           = "CODEX_CLI_INTERACTIVE_TIMEOUT_SECONDS"
	EnvCodexInteractiveIdleTimeoutSeconds       = "CODEX_CLI_INTERACTIVE_IDLE_TIMEOUT_SECONDS"
	EnvCodexInteractivePromptWaitSeconds        = "CODEX_CLI_INTERACTIVE_PROMPT_WAIT_SECONDS"
	EnvCodexInteractivePromptMaxWaitSeconds     = "CODEX_CLI_INTERACTIVE_PROMPT_MAX_WAIT_SECONDS"
	EnvCodexInteractiveStalePaneBackstopSeconds = "CODEX_CLI_INTERACTIVE_STALE_PANE_BACKSTOP_SECONDS"
)

type codexInteractiveSession struct {
	accountRoot          string
	ownerSessionID       string
	tmuxSessionName      string
	systemPromptTempFile string
	// projectInstructionCleanup runs at session teardown when the
	// opt-in WithWriteProjectInstructionFile flag was set. It restores
	// any pre-existing operator AGENTS.md byte-for-byte. nil if the
	// flag wasn't enabled for this session.
	projectInstructionCleanup func()
	// launchCleanup owns sandbox policy and shell files until the detached pane
	// has consumed them; tmux returning does not mean its child has started.
	launchCleanup func()
	workingDir    string
	// rolloutPath and threadID pin this session to its EXACT Codex rollout.
	// Resolving a retained answer by working directory + newest mtime is
	// ambiguous whenever two sessions share a directory — a Chat and a Schedule
	// for the same workflow do — and would return the other conversation's
	// final answer (PLAT-106). Once bound, every later lookup is exact.
	rolloutPath string
	threadID    string
	// bindPrompt is the first prompt this session sent while it had no rollout
	// bound. A directory scan may bind only a rollout whose user rows contain
	// it: exclusion alone let two sessions that started together in one
	// directory claim each other's rollouts and return each other's answers.
	// Guarded by rolloutMu.
	bindPrompt string
	// bindSince bounds the bind-prompt scan. It is taken before the session is
	// acquired, because a fresh session receives its first prompt as a launch
	// argument and Codex can record it well before the turn's promptSentAt.
	bindSince              time.Time
	cliSecurityFingerprint string
	idleLease              sessionlease.Lease
	initErr                error
	createdAt              time.Time
	mu                     sync.Mutex
	// rolloutMu protects only the lightweight transcript identity. A Codex
	// turn holds mu for its full lifetime, so using mu while enumerating other
	// sessions creates a cross-session lock inversion when two turns finish
	// together: A holds A.mu and waits for B.mu while B holds B.mu and waits for
	// A.mu. Keep rollout identity independently readable so completion/final
	// extraction never waits on another session's whole-turn lock (PLAT-116).
	rolloutMu        sync.RWMutex
	retainedProgress codexRetainedProgress
	// durableMu guards pendingDurable only. It is a leaf lock like
	// rolloutMu: the live send path holds no session lock, and nothing
	// takes session.mu while holding it — the arbiter and the server
	// durability watcher both run concurrent with a turn that owns mu.
	durableMu      sync.Mutex
	pendingDurable []codexPendingDurableAck
}

var codexInteractiveRegistry = sessionregistry.NewOwnerRegistry[string]()
var codexPersistentRegistry = sessionregistry.NewOwnerRegistry[*codexInteractiveSession]()

func (c *CodexCLIAdapter) buildCodexInteractiveArgs(opts *llmtypes.CallOptions, systemPrompt string) ([]string, string, func(), error) {
	modelToUse := resolveCodexCLIModelID(c.modelID)
	if opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		if model, ok := opts.Metadata.Custom[MetadataKeyCodexModel].(string); ok && model != "" {
			modelToUse = resolveCodexCLIModelID(model)
		}
	}

	// Materialize session-scoped MCP configuration before projecting workspace
	// artifacts. Large tool schemas and bridge credentials must live in TOML,
	// never in -c command-line overrides.
	var mcpServersJSON string
	var configuredProfile string
	if opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		if v, ok := opts.Metadata.Custom[MetadataKeyMCPServers].(string); ok {
			mcpServersJSON = v
		}
		if v, ok := opts.Metadata.Custom[MetadataKeyConfigProfile].(string); ok {
			configuredProfile = strings.TrimSpace(v)
		}
	}
	if strings.TrimSpace(mcpServersJSON) != "" && configuredProfile != "" {
		return nil, "", nil, fmt.Errorf("codex CLI cannot combine an explicit profile %q with the AgentWorks session MCP profile", configuredProfile)
	}
	autoApproveMCPTools := false
	if opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		if policy, ok := opts.Metadata.Custom[MetadataKeyApprovalPolicy].(string); ok {
			autoApproveMCPTools = strings.TrimSpace(policy) == "never"
		}
	}
	sessionProfile, sessionProfileCleanup, err := writeCodexSessionMCPProfile(mcpServersJSON, autoApproveMCPTools, opts.CLISecurity, llmtypes.CLIHomeEnvironment(opts)["CODEX_HOME"])
	if err != nil {
		return nil, "", nil, err
	}

	// Project the opt-in workspace artifacts (AGENTS.md system prompt,
	// .codex/config.toml session defaults and optional MCP tables) FIRST so projection success is known
	// before deciding whether to skip the CLI-side -c model_instructions_file
	// injection below. ON by default via WithWriteProjectInstructionFile;
	// best-effort, a failure here is not a session-killer.
	//
	// projectedToInstructionFile records whether the AGENTS.md projection
	// actually succeeded. Used to gate the CLI injection in
	// project-instruction-only mode so the prompt is carried once, not
	// twice. The returned projectCleanup is stored on the session by the
	// caller and run at teardown.
	var projectCleanup func()
	projectedToInstructionFile := false
	workingDir := codexWorkingDirFromOptions(opts)
	if writeProjectInstructionFromOptions(opts) && strings.TrimSpace(workingDir) != "" && strings.TrimSpace(systemPrompt) != "" {
		// MCP lives in the unique CODEX_HOME profile above. Keeping it out of
		// the project file avoids shared-workspace races and secret persistence.
		if cleanup, perr := writeCodexProjectArtifacts(workingDir, systemPrompt, "", true); perr != nil {
			c.logger.Errorf("codex cli: project artifacts write failed (best-effort): %v", perr)
		} else if cleanup != nil {
			projectCleanup = cleanup
			projectedToInstructionFile = true
		}
	}
	if sessionProfileCleanup != nil {
		priorCleanup := projectCleanup
		projectCleanup = func() {
			if priorCleanup != nil {
				priorCleanup()
			}
			sessionProfileCleanup()
		}
	}

	resumeSessionID, err := codexValidatedResumeSessionIDFromOptions(opts)
	if err != nil {
		if projectCleanup != nil {
			projectCleanup()
		}
		return nil, "", nil, err
	}
	appendResumeSessionID := func(args []string) []string {
		if resumeSessionID != "" {
			args = append(args, resumeSessionID)
		}
		return args
	}

	args := []string{"codex"}
	// (The previous --dangerously-bypass-hook-trust wiring was removed
	// when the codex .codex/hooks.json projection was deleted from
	// writeCodexProjectArtifacts — see comment in that file. Codex's
	// --disable <feature> CLI flags via WithDisableShellTool /
	// WithDisableFeatures replace the need for a hooks.json drop, so
	// we never have hooks codex needs to trust-bypass for.)
	if resumeSessionID != "" {
		args = append(args, "resume")
		// An explicit destination cwd suppresses Codex's native directory chooser.
		// The process cwd alone leaves resumed chats waiting at that chooser.
		if workingDir != "" {
			args = append(args, "--cd", workingDir)
		}
	}
	if sessionProfile != "" {
		args = append(args, "--profile", sessionProfile)
	}
	args = appendCodexDisableUpdateArgs(args)
	args = appendCodexAccountArgs(args, opts)
	args = append(args, "--no-alt-screen")
	if modelToUse != "" && modelToUse != "codex-cli" {
		args = append(args, "--model", modelToUse)
	}
	if opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		approvalPolicy := ""
		mcpServersWithApproval := map[string]bool{}
		mcpServersNeedingApproval := map[string]bool{}
		disabledFeatures := map[string]bool{}
		// Interactive tmux mode applies MetadataKeyProjectDirID as the process cwd
		// in startCodexTmuxSession. Do not rely on Codex's --cd flag here; the
		// TUI header and all child behavior should see the caller-provided cwd.
		sandbox, _ := opts.Metadata.Custom[MetadataKeySandbox].(string)
		if sandbox = codexSandboxUnderConfinement(opts, strings.TrimSpace(sandbox)); sandbox != "" {
			args = append(args, "--sandbox", sandbox)
		}
		if profile, ok := opts.Metadata.Custom[MetadataKeyConfigProfile].(string); ok && strings.TrimSpace(profile) != "" {
			args = append(args, "--profile", profile)
		}
		if disable, ok := opts.Metadata.Custom[MetadataKeyDisableShellTool].(bool); ok && disable {
			args = appendCodexDisabledFeatureArgs(args, disabledFeatures, codexBridgeOnlyDisabledFeatures...)
		}
		if features, ok := opts.Metadata.Custom[MetadataKeyDisableFeatures].(string); ok && strings.TrimSpace(features) != "" {
			args = appendCodexDisabledFeatureArgs(args, disabledFeatures, strings.Split(features, ",")...)
		}
		if features, ok := opts.Metadata.Custom[MetadataKeyEnableFeatures].(string); ok && strings.TrimSpace(features) != "" {
			args = appendCodexFeatureCSV(args, "--enable", features)
		}
		if policy, ok := opts.Metadata.Custom[MetadataKeyApprovalPolicy].(string); ok && strings.TrimSpace(policy) != "" {
			policy = strings.TrimSpace(policy)
			approvalPolicy = policy
			args = append(args, "--ask-for-approval", policy)
			args = append(args, "-c", fmt.Sprintf("approval_policy=%q", policy))
		}
		if effort, ok := opts.Metadata.Custom[MetadataKeyReasoningEffort].(string); ok && strings.TrimSpace(effort) != "" {
			args = append(args, "-c", fmt.Sprintf("model_reasoning_effort=%q", effort))
		}
		if overrides, ok := opts.Metadata.Custom[MetadataKeyConfigOverrides].([]string); ok {
			for _, override := range overrides {
				if strings.TrimSpace(override) != "" {
					args = append(args, "-c", override)
					if serverName := codexMCPServerNameFromConfigOverride(override); serverName != "" {
						if strings.Contains(override, ".default_tools_approval_mode") || strings.Contains(override, ".tools.") && strings.Contains(override, ".approval_mode") {
							mcpServersWithApproval[serverName] = true
						} else {
							mcpServersNeedingApproval[serverName] = true
						}
					}
				}
			}
		}
		if approvalPolicy == "never" {
			for serverName := range mcpServersNeedingApproval {
				if !mcpServersWithApproval[serverName] {
					args = append(args, "-c", fmt.Sprintf("mcp_servers.%s.default_tools_approval_mode=%q", serverName, "approve"))
				}
			}
		}
	}
	// Inject the system prompt via -c model_instructions_file UNLESS the
	// caller opted into project-instruction-only mode AND the AGENTS.md
	// projection above actually succeeded — in which case AGENTS.md is the
	// sole carrier (no doubled prompt / token cost). If the projection was
	// skipped (flag off / empty working dir) or failed,
	// projectedToInstructionFile is false and we still inject so the prompt
	// is never silently dropped.
	if strings.TrimSpace(systemPrompt) != "" && !(projectInstructionOnlyFromOptions(opts) && projectedToInstructionFile) {
		systemPromptTempFile, err := writeCodexInteractiveSystemPromptFile(systemPrompt)
		if err != nil {
			if projectCleanup != nil {
				projectCleanup()
			}
			return nil, "", nil, err
		}
		override, err := codexStringConfigOverride("model_instructions_file", systemPromptTempFile)
		if err != nil {
			_ = os.Remove(systemPromptTempFile)
			if projectCleanup != nil {
				projectCleanup()
			}
			return nil, "", nil, err
		}
		args = append(args, "-c", override)
		return appendResumeSessionID(args), systemPromptTempFile, projectCleanup, nil
	}
	return appendResumeSessionID(args), "", projectCleanup, nil
}

func releaseCodexInteractiveSession(session *codexInteractiveSession, logger interfaces.Logger) {
	if session == nil {
		return
	}
	session.idleLease.Arm(codexInteractiveIdleTimeout(), func() {
		closeCodexPersistentSession(session.ownerSessionID, "idle timeout", logger)
	})
	session.mu.Unlock()
}

func releaseCodexBoundedInteractiveSession(session *codexInteractiveSession, logger interfaces.Logger) {
	if session == nil {
		return
	}
	// Keep the real tmux pane alive for the shared bounded retention window so
	// the UI terminal remains inspectable/debuggable while it is visible.
	retention := llmtypes.TmuxKillDelay
	if retention <= 0 {
		closeCodexSessionLocked(session, "bounded turn complete", logger)
		return
	}
	if logger != nil {
		logger.Debugf("Retaining completed Codex interactive session %s for owner %s for %s (then kill)", session.tmuxSessionName, session.ownerSessionID, retention)
	}
	session.idleLease.Arm(retention, func() {
		closeCodexPersistentSession(session.ownerSessionID, "bounded retention elapsed", logger)
	})
	session.mu.Unlock()
}

// CloseCodexCLIInteractiveSessionForOwner closes the persistent codex
// interactive session for the given owner. See cursorcli's equivalent
// CloseCursorCLIInteractiveSessionForOwner for the mid-chat-prompt-change
// motivation.
func CloseCodexCLIInteractiveSessionForOwner(ownerSessionID, reason string) {
	closeCodexAppSession(ownerSessionID)
	closeCodexPersistentSession(ownerSessionID, reason, nil)
}

// CloseCodexCLIInteractiveSessionByTmux closes the persistent codex
// interactive session whose backing tmux session matches tmuxSessionName,
// regardless of the owner key it was registered under. Teardown backstop for
// when the owning session ID is unknown or has drifted. Delegates to the
// owner-keyed close so the same graceful exit + cleanup runs. No-op when no
// live session matches.
func CloseCodexCLIInteractiveSessionByTmux(tmuxSessionName, reason string) {
	name := strings.TrimSpace(tmuxSessionName)
	if name == "" {
		return
	}
	owner, _, ok := codexPersistentRegistry.Find(func(s *codexInteractiveSession) bool {
		return s != nil && s.tmuxSessionName == name
	})
	if !ok || owner == "" {
		return
	}
	closeCodexPersistentSession(owner, reason, nil)
}

func closeCodexPersistentSession(ownerSessionID, reason string, logger interfaces.Logger) {
	session, ok := codexPersistentRegistry.Delete(ownerSessionID)
	if !ok || session == nil {
		return
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	session.idleLease.Stop()
	if logger != nil {
		logger.Debugf("Closing Codex interactive session %s for owner %s: %s", session.tmuxSessionName, ownerSessionID, reason)
	}
	closeCodexSessionLocked(session, reason, logger)
}

func closeCodexSessionLocked(session *codexInteractiveSession, reason string, logger interfaces.Logger) {
	if session == nil {
		return
	}
	session.idleLease.Stop()
	if logger != nil {
		logger.Debugf("Closing Codex interactive session %s for owner %s: %s", session.tmuxSessionName, session.ownerSessionID, reason)
	}
	removeCodexPersistentSession(session.ownerSessionID, session)
	unregisterCodexInteractiveSession(session.ownerSessionID, session.tmuxSessionName)
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = tmuxinput.Default.Do(closeCtx, tmuxinput.Request{
		SessionID: session.tmuxSessionName,
		Source:    "codex-cli-close",
		Priority:  tmuxinput.PriorityInterrupt,
	}, func(ctx context.Context) error {
		_ = runCodexCommand(ctx, nil, "tmux", "send-keys", "-t", session.tmuxSessionName, "C-c")
		return killCodexTmuxSession(ctx, session.tmuxSessionName)
	})
	if session.systemPromptTempFile != "" {
		_ = os.Remove(session.systemPromptTempFile)
		session.systemPromptTempFile = ""
	}
	if session.projectInstructionCleanup != nil {
		session.projectInstructionCleanup()
		session.projectInstructionCleanup = nil
	}
	cleanupCodexLaunch(session)
}

func cleanupCodexLaunch(session *codexInteractiveSession) {
	if session.launchCleanup != nil {
		session.launchCleanup()
		session.launchCleanup = nil
	}
}

// writeProjectInstructionFromOptions reads the feature flag for writing
// the per-session system prompt to <workingDir>/AGENTS.md. Defaults to
// true when the key is unset; callers can opt out by passing
// WithCodexWriteProjectInstructionFile(false).
func writeProjectInstructionFromOptions(opts *llmtypes.CallOptions) bool {
	if llmtypes.CLIHasPrivateRuntime(opts) {
		return false
	}
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return true
	}
	v, ok := opts.Metadata.Custom[MetadataKeyWriteProjectInstructionFile]
	if !ok {
		return true
	}
	enabled, _ := v.(bool)
	return enabled
}

// projectInstructionOnlyFromOptions reads the OFF-by-default feature flag for
// carrying the system prompt only via the projected AGENTS.md file (skipping
// the CLI-side -c developer_instructions / -c model_instructions_file
// injection). Returns false when the key is unset. Callers opt in with
// WithProjectInstructionOnly(true).
func projectInstructionOnlyFromOptions(opts *llmtypes.CallOptions) bool {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return false
	}
	enabled, _ := opts.Metadata.Custom[MetadataKeyProjectInstructionOnly].(bool)
	return enabled
}

// writeCodexProjectAgentsFile adds the per-session system prompt to
// <workingDir>/AGENTS.md (codex's project-instructions convention) as a
// marked block; the returned cleanup releases it.
func writeCodexProjectAgentsFile(workingDir, systemPrompt string) (func(), error) {
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		return func() {}, nil
	}
	// The prompt is added to AGENTS.md as a marked block, counted per
	// session: a project's own AGENTS.md and other sessions sharing the folder
	// are never overwritten or deleted.
	token, err := projectfile.AcquireLease(filepath.Join(workingDir, "AGENTS.md"), systemPrompt)
	if err != nil {
		return nil, err
	}
	return func() { projectfile.ReleaseToken(token) }, nil
}

func markCodexInteractiveSessionFailedLocked(session *codexInteractiveSession, err error, logger interfaces.Logger) {
	if session == nil {
		return
	}
	if err != nil {
		session.initErr = err
	}
	session.idleLease.Stop()
	if logger != nil {
		logger.Debugf("Discarding Codex interactive session %s for owner %s: %v", session.tmuxSessionName, session.ownerSessionID, err)
	}
}

func cleanupFailedCodexInteractiveSession(session *codexInteractiveSession) {
	if session == nil {
		return
	}
	removeCodexPersistentSession(session.ownerSessionID, session)
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = killCodexTmuxSession(cleanupCtx, session.tmuxSessionName)
	cleanupCodexLaunch(session)
	unregisterCodexInteractiveSession(session.ownerSessionID, session.tmuxSessionName)
	if session.systemPromptTempFile != "" {
		_ = os.Remove(session.systemPromptTempFile)
	}
}

func removeCodexPersistentSession(ownerSessionID string, session *codexInteractiveSession) {
	codexPersistentRegistry.DeleteIf(ownerSessionID, session)
}

func CleanupCodexCLIInteractiveSessions(ctx context.Context) error {
	closeCodexAppSessions()
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil
	}
	sessions := codexPersistentRegistry.Drain()

	var failures []string
	for _, session := range sessions {
		stopCodexIdleTimerIfAvailable(session)
		unregisterCodexInteractiveSession(session.ownerSessionID, session.tmuxSessionName)
		if session.systemPromptTempFile != "" {
			_ = os.Remove(session.systemPromptTempFile)
		}
		// Honor the WithWriteProjectInstructionFile byte-restore
		// promise: when the global cleanup path tears down sessions
		// without going through closeCodexPersistentSession (e.g.
		// process exit, test teardown, explicit
		// CleanupCodexCLIInteractiveSessions call), the project
		// artifact restore must still run. Without this, operator-
		// owned AGENTS.md / .codex/config.toml content stays
		// destroyed after persistent-session shutdown.
		if session.projectInstructionCleanup != nil {
			session.projectInstructionCleanup()
			session.projectInstructionCleanup = nil
		}
		if err := killCodexTmuxSession(ctx, session.tmuxSessionName); err != nil {
			failures = append(failures, err.Error())
		}
		cleanupCodexLaunch(session)
	}
	if len(failures) > 0 {
		return fmt.Errorf("failed to clean up Codex interactive sessions: %s", strings.Join(failures, "; "))
	}
	return nil
}

func stopCodexIdleTimerIfAvailable(session *codexInteractiveSession) {
	if session == nil || !session.mu.TryLock() {
		return
	}
	defer session.mu.Unlock()
	session.idleLease.Stop()
}

func registerCodexInteractiveSession(ownerSessionID, tmuxSessionName string) {
	tmuxSessionName = strings.TrimSpace(tmuxSessionName)
	if tmuxSessionName == "" {
		return
	}
	tmuxinput.MarkStartingForOwner(tmuxSessionName, ownerSessionID)
	codexInteractiveRegistry.Set(ownerSessionID, tmuxSessionName)
}

func unregisterCodexInteractiveSession(ownerSessionID, tmuxSessionName string) {
	codexInteractiveRegistry.DeleteIf(ownerSessionID, tmuxSessionName)
	tmuxinput.RemoveReadiness(tmuxSessionName)
}

func activeCodexInteractiveSession(ownerSessionID string) (string, bool) {
	sessionName, ok := codexInteractiveRegistry.Get(ownerSessionID)
	return sessionName, ok && strings.TrimSpace(sessionName) != ""
}

// InteractiveSessionRegistered reports whether the owner's Codex TUI is
// registered — the live-injection transport exists. Cheap registry lookup
// (no tmux round-trip) for steer gating; delivery re-verifies liveness.
// InteractiveTurnActive excludes an idle retained app-server and its maintenance
// compaction turn from live person/agent input. Delivery rechecks under the lock.
func InteractiveTurnActive(owner string) bool {
	if _, ok := activeCodexInteractiveSession(owner); ok {
		return true
	}
	s := codexAppSessionForOwner(owner)
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rpc == nil || s.turnID == "" || s.compacting {
		return false
	}
	select {
	case <-s.rpc.done:
		return false
	default:
		return true
	}
}

func InteractiveSessionRegistered(ownerSessionID string) bool {
	if _, ok := activeCodexInteractiveSession(ownerSessionID); ok {
		return true
	}
	s := codexAppSessionForOwner(ownerSessionID)
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rpc == nil {
		return false
	}
	select {
	case <-s.rpc.done:
		return false
	default:
		return true
	}
}

func SendCodexInteractiveInput(ctx context.Context, ownerSessionID, message string) error {
	if _, ok := activeCodexInteractiveSession(ownerSessionID); !ok {
		return SendCodexAppServerInput(ctx, ownerSessionID, message)
	}
	if session, ok := codexPersistentRegistry.Get(ownerSessionID); ok && session != nil {
		if !session.idleLease.Arm(codexInteractiveIdleTimeout(), func() {
			closeCodexPersistentSession(session.ownerSessionID, "idle timeout", nil)
		}) {
			return fmt.Errorf("Codex session for owner %s is expiring; retry to resume it in a fresh tmux session", ownerSessionID)
		}
	}
	sessionName, ok := activeCodexInteractiveSession(ownerSessionID)
	if !ok {
		return fmt.Errorf("no active Codex interactive session registered for owner session %s", ownerSessionID)
	}
	return sendCodexInputToTmux(ctx, ownerSessionID, sessionName, message)
}

func codexInteractiveSessionIDFromOptions(opts *llmtypes.CallOptions) string {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return ""
	}
	if sessionID, ok := opts.Metadata.Custom[MetadataKeyInteractiveSessionID].(string); ok {
		return strings.TrimSpace(sessionID)
	}
	return ""
}

func codexPersistentInteractiveFromOptions(opts *llmtypes.CallOptions) bool {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return false
	}
	enabled, ok := opts.Metadata.Custom[MetadataKeyPersistentInteractive].(bool)
	return ok && enabled
}

func codexResumeSessionIDFromOptions(opts *llmtypes.CallOptions) string {
	sessionID, _ := codexValidatedResumeSessionIDFromOptions(opts)
	return sessionID
}

func codexValidatedResumeSessionIDFromOptions(opts *llmtypes.CallOptions) (string, error) {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return "", nil
	}
	if sessionID, ok := opts.Metadata.Custom[MetadataKeyResumeSessionID].(string); ok {
		sessionID = strings.TrimSpace(sessionID)
		if sessionID == "" {
			return "", nil
		}
		if !isCodexSessionID(sessionID) {
			return "", fmt.Errorf("invalid codex resume session id %q", sessionID)
		}
		return sessionID, nil
	}
	return "", nil
}

func splitCodexSystemPrompt(messages []llmtypes.MessageContent) (string, []llmtypes.MessageContent) {
	var systems []string
	conversation := make([]llmtypes.MessageContent, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == llmtypes.ChatMessageTypeSystem {
			for _, part := range msg.Parts {
				if textPart, ok := part.(llmtypes.TextContent); ok {
					systems = append(systems, textPart.Text)
				}
			}
			continue
		}
		conversation = append(conversation, msg)
	}
	return strings.Join(systems, "\n\n"), conversation
}

func buildCodexInteractivePrompt(messages []llmtypes.MessageContent) string {
	var parts []string
	for _, message := range llmtypes.PendingHumanMessages(messages) {
		parts = append(parts, extractTextFromMessage(message))
	}
	return strings.Join(parts, "\n\n")
}

func codexCanSubmitInitialPromptAtLaunch(opts *llmtypes.CallOptions, prompt string) bool {
	if strings.TrimSpace(prompt) == "" || llmtypes.CodingProviderLaunchOnlyFromOptions(opts) {
		return false
	}
	// `codex [PROMPT]` accepts a positional first prompt. `codex resume`,
	// however, has a different positional shape and is intentionally left on
	// the established ready-then-paste path.
	// A prompt given at launch starts the turn before the MCP bridge has answered tools/list, so a new chat's
	// first turn could lack the platform's tools (a Code tab said it had no inbox tool, 2026-10-17). With a
	// readiness marker configured the prompt is pasted after the bridge is ready instead.
	if opts != nil && opts.Metadata != nil && codingready.MCPReadyFileFromMetadata(opts.Metadata.Custom) != "" {
		return false
	}
	return strings.TrimSpace(codexResumeSessionIDFromOptions(opts)) == ""
}

func appendCodexInitialPromptArgs(args []string, opts *llmtypes.CallOptions, prompt string) []string {
	if !codexCanSubmitInitialPromptAtLaunch(opts, prompt) {
		return args
	}
	// Codex supports an optional positional prompt. Supplying the first user
	// message here lets the CLI start the turn as soon as its own startup and
	// MCP initialization finish instead of launching an empty composer and
	// making the caller wait before pasting the same message through tmux.
	return append(args, "--", prompt)
}

func codexMCPServerNameFromConfigOverride(override string) string {
	override = strings.TrimSpace(override)
	if !strings.HasPrefix(override, "mcp_servers.") {
		return ""
	}
	rest := strings.TrimPrefix(override, "mcp_servers.")
	idx := strings.Index(rest, ".")
	if idx <= 0 {
		return ""
	}
	serverName := rest[:idx]
	for _, r := range serverName {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return ""
		}
	}
	return serverName
}

func writeCodexInteractiveSystemPromptFile(systemPrompt string) (string, error) {
	tmpFile, err := os.CreateTemp("", "codex-interactive-system-*.md")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file for Codex interactive system prompt: %w", err)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.WriteString(systemPrompt); err != nil {
		tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("failed to write Codex interactive system prompt: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("failed to close Codex interactive system prompt: %w", err)
	}
	return tmpPath, nil
}

func codexAssistantHistory(messages []llmtypes.MessageContent) []string {
	history := make([]string, 0)
	for _, msg := range messages {
		if msg.Role != llmtypes.ChatMessageTypeAI {
			continue
		}
		text := strings.TrimSpace(extractTextFromMessage(msg))
		if text != "" {
			history = append(history, text)
		}
	}
	return history
}

func codexWorkingDirFromOptions(opts *llmtypes.CallOptions) string {
	if opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		if dir, ok := opts.Metadata.Custom[MetadataKeyProjectDirID].(string); ok {
			if trimmed := strings.TrimSpace(dir); trimmed != "" {
				return trimmed
			}
		}
	}
	// Fallback to the process cwd. WITHOUT this, the sidecar
	// parsers (readCodexTranscriptUsage / readCodexTranscriptMessages)
	// get expectedWorkingDir="" and skip their session_meta.cwd
	// filter — which means they happily pick up the freshest
	// rollout from a parallel codex process (Codex Desktop / VS
	// Code Codex), leaking that other process's conversation +
	// tokens into this session. cursor's adapter has the same
	// fallback via cursorMustGetwd().
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

func startCodexTmuxSession(
	ctx context.Context,
	sessionName string,
	args []string,
	workingDir string,
	policy *llmtypes.CLISecurityPolicy,
	runtimeReadPaths []string,
	scopedEnv, unsetEnv []string,
	scrub *shelllaunch.ScopeScrub,
) (cleanup func(), err error) {
	if workingDir != "" {
		// Pre-trust workingDir in ~/.codex/config.toml so codex skips
		// its interactive "Do you trust the contents of this
		// directory?" prompt. Per
		// https://github.com/openai/codex/issues/14345, the trust
		// prompt fires even with --dangerously-bypass-approvals-and-sandbox
		// on v0.114+ unless the path is recorded in
		// projects."<path>".trust_level = "trusted". The pattern
		// mirrors preTrustClaudeWorkingDir which writes to
		// ~/.claude.json. Errors are silently ignored — the worst
		// case is the prompt appears and the in-tmux dismissCodexTrustPrompt
		// (already wired into waitForCodexPrompt) handles it
		// reactively.
		accountHome := ""
		for _, entry := range scopedEnv {
			if strings.HasPrefix(entry, "HOME=") {
				accountHome = strings.TrimPrefix(entry, "HOME=")
			}
		}
		if accountHome != "" {
			preTrustCodexWorkingDirAtHome(workingDir, accountHome)
		} else if policy != nil && llmtypes.NormalizeCLISecurityMode(policy.Mode) == llmtypes.CLISecurityModeIsolated {
			// Isolated Codex (Landlock or Seatbelt) reads its private home.
			preTrustCodexWorkingDirAtHome(workingDir, policy.PrivateHome)
		} else {
			preTrustCodexWorkingDir(workingDir)
		}
	}
	shellCommand, cleanupSandbox, err := clisandbox.PrepareCodexCommandScoped(policy, args, workingDir, runtimeReadPaths, scopedEnv, unsetEnv, scrub)
	if err != nil {
		return nil, fmt.Errorf("prepare Codex CLI security sandbox: %w", err)
	}
	defer func() {
		if err != nil {
			cleanupSandbox()
		}
	}()
	tmuxArgs := []string{"new-session", "-d", "-s", sessionName}
	tmuxArgs = append(tmuxArgs, tmuxsize.Args()...)
	tmuxArgs = append(tmuxArgs, shellCommand)
	tmuxArgs = tmuxlaunch.WithHistoryLimit(tmuxArgs, tmuxexec.DefaultHistoryLimit)
	if err := runCodexCommand(ctx, nil, "tmux", tmuxArgs...); err != nil {
		return nil, fmt.Errorf("failed to start Codex interactive session %q: %w", sessionName, err)
	}
	_ = runCodexCommand(ctx, nil, "tmux", "set-option", "-t", sessionName, "remain-on-exit", "on")
	if err := runCodexCommand(ctx, nil, "tmux", "set-option", "-t", sessionName, "history-limit", tmuxexec.DefaultHistoryLimit); err != nil {
		return nil, fmt.Errorf("failed to configure Codex tmux history for session %q: %w", sessionName, err)
	}
	// Pin the window size to manual so the detached session keeps the size we
	// launched at instead of collapsing to default-size (80x24), which reflows
	// the TUI into half-width and makes the captured pane unreadable.
	_ = runCodexCommand(ctx, nil, "tmux", "set-option", "-t", sessionName, "window-size", "manual")
	_ = runCodexCommand(ctx, nil, "tmux", "set-option", "-t", sessionName, "focus-events", "on")
	return cleanupSandbox, nil
}

var preTrustCodexMu sync.Mutex

// preTrustCodexWorkingDir records workingDir as a trusted project in
// ~/.codex/config.toml so codex's "Do you trust the contents of this
// directory?" prompt does not fire when the tmux session launches.
// Per https://github.com/openai/codex/issues/14345, codex v0.114+
// shows this prompt EVEN WITH --dangerously-bypass-approvals-and-sandbox
// set, unless the path is pre-recorded via
//
//	[projects."<absolute-path>"]
//	trust_level = "trusted"
//
// in ~/.codex/config.toml. We append this section if the workingDir
// is not already trusted. On macOS /var is a symlink to /private/var,
// so we record both raw and EvalSymlinks-resolved paths to match
// however codex normalizes the path at trust-check time.
//
// Errors are silently ignored — the in-tmux dismissCodexTrustPrompt
// (already wired into waitForCodexPrompt) reactively handles the
// prompt if pre-trust fails. This is belt-and-suspenders, not a
// replacement.
func preTrustCodexWorkingDir(workingDir string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	preTrustCodexWorkingDirAtHome(workingDir, home)
}

func preTrustCodexWorkingDirAtHome(workingDir, home string) {
	workingDir = strings.TrimSpace(workingDir)
	home = strings.TrimSpace(home)
	if workingDir == "" || home == "" {
		return
	}
	paths := []string{workingDir}
	// Codex compares the configured project key with the physical cwd text.
	// On a case-insensitive filesystem those can differ only in spelling (for
	// example AgentWorks versus agentworks), and filepath.EvalSymlinks preserves
	// the caller's spelling. Record the directory-entry spelling as well so a
	// generated workspace does not fall through to Codex's interactive trust
	// screen even though both paths identify the same directory.
	if physical := codexFilesystemPathSpelling(workingDir); physical != workingDir {
		paths = append(paths, physical)
	}
	if resolved, err := filepath.EvalSymlinks(workingDir); err == nil {
		paths = append(paths, resolved)
		if physical := codexFilesystemPathSpelling(resolved); physical != resolved {
			paths = append(paths, physical)
		}
	}
	paths = uniqueCodexPaths(paths)
	configDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return
	}
	configPath := filepath.Join(configDir, "config.toml")

	preTrustCodexMu.Lock()
	defer preTrustCodexMu.Unlock()

	existing, _ := os.ReadFile(configPath)
	existingStr := string(existing)

	var toAppend strings.Builder
	for _, p := range paths {
		// Look for an existing [projects."<p>"] section. If present,
		// assume it's already trusted (the issue's workaround scripts
		// follow the same idempotency assumption). Codex parses TOML
		// quoted-keys with backslash escapes, but project paths from
		// macOS/Linux don't contain characters that need escaping.
		marker := fmt.Sprintf("[projects.%q]", p)
		if strings.Contains(existingStr, marker) {
			continue
		}
		// Ensure separation from any prior content.
		if toAppend.Len() == 0 && existingStr != "" && !strings.HasSuffix(existingStr, "\n") {
			toAppend.WriteString("\n")
		}
		toAppend.WriteString("\n")
		toAppend.WriteString(marker)
		toAppend.WriteString("\n")
		toAppend.WriteString(`trust_level = "trusted"`)
		toAppend.WriteString("\n")
	}
	if toAppend.Len() == 0 {
		return
	}

	updated := existingStr + toAppend.String()
	_ = os.WriteFile(configPath, []byte(updated), 0o600)
}

func codexFilesystemPathSpelling(path string) string {
	if spelling, err := pathidentity.Spelling(path); err == nil {
		return spelling
	}
	return filepath.Clean(path)
}

func uniqueCodexPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	unique := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		unique = append(unique, path)
	}
	return unique
}

func codexInteractiveShellCommand(args []string, workingDir string) string {
	return shelllaunch.Command(args, workingDir)
}

func waitForCodexPrompt(ctx context.Context, sessionName string, streamChan chan<- llmtypes.StreamChunk, streamTerminalScreen bool) error {
	return waitForCodexPromptMode(ctx, sessionName, streamChan, false, streamTerminalScreen)
}

func waitForCodexInputPrompt(ctx context.Context, sessionName string, streamChan chan<- llmtypes.StreamChunk, streamTerminalScreen bool) error {
	return waitForCodexPromptMode(ctx, sessionName, streamChan, true, streamTerminalScreen)
}

func waitForCodexInitialPromptAccepted(ctx context.Context, sessionName, prompt string, streamChan chan<- llmtypes.StreamChunk, streamTerminalScreen bool, oracle codexSubmissionOracle) (string, error) {
	return waitForCodexInitialPromptAcceptedWith(ctx, sessionName, prompt, streamChan, streamTerminalScreen, codexSubmissionWait{oracle: oracle})
}

func waitForCodexInitialPromptAcceptedWith(ctx context.Context, sessionName, prompt string, streamChan chan<- llmtypes.StreamChunk, streamTerminalScreen bool, wait codexSubmissionWait) (string, error) {
	maxWait := codexInteractivePromptMaxWait()
	maxTimer := time.NewTimer(maxWait)
	defer maxTimer.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	dismissedTrustPrompt := false
	dismissedRateReminder := false
	dismissedSafetyChecks := false
	hookTrustDismissAttempts := 0
	const maxHookTrustDismissAttempts = 6
	var lastTerminalSnapshot string
	var lastTerminalStreamedAt time.Time
	var lastStartupCapture string
	quietWatch := &stalldiag.Watch{Provider: "codex-cli", Session: sessionName}
	capture := wait.capture
	if capture == nil {
		capture = captureCodexPane
	}

	for {
		select {
		case <-ctx.Done():
			return lastStartupCapture, ctx.Err()
		case <-maxTimer.C:
			captured, _ := captureCodexPane(context.Background(), sessionName)
			if strings.TrimSpace(captured) != "" {
				log.Printf("[TURN] stalled cause=launch_timeout provider=codex-cli session=%s phase=launch_prompt waited=%s", sessionName, maxWait)
				return lastStartupCapture, fmt.Errorf("Codex did not start in %s. Your message is saved; press Retry. (timed out waiting for the Codex CLI launch prompt to start; %s)", maxWait, llmtypes.CompactTerminalPaneForError(sessionName, captured))
			}
			log.Printf("[TURN] stalled cause=launch_timeout provider=codex-cli session=%s phase=launch_prompt waited=%s", sessionName, maxWait)
			return lastStartupCapture, fmt.Errorf("Codex did not start in %s. Your message is saved; press Retry. (timed out waiting for the Codex CLI launch prompt to start)", maxWait)
		case <-ticker.C:
			// The native rollout can accept a positional prompt before the TUI
			// renders its echo or activity. Release the startup input lane as soon
			// as this exact session records the turn, including capture failures.
			strict := false
			if wait.oracle != nil {
				started, authoritative := wait.oracle()
				if started {
					return lastStartupCapture, nil
				}
				strict = authoritative
			}
			captured, err := capture(ctx, sessionName)
			if err != nil {
				continue
			}
			quietWatch.Observe(time.Now(), captured, true)
			// Codex is waiting on its own sign-in screen: the prompt can never be accepted.
			if codexPaneAsksForSignIn(captured) {
				log.Printf("[TURN] stalled cause=login_required provider=codex-cli session=%s phase=launch_prompt", sessionName)
				return lastStartupCapture, &CodexLoginRequiredError{}
			}
			if streamChan != nil && streamTerminalScreen && time.Since(lastTerminalStreamedAt) >= time.Second {
				if streamCodexTerminalSnapshot(ctx, sessionName, streamChan, &lastTerminalSnapshot) {
					lastTerminalStreamedAt = time.Now()
				}
			}
			if hasCodexTrustPrompt(captured) {
				if !dismissedTrustPrompt {
					_ = dismissCodexTrustPrompt(ctx, sessionName, captured)
					dismissedTrustPrompt = true
				}
				continue
			}
			if hasCodexHookTrustReviewPrompt(captured) && hookTrustDismissAttempts < maxHookTrustDismissAttempts {
				_ = dismissCodexHookTrustReviewPrompt(ctx, sessionName)
				hookTrustDismissAttempts++
				continue
			}
			if hasCodexRateLimitReminderModal(captured) {
				if !dismissedRateReminder {
					handled, _ := dismissCodexRateLimitReminderSerialized(ctx, sessionName)
					dismissedRateReminder = handled
				}
				continue
			}
			if hasCodexAdditionalSafetyChecksModal(captured) {
				if !dismissedSafetyChecks {
					dismissedSafetyChecks = dismissCodexAdditionalSafetyChecks(ctx, sessionName, captured) == nil
				}
				continue
			}
			// A launch-time positional prompt is accepted once Codex shows active
			// work, queued input, the echoed user prompt, or a completed answer.
			// The last case covers very short turns that finish between captures.
			if !strict && (hasCodexActivity(captured) || hasCodexQueuedInput(captured) ||
				codexPaneShowsPromptDraft(captured, prompt) ||
				(hasCodexReadyPrompt(captured) && hasCodexExplicitCompletedMarker(captured))) ||
				hasCodexPolicyInvalidPrompt(captured) {
				return lastStartupCapture, nil
			}
			// Retain only screens observed before the launch prompt starts. The
			// response watcher subtracts this baseline so Codex's startup animation
			// and old "model: loading" frames cannot look like perpetual activity.
			lastStartupCapture = captured
		}
	}
}

func waitForCodexPromptMode(ctx context.Context, sessionName string, streamChan chan<- llmtypes.StreamChunk, acceptActiveComposer bool, streamTerminalScreen bool) error {
	promptWait := codexInteractivePromptWait()
	maxWait := codexInteractivePromptMaxWait()
	maxTimer := time.NewTimer(maxWait)
	defer maxTimer.Stop()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	dismissedRateReminder := false
	dismissedTrustPrompt := false
	dismissedSafetyChecks := false
	// hookTrustDismissAttempts counts re-dismissal attempts rather
	// than using a binary flag because codex's hook trust UI is a
	// MULTI-STEP wizard: an initial 3-option menu, then an expanded
	// review table after "Trust all and continue", then the normal
	// ready prompt. Each step needs its own dismissal keystrokes.
	// We cap attempts to avoid infinite loops if codex enters an
	// unexpected state — 6 attempts at 200ms ticks gives codex ~1.2s
	// to advance, well within the 120s prompt-wait deadline.
	hookTrustDismissAttempts := 0
	const maxHookTrustDismissAttempts = 6
	var lastTerminalSnapshot string
	var lastTerminalStreamedAt time.Time
	lastActivityAt := time.Now()
	lastCaptured := ""
	var promptCandidateSnapshot string
	var promptCandidateSince time.Time
	promptQuiet := &stalldiag.Watch{Provider: "codex-cli", Session: sessionName}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-maxTimer.C:
			captured, _ := captureCodexPane(context.Background(), sessionName)
			if strings.TrimSpace(captured) != "" {
				log.Printf("[TURN] stalled cause=launch_timeout provider=codex-cli session=%s phase=prompt_mode waited=%s", sessionName, maxWait)
				return fmt.Errorf("Codex did not start in %s. Your message is saved; press Retry. (timed out waiting for the Codex CLI prompt; %s)", maxWait, llmtypes.CompactTerminalPaneForError(sessionName, captured))
			}
			log.Printf("[TURN] stalled cause=launch_timeout provider=codex-cli session=%s phase=prompt_mode waited=%s", sessionName, maxWait)
			return fmt.Errorf("Codex did not start in %s. Your message is saved; press Retry. (timed out waiting for the Codex CLI prompt)", maxWait)
		case <-ticker.C:
			captured, err := captureCodexPane(ctx, sessionName)
			if err != nil {
				continue
			}
			if captured != lastCaptured {
				lastCaptured = captured
				lastActivityAt = time.Now()
			}
			promptQuiet.Observe(time.Now(), captured, true)
			if codexPaneAsksForSignIn(captured) {
				log.Printf("[TURN] stalled cause=login_required provider=codex-cli session=%s phase=prompt_mode", sessionName)
				return &CodexLoginRequiredError{}
			}
			if streamChan != nil && streamTerminalScreen {
				if time.Since(lastTerminalStreamedAt) >= time.Second && streamCodexTerminalSnapshot(ctx, sessionName, streamChan, &lastTerminalSnapshot) {
					lastTerminalStreamedAt = time.Now()
				}
			}
			if hasCodexTrustPrompt(captured) {
				if !dismissedTrustPrompt {
					_ = dismissCodexTrustPrompt(ctx, sessionName, captured)
					dismissedTrustPrompt = true
					lastActivityAt = time.Now()
				}
				continue
			}
			// Hook trust review screen ("Press t to trust all; enter to
			// review hooks; esc to close") fires on launch whenever the
			// workspace has a .codex/hooks.json that codex hasn't seen
			// before — even with --dangerously-bypass-hook-trust set,
			// because the flag enables hook EXECUTION but does not
			// auto-dismiss the visual review screen on v0.131.0.
			// Sending "t" trusts all hooks for this invocation, which
			// is the same trust state the flag already implies.
			if hasCodexHookTrustReviewPrompt(captured) && hookTrustDismissAttempts < maxHookTrustDismissAttempts {
				_ = dismissCodexHookTrustReviewPrompt(ctx, sessionName)
				hookTrustDismissAttempts++
				lastActivityAt = time.Now()
				continue
			}
			if hasCodexRateLimitReminderModal(captured) {
				if !dismissedRateReminder {
					handled, _ := dismissCodexRateLimitReminderSerialized(ctx, sessionName)
					dismissedRateReminder = handled
					if handled {
						lastActivityAt = time.Now()
					}
				}
				continue
			}
			if hasCodexAdditionalSafetyChecksModal(captured) {
				if !dismissedSafetyChecks {
					dismissedSafetyChecks = dismissCodexAdditionalSafetyChecks(ctx, sessionName, captured) == nil
					lastActivityAt = time.Now()
				}
				continue
			}
			if acceptActiveComposer && hasCodexPromptCandidate(captured) {
				return nil
			}
			if hasCodexReadyPrompt(captured) {
				return nil
			}
			// A resumed pane can contain an old "Working ... esc to interrupt"
			// line above a new empty composer. The broad activity scan must keep
			// supporting genuinely long tool output, so use screen stability to
			// disambiguate here: active Codex updates its status/timer, whereas an
			// idle pane with historical activity remains byte-identical.
			if hasCodexPromptCandidate(captured) {
				stableSnapshot := codexPromptCandidateStabilitySnapshot(captured)
				if stableSnapshot != promptCandidateSnapshot {
					promptCandidateSnapshot = stableSnapshot
					promptCandidateSince = time.Now()
				} else if !promptCandidateSince.IsZero() && time.Since(promptCandidateSince) >= codexPromptCandidateStableWindow {
					return nil
				}
			} else {
				promptCandidateSnapshot = ""
				promptCandidateSince = time.Time{}
			}
			if hasCodexActivity(captured) || hasCodexQueuedInput(captured) {
				continue
			}
			if time.Since(lastActivityAt) >= promptWait {
				captured, _ := captureCodexPane(context.Background(), sessionName)
				if strings.TrimSpace(captured) != "" {
					return fmt.Errorf("timed out after %s of inactivity waiting for Codex CLI prompt; %s", promptWait, llmtypes.CompactTerminalPaneForError(sessionName, captured))
				}
				return fmt.Errorf("timed out after %s of inactivity waiting for Codex CLI prompt", promptWait)
			}
		}
	}
}

func sendCodexPromptToTmuxConfirmedBy(ctx context.Context, sessionName, prompt string, oracle codexSubmissionOracle) error {
	return sendCodexInputToTmuxWithReadiness(ctx, sessionName, prompt, true, oracle)
}

// A pane timeout after submission is provisional. Only a new rollout user
// row can overturn it; the arbiter never sends another key.
func codexConfirmInitialSubmitAfterPaneError(ctx context.Context, session *codexInteractiveSession, prompt string, since time.Time, submitErr error) error {
	if submitErr == nil {
		return nil
	}
	ack, err := pollCodexDurableAck(ctx, prompt, since, 0, codexDurableAckBudget(), codexDurableAckPoll{
		resolve: func() string { return resolveCodexRolloutPathNoMu(session, since) },
	})
	if err == nil && ack.Outcome == CodexDurableAckConfirmed {
		return nil
	}
	if err == nil && ack.Outcome == CodexDurableAckUnflushed {
		return &CodexInputUnflushedError{OwnerSessionID: session.ownerSessionID, Latency: ack.Latency}
	}
	return fmt.Errorf("%w; initial Codex delivery unconfirmed by rollout: %w", submitErr, err)
}

func sendCodexInputToTmux(ctx context.Context, ownerSessionID, sessionName, message string) error {
	since := time.Now()
	stashCodexDurableReceiptForSend(ownerSessionID, message, since)
	err := sendCodexInputToTmuxWithReadiness(ctx, sessionName, message, false, nil)
	if err == nil {
		return nil
	}
	return codexArbiterAfterPaneFailure(ctx, ownerSessionID, sessionName, message, since, err)
}

func sendCodexInputToTmuxWithReadiness(ctx context.Context, sessionName, message string, initialPrompt bool, oracle codexSubmissionOracle) error {
	_, err := tmuxinput.Default.Do(ctx, tmuxinput.Request{
		SessionID:       sessionName,
		Source:          "codex-cli",
		BypassReadiness: initialPrompt,
	}, func(ctx context.Context) error {
		return sendCodexInputToTmuxUnserialized(ctx, sessionName, message, oracle)
	})
	return err
}

func sendCodexInputToTmuxUnserialized(ctx context.Context, sessionName, message string, oracle codexSubmissionOracle) error {
	// [LATENCY_DEBUG] timing: this whole function is "how long until Codex
	// actually started working on this message" — pane reset + paste + Enter
	// (pasted) is the mechanical delivery cost; the remaining time up to
	// confirmed is waitForCodexInputSubmitted polling the pane for Codex to
	// visibly pick the input up. Neither half was logged anywhere before —
	// a slow turn with a fast model response looked identical to one where
	// tmux itself was the bottleneck.
	start := time.Now()
	message = strings.TrimRight(message, "\r\n")
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("Codex interactive input is empty")
	}
	waitForCodexPaneOutOfStartup(ctx, sessionName, codexStartupPasteWait, captureCodexPane)
	// Clear any stale unsent draft inside the same broker transaction as paste +
	// submit. The previous implementation cleared outside the transaction and
	// then performed a second multi-second readiness wait on every turn.
	resetCodexPaneForTurn(ctx, sessionName)
	baseline, _ := captureCodexPane(ctx, sessionName)
	bufferName := "mlp-codex-input-" + codexRandomHex(6)
	tmp, err := os.CreateTemp("", "codex-tmux-input-*.txt")
	if err != nil {
		return fmt.Errorf("failed to create Codex tmux input temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(message); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write Codex tmux input temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close Codex tmux input temp file: %w", err)
	}
	if err := runCodexCommand(ctx, nil, "tmux", "load-buffer", "-b", bufferName, tmpPath); err != nil {
		return fmt.Errorf("failed to load Codex input into tmux buffer: %w", err)
	}
	if err := runCodexCommand(ctx, nil, "tmux", "paste-buffer", "-d", "-p", "-r", "-b", bufferName, "-t", sessionName); err != nil {
		return fmt.Errorf("failed to paste input into Codex interactive session: %w", err)
	}
	pasted := time.Since(start)
	// Codex 0.142's TUI accepts tmux's literal Enter key here, while C-m can
	// leave the pasted text sitting in the input buffer without starting a turn.
	// Send the submit key twice in the same tmux operation: production has
	// observed a single Enter being swallowed even though tmux accepted it.
	submit := func(ctx context.Context) error {
		args := append([]string{"send-keys", "-t", sessionName}, codexSubmitKeys()...)
		if err := runCodexCommand(ctx, nil, "tmux", args...); err != nil {
			return fmt.Errorf("failed to submit input to Codex interactive session: %w", err)
		}
		return nil
	}
	if err := submit(ctx); err != nil {
		return err
	}
	err = waitForCodexInputSubmittedWith(ctx, sessionName, message, baseline, 8*time.Second, codexSubmissionWait{
		oracle:   oracle,
		resubmit: submit,
	})
	log.Printf("[LATENCY_DEBUG] codex tmux delivery | session=%s pasted=%dms confirmed=%dms err=%v",
		sessionName, pasted.Milliseconds(), time.Since(start).Milliseconds(), err)
	return err
}

func codexSubmitKeys() []string {
	return []string{"Enter", "Enter"}
}

func waitForCodexInputSubmitted(ctx context.Context, sessionName, message, baseline string, timeout time.Duration) error {
	return waitForCodexInputSubmittedWith(ctx, sessionName, message, baseline, timeout, codexSubmissionWait{})
}

// waitForCodexInputSubmittedWith confirms that Enter started a turn. With an
// authoritative oracle (the session's rollout) only the oracle can confirm;
// the pane heuristics are the fallback for sessions whose rollout is not
// bound yet. Either way, a prompt that is still an unsent draft after
// resubmitAfter gets Enter again: Codex 0.153 spends the first Enter on
// dismissing its "Conversation interrupted" notice.
func waitForCodexInputSubmittedWith(ctx context.Context, sessionName, message, baseline string, timeout time.Duration, wait codexSubmissionWait) error {
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	capture := wait.capture
	if capture == nil {
		capture = captureCodexPane
	}
	resubmitAfter := wait.resubmitAfter
	if resubmitAfter <= 0 {
		resubmitAfter = codexDefaultResubmitAfter
	}
	maxResubmits := wait.maxResubmits
	if maxResubmits <= 0 {
		maxResubmits = codexDefaultMaxResubmits
	}
	baselineSnapshot := codexPromptCandidateStabilitySnapshot(baseline)
	var lastCapture string
	var lastErr error
	lastSubmit := time.Now()
	resubmits := 0
	for {
		strict := false
		if wait.oracle != nil {
			started, authoritative := wait.oracle()
			if started {
				return nil
			}
			strict = authoritative
		}
		captured, err := capture(deadline, sessionName)
		if err == nil {
			lastCapture = captured
			transitioned := codexPromptCandidateStabilitySnapshot(captured) != baselineSnapshot
			// This modal is only shown after Codex has accepted the submitted
			// request. Let the response watcher dismiss it after this broker
			// transaction releases the session input lane.
			if hasCodexAdditionalSafetyChecksModal(captured) {
				return nil
			}
			// Historical Working/tool lines may already exist in baseline. They
			// prove the CLI is active, not that this input moved. Require a pane
			// transition after Enter before accepting activity as confirmation.
			// Normalize rotating empty-composer hints so their animation alone
			// cannot masquerade as a submitted turn.
			if !strict && transitioned && (hasCodexActivity(captured) || hasCodexQueuedInput(captured)) {
				return nil
			}
			// A very short turn can already be back at an empty composer before
			// the first capture. Accept the bottom composer as authoritative even
			// when older Working lines in scrollback make hasCodexReadyPrompt
			// conservative. Do not scan submitted history as though it were the
			// still-active editor draft.
			if !strict && transitioned && codexPaneHasEmptyComposer(captured) && !codexPaneShowsPromptDraft(captured, message) {
				return nil
			}
			if wait.resubmit != nil && resubmits < maxResubmits && time.Since(lastSubmit) >= resubmitAfter &&
				codexPaneHoldsUnsentDraft(captured, message) {
				log.Printf("[codex] input still an unsent draft after %s; pressing Enter again (session=%s attempt=%d)",
					time.Since(lastSubmit).Round(time.Millisecond), sessionName, resubmits+1)
				if err := wait.resubmit(deadline); err != nil {
					return err
				}
				resubmits++
				lastSubmit = time.Now()
			}
		} else {
			lastErr = err
		}
		select {
		case <-deadline.Done():
			if lastErr != nil && strings.TrimSpace(lastCapture) == "" {
				return fmt.Errorf("could not confirm Codex input submission: %w", lastErr)
			}
			return fmt.Errorf("Codex input remained unconfirmed after %s; %s", timeout, llmtypes.CompactTerminalPaneForError(sessionName, lastCapture))
		case <-ticker.C:
		}
	}
}

func codexPaneShowsPromptDraft(captured, message string) bool {
	lines := strings.Split(stripCodexANSI(captured), "\n")
	needle := strings.TrimSpace(strings.Split(strings.TrimSpace(message), "\n")[0])
	if len(needle) > 80 {
		needle = needle[:80]
	}
	seen := 0
	for i := len(lines) - 1; i >= 0 && seen < 12; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		seen++
		lower := strings.ToLower(line)
		if (strings.Contains(lower, "pasted text #") || strings.Contains(lower, "pasted text ")) &&
			(strings.HasPrefix(line, "›") || strings.HasPrefix(line, ">") || strings.HasPrefix(line, "❯")) {
			return true
		}
		// Only the bottom-most composer is active. Older `› user message`
		// lines above an empty composer are submitted conversation history.
		if isCodexGhostPlaceholderPromptLine(line) || line == "›" || line == ">" || line == "❯" {
			return false
		}
		if isCodexPromptWithInputLine(line) {
			return needle == "" || strings.Contains(line, needle)
		}
	}
	return false
}

func codexPaneHasEmptyComposer(captured string) bool {
	if hasCodexTrustPrompt(captured) || hasCodexHookTrustReviewPrompt(captured) || hasCodexRateLimitReminderModal(captured) || hasCodexAdditionalSafetyChecksModal(captured) || hasCodexQueuedInput(captured) {
		return false
	}
	if codexPaneIsResuming(captured) {
		return false
	}
	lines := strings.Split(stripCodexANSI(captured), "\n")
	seen := 0
	for i := len(lines) - 1; i >= 0 && seen < 12; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		seen++
		if isCodexGhostPlaceholderPromptLine(line) || line == "›" || line == ">" || line == "❯" {
			return true
		}
		if isCodexPromptWithInputLine(line) {
			return false
		}
	}
	return false
}

func waitForCodexInteractiveResponse(ctx context.Context, sessionName, baseline string, streamChan chan<- llmtypes.StreamChunk, turnStart time.Time, workingDir string, streamTranscript bool, streamTerminalScreen bool, resolveRollout func(time.Time) string, diagnostics *codexCompletionDiagnosticHooks) (string, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	quietWatch := &stalldiag.Watch{Provider: "codex-cli", Session: sessionName}
	stalePaneBackstop := codexInteractiveStalePaneBackstop()
	nativeTurnWait := codexInteractivePromptMaxWait()
	var sawActivity bool
	var idleSince time.Time
	var lastCaptured string
	var lastTerminalSnapshot string
	var lastTerminalStreamedAt time.Time
	var dismissedRateReminder bool
	var dismissedSafetyChecks bool
	// Codex can write its native completion event just before rendering a
	// follow-up runtime modal (notably the rate-limit reminder). Keep watching
	// briefly after that event so the modal is dismissed before this session is
	// released to the next workflow stage.
	var completionObservedAt time.Time
	const postCompletionModalGrace = time.Second
	// Stale-pane backstop tracking: the raw capture from the previous tick and
	// the time it last changed. Tracked at the top of every tick, independent of
	// all the branch logic below, so a prompt-detection bug that keeps the loop
	// in a "not ready" branch can never suppress it.
	var backstopPrevCapture string
	var paneUnchangedSince time.Time
	completionTracker := newCodexTurnCompletionTracker(turnStart, workingDir, resolveRollout)
	completionTracker.setDiagnosticHooks(diagnostics)
	// Opt-in structured streaming: tail the rollout JSONL mid-turn so a
	// design-first UI gets assistant text + tool-call starts without the pane.
	// Additive; nil when disabled. Runs inside this loop (which returns before
	// the adapter closes streamChan), so there is no send-on-closed-chan race.
	var transcriptStream *codexTranscriptStreamState
	if streamChan != nil && streamTranscript {
		transcriptStream = newCodexTranscriptStreamState(turnStart, workingDir, resolveRollout)
	}
	for {
		select {
		case <-ctx.Done():
			// PLAT-160: a tool call's rollout row written in the gap between
			// the last tick and cancellation (normal completion, timeout, or
			// Stop) used to be lost entirely -- this branch returned without
			// ever polling again. context.Background() here matches
			// cursorcli's transcript tailer, which already does a final
			// flush this way: poll's own streamChan send also selects on its
			// context, so using the now-cancelled ctx here would race that
			// same cancellation and could drop the send.
			if transcriptStream != nil {
				transcriptStream.poll(context.Background(), streamChan)
			}
			captured, _ := captureCodexPane(context.Background(), sessionName)
			return captured, ctx.Err()
		case <-ticker.C:
			captured, err := captureCodexPane(ctx, sessionName)
			if err != nil {
				return "", err
			}
			quietWatch.Observe(time.Now(), captured, true)
			// Codex is waiting on its own sign-in screen: the turn can never start.
			if codexPaneAsksForSignIn(captured) {
				return captured, &CodexLoginRequiredError{}
			}
			delta := codexCapturedAfterBaseline(captured, baseline)
			stableCapture := codexPromptCandidateStabilitySnapshot(captured)
			if tmuxcontrol.ConsumeForceComplete(sessionName) {
				return captured, tmuxcontrol.ErrForceComplete
			}
			// Poll the rollout tail BEFORE the completion/return checks so the
			// final tick (where the rollout gets its last rows and completion is
			// detected) still streams that content before the loop returns.
			if transcriptStream != nil {
				transcriptStream.poll(ctx, streamChan)
			}
			// These menus can be drawn immediately after Codex's final-answer
			// transcript event. They must be handled before honoring completion,
			// otherwise the caller advances while an orphaned Codex process still
			// owns this tmux pane.
			if hasCodexRateLimitReminderModal(captured) {
				if !dismissedRateReminder {
					handled, _ := dismissCodexRateLimitReminderSerialized(ctx, sessionName)
					dismissedRateReminder = handled
					if handled {
						completionObservedAt = time.Time{}
					}
				}
				sawActivity = true
				idleSince = time.Time{}
				lastCaptured = stableCapture
				continue
			}
			if hasCodexAdditionalSafetyChecksModal(captured) {
				if !dismissedSafetyChecks {
					handled, _ := dismissCodexAdditionalSafetyChecksSerialized(ctx, sessionName)
					dismissedSafetyChecks = handled
					if handled {
						completionObservedAt = time.Time{}
					}
				}
				sawActivity = true
				idleSince = time.Time{}
				lastCaptured = stableCapture
				continue
			}
			// Prefer Codex's native per-turn completion event. Terminal status
			// lines are retained in tmux scrollback and are therefore only a
			// compatibility fallback, not the source of truth for liveness.
			if completionTracker.completed() {
				// Esc in the TUI (or a client interrupt) ends the turn with
				// turn_aborted and no task_complete. Waiting on would hang until
				// the deadline, and parsing the pane would hand back whatever
				// older text is still visible as if it were the answer.
				if aborted, reason := completionTracker.aborted(); aborted {
					return captured, fmt.Errorf("codex-cli turn failed: %s", codexTurnAbortedMessage(reason))
				}
				if completionObservedAt.IsZero() {
					completionObservedAt = time.Now()
					continue
				}
				if time.Since(completionObservedAt) < postCompletionModalGrace {
					continue
				}
				diagnostics.reportCompletedBy(codexCompletionSourceRollout)
				return captured, nil
			}
			rolloutBlocksTerminalFallback := completionTracker.blocksTerminalFallback()
			// Once the rollout records this turn it is the authority (PLAT-354):
			// pane idleness and the stale-pane backstop no longer end the turn,
			// except through the bounded late fallback below.
			rolloutOwnsTurn := completionTracker.rolloutOwnsTurn()
			// A cold Codex session can draw an idle composer before it writes the
			// user row needed to bind its rollout. That gap is not a reply: the
			// pane can contain only the runtime directory (PLAT-861-237). When an
			// exact native resolver is supplied, it is authoritative even before
			// binding. Reuse the existing start budget if no native turn appears.
			terminalMayComplete := resolveRollout == nil && !rolloutOwnsTurn
			if resolveRollout != nil && !rolloutOwnsTurn && time.Since(turnStart) >= nativeTurnWait {
				diagnostics.reportCompletedBy("native_turn_timeout")
				return captured, errors.New("Codex did not return a reply. Open its live view to check progress, or choose another model.")
			}
			// Stale-pane backstop. Independent of hasCodexReadyPrompt and every
			// branch below: if the pane has produced activity and then frozen
			// (byte-identical) for longer than the backstop, the turn is over but
			// completion detection failed to recognize it. Return the pane the same
			// way the ready-prompt success path does (return captured, nil) and let
			// the caller extract — codexcli's response extraction needs the prompt
			// and history which the success path also defers to the caller. Only
			// armed once sawActivity is set, so a never-delivered prompt (pane never
			// changed from baseline) never trips it.
			if stableCapture != backstopPrevCapture {
				backstopPrevCapture = stableCapture
				paneUnchangedSince = time.Now()
			} else if sawActivity && stalePaneBackstop > 0 && !paneUnchangedSince.IsZero() &&
				time.Since(paneUnchangedSince) >= stalePaneBackstop &&
				terminalMayComplete &&
				hasCodexPromptCandidate(captured) && hasCodexExplicitCompletedMarker(captured) {
				diagnostics.reportCompletedBy(codexCompletionSourcePane)
				return captured, nil
			}
			if streamChan != nil && streamTerminalScreen {
				if time.Since(lastTerminalStreamedAt) >= time.Second && streamCodexTerminalSnapshot(ctx, sessionName, streamChan, &lastTerminalSnapshot) {
					lastTerminalStreamedAt = time.Now()
				}
			}
			if rolloutBlocksTerminalFallback {
				// Codex 0.144 can redraw an idle composer while an MCP request is
				// still outstanding. Treat the rollout's unmatched call (or
				// commentary without final_answer) as authoritative liveness.
				sawActivity = true
				idleSince = time.Time{}
				lastCaptured = stableCapture
				continue
			}
			if hasCodexQueuedInput(delta) {
				sawActivity = true
				idleSince = time.Time{}
				lastCaptured = stableCapture
				continue
			}
			if hasCodexActivity(delta) {
				sawActivity = true
				idleSince = time.Time{}
				lastCaptured = stableCapture
				continue
			}
			if strings.TrimSpace(delta) != "" {
				sawActivity = true
			}
			if !sawActivity || !hasCodexReadyPrompt(captured) {
				idleSince = time.Time{}
				lastCaptured = stableCapture
				continue
			}
			if stableCapture != lastCaptured {
				lastCaptured = stableCapture
				idleSince = time.Now()
				continue
			}
			if idleSince.IsZero() {
				idleSince = time.Now()
				continue
			}
			if time.Since(idleSince) >= codexInteractiveStableWindow {
				if terminalMayComplete {
					diagnostics.reportCompletedBy(codexCompletionSourcePane)
					return captured, nil
				}
				if completionTracker.finalAnswerSettled() && time.Since(idleSince) >= codexRolloutLateFallback {
					diagnostics.reportCompletedBy(codexCompletionSourceRolloutLateFallback)
					return captured, nil
				}
			}
		}
	}
}

func parseCodexInteractiveResponse(captured, baseline, echoedUserPrompt string, historicalAssistantTexts []string) string {
	if hasCodexTrustPrompt(captured) {
		return ""
	}
	delta := codexCapturedAfterBaseline(captured, baseline)
	text := rawFramedCodexAnswer(delta)
	if strings.TrimSpace(text) == "" {
		return parseCodexInteractiveResponseSegmentFallback(delta, echoedUserPrompt, historicalAssistantTexts)
	}
	text = stripCodexEchoedUserPrompt(text, echoedUserPrompt)
	text = stripCodexHistoricalAssistantText(text, historicalAssistantTexts)
	if strings.TrimSpace(text) == "" {
		text = codexTerminalTailTextFallback(normalizeCodexPaneSnapshot(delta).Segments, 24)
		text = stripCodexEchoedUserPrompt(text, echoedUserPrompt)
		text = stripCodexHistoricalAssistantText(text, historicalAssistantTexts)
	}
	if isCodexLikelyQueuedUserEcho(text) {
		return ""
	}
	return strings.TrimSpace(text)
}

func forcedCodexInteractiveResponse(captured, baseline, echoedUserPrompt string, historicalAssistantTexts []string) string {
	delta := codexCapturedAfterBaseline(captured, baseline)
	text := parseCodexInteractiveResponseSegmentFallback(delta, echoedUserPrompt, historicalAssistantTexts)
	if strings.TrimSpace(text) != "" {
		return strings.TrimSpace(text)
	}
	text = stripCodexEchoedUserPrompt(stripCodexANSI(delta), echoedUserPrompt)
	text = stripCodexHistoricalAssistantText(text, historicalAssistantTexts)
	return strings.TrimSpace(text)
}

func codexPolicyInvalidPromptError(captured string) error {
	if hasCodexPolicyInvalidPrompt(captured) {
		return errors.New("Codex CLI rejected the prompt as policy invalid")
	}
	return nil
}

func codexPolicyInvalidPromptTextError(text string) error {
	if hasCodexPolicyInvalidPromptText(text) {
		return errors.New("Codex CLI rejected the prompt as policy invalid")
	}
	return nil
}

func hasCodexPolicyInvalidPrompt(captured string) bool {
	return hasCodexPolicyInvalidPromptText(stripCodexANSI(captured))
}

func hasCodexPolicyInvalidPromptText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "invalid prompt") &&
		(strings.Contains(lower, "usage policy") || strings.Contains(lower, "violating our usage policy")) {
		return true
	}
	if strings.Contains(lower, "potentially violating our usage policy") {
		return true
	}
	// Codex sometimes leaves only the tail URL visible/extracted after the
	// invalid-prompt banner scrolls. Treat a short answer containing only that
	// docs pointer as the same fatal provider rejection, not a real answer.
	return strings.Contains(lower, "platform.openai.com/docs/guides/reasoning#advice-on-prompting") &&
		len([]rune(lower)) < 260
}

func rawFramedCodexAnswer(delta string) string {
	lines := normalizeCodexPaneLines(delta)
	ruleIndices := make([]int, 0, 4)
	for i, line := range lines {
		if isCodexHorizontalRuleLine(line) {
			ruleIndices = append(ruleIndices, i)
		}
	}
	if len(ruleIndices) >= 2 {
		for i := len(ruleIndices) - 1; i > 0; i-- {
			text := cleanCodexRawFinalLines(lines[ruleIndices[i-1]+1:ruleIndices[i]], false)
			if strings.TrimSpace(text) != "" && !isCodexLikelyToolReplayFinalText(text) {
				return text
			}
		}
	}
	if len(ruleIndices) == 1 {
		text := cleanCodexRawFinalLines(lines[ruleIndices[0]+1:], true)
		if isCodexLikelyToolReplayFinalText(text) {
			return ""
		}
		return text
	}
	return ""
}

func isCodexLikelyToolReplayFinalText(text string) bool {
	lines := nonEmptyCodexLines(text)
	if len(lines) == 0 {
		return true
	}
	if isCodexAssistantAnswerPathSegment(lines) {
		return false
	}
	toolish := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if isCodexToolReplayLine(trimmed) ||
			isCodexToolReplayContinuationLine(trimmed) ||
			isCodexShellScriptContinuationLine(trimmed) ||
			isCodexJSONToolOutputLine(trimmed) ||
			strings.Contains(lower, `,"timeout":`) ||
			strings.Contains(lower, `"timeout":`) && strings.Contains(lower, "})") ||
			strings.Contains(lower, "py\",\"timeout") ||
			strings.Contains(lower, "indent=2))") {
			toolish++
		}
	}
	return toolish == len(lines)
}

func cleanCodexRawFinalLines(lines []string, stopAtPrompt bool) string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || isCodexHorizontalRuleLine(trimmed) {
			continue
		}
		if stopAtPrompt && isCodexPromptBoundaryLine(trimmed) {
			break
		}
		out = append(out, trimmed)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func isCodexPromptBoundaryLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	return trimmed == "›" ||
		trimmed == ">" ||
		trimmed == "❯" ||
		strings.HasPrefix(trimmed, "› ") ||
		strings.HasPrefix(lower, "gpt-") && strings.Contains(lower, "·") ||
		strings.Contains(lower, "openai codex")
}

func parseCodexInteractiveResponseSegmentFallback(delta, echoedUserPrompt string, historicalAssistantTexts []string) string {
	snapshot := normalizeCodexPaneSnapshot(delta)
	text := framedAssistantTextFromCodexSegments(snapshot.Segments)
	if strings.TrimSpace(text) == "" {
		text = finalAssistantTextFromCodexSegments(snapshot.Segments)
	}
	if strings.TrimSpace(text) == "" {
		text = snapshot.AssistantText
	}
	if strings.TrimSpace(text) == "" {
		text = codexTerminalTailTextFallback(snapshot.Segments, 24)
	}
	text = stripCodexEchoedUserPrompt(text, echoedUserPrompt)
	text = stripCodexHistoricalAssistantText(text, historicalAssistantTexts)
	if isCodexLikelyQueuedUserEcho(text) {
		return ""
	}
	return strings.TrimSpace(text)
}

func codexTerminalTailTextFallback(segments []codexSegment, maxLines int) string {
	if maxLines <= 0 {
		maxLines = 24
	}
	lines := make([]string, 0, maxLines)
	for _, segment := range segments {
		if segment.Kind != codexSegmentAssistant {
			continue
		}
		for _, line := range segment.Lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || isCodexPromptBoundaryLine(trimmed) || isCodexTUILine(trimmed) {
				continue
			}
			lines = append(lines, trimmed)
		}
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func isCodexLikelyQueuedUserEcho(text string) bool {
	lines := nonEmptyCodexLines(text)
	if len(lines) == 0 {
		return false
	}
	lower := strings.ToLower(strings.Join(lines, "\n"))
	if strings.Contains(lower, "pre-validation failed") &&
		strings.Contains(lower, "checks:") &&
		(strings.Contains(lower, "fix the specific issues") ||
			strings.Contains(lower, "validation failed") ||
			strings.Contains(lower, "must exist")) {
		return true
	}
	return false
}

func extractCodexVisibleAssistantText(delta string) string {
	return normalizeCodexPaneSnapshot(delta).AssistantText
}

type codexSegmentKind string

const (
	codexSegmentAssistant   codexSegmentKind = "assistant_text"
	codexSegmentToolStatus  codexSegmentKind = "tool_status"
	codexSegmentPlanStatus  codexSegmentKind = "plan_status"
	codexSegmentChrome      codexSegmentKind = "terminal_chrome"
	codexSegmentInputEcho   codexSegmentKind = "input_echo"
	codexSegmentErrorStatus codexSegmentKind = "error_status"
)

type codexSegment struct {
	Kind  codexSegmentKind
	Lines []string
}

type codexNormalizedSnapshot struct {
	AssistantText string
	Fingerprint   string
	Segments      []codexSegment
}

func normalizeCodexPaneSnapshot(raw string) codexNormalizedSnapshot {
	lines, wrapEligible := normalizeCodexPaneLinesWithMeta(raw)
	segments := segmentCodexLines(lines, wrapEligible)
	assistantText := assistantTextFromCodexSegments(segments)
	return codexNormalizedSnapshot{
		AssistantText: assistantText,
		Fingerprint:   codexSnapshotFingerprint(assistantText),
		Segments:      segments,
	}
}

func normalizeCodexPaneLines(raw string) []string {
	lines, _ := normalizeCodexPaneLinesWithMeta(raw)
	return lines
}

// normalizeCodexPaneLinesWithMeta is normalizeCodexPaneLines plus a parallel
// wrapEligible flag per emitted line. A line is wrap-eligible when its raw
// source line was indented and carried NO "•" event-block bullet — i.e. it is a
// candidate continuation of a wrapped "›" prompt that the TUI split across
// terminal rows. normalizeCodexPaneLine trims the leading indentation and the
// bullet, destroying that signal, so we capture it here before normalization.
func normalizeCodexPaneLinesWithMeta(raw string) ([]string, []bool) {
	raw = normalizeCodexInlineTUIBoundaries(raw)
	raw = expandCodexEscapedToolNewlines(raw)
	rawLines := strings.Split(raw, "\n")
	lines := make([]string, 0, len(rawLines))
	wrapEligible := make([]bool, 0, len(rawLines))
	for _, line := range rawLines {
		eligible := isCodexWrapEligibleRawLine(line)
		normalized := normalizeCodexPaneLine(line)
		for _, splitLine := range splitCodexWorkingStatusPrefix(normalized) {
			if splitLine != "" && !isCodexBulletOnlyLine(splitLine) {
				lines = append(lines, splitLine)
				wrapEligible = append(wrapEligible, eligible)
			}
		}
	}
	return lines, wrapEligible
}

// isCodexWrapEligibleRawLine reports whether a raw pane line (ANSI stripped)
// COULD be a wrapped continuation of the preceding line, rather than the start
// of a new event/message. Codex marks each new event/message block with a "•"
// bullet, so the one thing that reliably rules a line OUT is that bullet; a
// blank line also can't be a continuation.
//
// This intentionally does NOT require leading indentation. Indentation is a
// real signal for codex's OWN multi-line prompt box (it pads continuation rows
// of a prompt the user typed as several lines), but it is NOT present when the
// terminal itself hard-wraps ONE long line at the pane's column width — e.g. a
// long file path in a prompt or tool-call narration — which continues flush at
// column 0 with no indentation at all. Gating solely on indentation meant a
// hard-wrapped continuation of a "›" prompt echo failed this check, was
// classified as ordinary assistant text, and leaked into the extracted final
// answer (reproduced live: a long activity path wrapping the pane leaked its
// tail, e.g. "ons/answer_key.txt", straight into the reply). The real
// containment against wrongly absorbing genuine answer content is downstream
// in isCodexPromptWrapContinuationLine (requires an ACTIVE chrome segment that
// already has a "›" prompt marker) and segmentCodexLines' Kind-change flush
// (a bulleted answer line always flushes the chrome segment first) — this
// function only needs to rule out "is structurally its own new block".
func isCodexWrapEligibleRawLine(line string) bool {
	stripped := stripCodexANSI(line)
	if strings.TrimSpace(stripped) == "" {
		return false
	}
	return !strings.HasPrefix(strings.TrimSpace(stripped), "•")
}

func expandCodexEscapedToolNewlines(raw string) string {
	parts := strings.Split(raw, `\n`)
	if len(parts) == 1 {
		return raw
	}
	var b strings.Builder
	b.WriteString(parts[0])
	for _, part := range parts[1:] {
		if isCodexEscapedToolNewlinePrefix(part) {
			b.WriteByte('\n')
		} else {
			b.WriteString(`\n`)
		}
		b.WriteString(part)
	}
	return b.String()
}

func isCodexEscapedToolNewlinePrefix(s string) bool {
	trimmed := strings.TrimSpace(s)
	return strings.HasPrefix(trimmed, "total ") ||
		codexShellListingPattern.MatchString(trimmed) ||
		strings.HasPrefix(trimmed, "./") ||
		strings.HasPrefix(trimmed, `"stdout"`) ||
		strings.HasPrefix(trimmed, `"stderr"`) ||
		strings.HasPrefix(trimmed, `"exit_code"`) ||
		strings.HasPrefix(trimmed, `"execution_time_ms"`) ||
		strings.Contains(trimmed, " &&") ||
		strings.Contains(trimmed, " |")
}

func normalizeCodexPaneLine(line string) string {
	line = strings.TrimSpace(stripCodexANSI(line))
	line = strings.TrimSpace(strings.TrimPrefix(line, "• "))
	return line
}

var codexWorkingStatusPrefixPattern = regexp.MustCompile(`^(Working \([0-9]+s\)?)\s+(.+)$`)

func splitCodexWorkingStatusPrefix(line string) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	matches := codexWorkingStatusPrefixPattern.FindStringSubmatch(line)
	if len(matches) != 3 {
		return []string{line}
	}
	return []string{strings.TrimSpace(matches[1]), strings.TrimSpace(matches[2])}
}

func segmentCodexLines(lines []string, wrapEligible []bool) []codexSegment {
	segments := make([]codexSegment, 0, len(lines))
	current := codexSegment{}
	flush := func() {
		if len(current.Lines) > 0 {
			segments = append(segments, current)
		}
		current = codexSegment{}
	}

	for i, line := range lines {
		nextLine := ""
		if i+1 < len(lines) {
			nextLine = lines[i+1]
		}
		lineWrapEligible := i < len(wrapEligible) && wrapEligible[i]
		if current.Kind == codexSegmentToolStatus &&
			(isCodexToolContinuationLine(line) || isCodexContextualToolContinuationLine(current.Lines, line, nextLine)) {
			current.Lines = append(current.Lines, line)
			continue
		}
		if current.Kind == codexSegmentPlanStatus && isCodexPlanContinuationLine(line) {
			current.Lines = append(current.Lines, line)
			continue
		}
		if current.Kind == codexSegmentAssistant && isCodexAssistantAnswerContinuationLine(current.Lines, line) {
			current.Lines = append(current.Lines, line)
			continue
		}
		if current.Kind == codexSegmentChrome && lineWrapEligible &&
			isCodexPromptWrapContinuationLine(current.Lines, line) {
			current.Lines = append(current.Lines, line)
			continue
		}

		kind := classifyCodexLine(line)
		if len(current.Lines) == 0 {
			current = codexSegment{Kind: kind, Lines: []string{line}}
			continue
		}
		if current.Kind != kind {
			flush()
			current = codexSegment{Kind: kind, Lines: []string{line}}
			continue
		}
		current.Lines = append(current.Lines, line)
	}
	flush()
	return segments
}

func classifyCodexLine(line string) codexSegmentKind {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	switch {
	case isCodexInputEchoLine(trimmed), isCodexQueuedInputLine(trimmed):
		return codexSegmentInputEcho
	case strings.HasPrefix(lower, "updated plan"):
		return codexSegmentPlanStatus
	case isCodexToolStatusLine(trimmed),
		isCodexToolReplayLine(trimmed),
		strings.HasPrefix(lower, "searching the web"),
		strings.HasPrefix(lower, "searched http"),
		strings.HasPrefix(lower, "spawned "),
		strings.HasPrefix(lower, "waiting for "),
		strings.HasPrefix(lower, "finished waiting"),
		strings.HasPrefix(lower, "working ("),
		strings.HasPrefix(trimmed, "└"),
		strings.HasPrefix(trimmed, "├"),
		strings.HasPrefix(trimmed, "│"):
		return codexSegmentToolStatus
	case strings.HasPrefix(trimmed, "✔"),
		strings.HasPrefix(trimmed, "✓"),
		strings.HasPrefix(trimmed, "□"),
		strings.HasPrefix(trimmed, "☐"):
		return codexSegmentPlanStatus
	case isCodexErrorStatusLine(trimmed):
		return codexSegmentErrorStatus
	case isCodexTUILine(trimmed), isCodexShellStartupNoiseLine(trimmed):
		return codexSegmentChrome
	default:
		return codexSegmentAssistant
	}
}

var codexShellAgentPIDPattern = regexp.MustCompile(`^Agent pid [0-9]+$`)

// isCodexShellStartupNoiseLine recognizes output emitted by a user's login
// shell before the Codex TUI takes over the pane. In particular, common
// ssh-agent setup snippets print these lines the first time a shell starts.
// They are terminal bootstrap output, never a Codex assistant response. If
// they are parsed as an answer, a newly launched session can appear to finish
// before its positional prompt has even started.
func isCodexShellStartupNoiseLine(line string) bool {
	trimmed := strings.TrimSpace(stripCodexANSI(line))
	return codexShellAgentPIDPattern.MatchString(trimmed) ||
		strings.HasPrefix(trimmed, "Identity added: ")
}

func assistantTextFromCodexSegments(segments []codexSegment) string {
	out := make([]string, 0, len(segments))
	for i, segment := range segments {
		if segment.Kind == codexSegmentAssistant {
			var prevKind, nextKind codexSegmentKind
			if i > 0 {
				prevKind = segments[i-1].Kind
			}
			if i+1 < len(segments) {
				nextKind = segments[i+1].Kind
			}
			if isCodexLikelyToolReplayAssistantSegment(segment.Lines, prevKind, nextKind) {
				continue
			}
			out = append(out, segment.Lines...)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func finalAssistantTextFromCodexSegments(segments []codexSegment) string {
	for i := len(segments) - 1; i >= 0; i-- {
		segment := segments[i]
		if segment.Kind != codexSegmentAssistant {
			continue
		}
		var prevKind, nextKind codexSegmentKind
		if i > 0 {
			prevKind = segments[i-1].Kind
		}
		if i+1 < len(segments) {
			nextKind = segments[i+1].Kind
		}
		if isCodexLikelyToolReplayAssistantSegment(segment.Lines, prevKind, nextKind) {
			continue
		}
		return strings.TrimSpace(strings.Join(segment.Lines, "\n"))
	}
	return ""
}

func framedAssistantTextFromCodexSegments(segments []codexSegment) string {
	for i := len(segments) - 1; i >= 0; i-- {
		segment := segments[i]
		if segment.Kind != codexSegmentAssistant {
			continue
		}
		var prev, next codexSegment
		var prevKind, nextKind codexSegmentKind
		if i > 0 {
			prev = segments[i-1]
			prevKind = prev.Kind
		}
		if i+1 < len(segments) {
			next = segments[i+1]
			nextKind = next.Kind
		}
		if isCodexLikelyToolReplayAssistantSegment(segment.Lines, prevKind, nextKind) {
			continue
		}
		if isCodexHorizontalRuleSegment(prev) && isCodexHorizontalRuleSegment(next) {
			return strings.TrimSpace(strings.Join(segment.Lines, "\n"))
		}
	}
	return ""
}

func isCodexHorizontalRuleSegment(segment codexSegment) bool {
	if segment.Kind != codexSegmentChrome {
		return false
	}
	for _, line := range segment.Lines {
		if isCodexHorizontalRuleLine(line) {
			return true
		}
	}
	return false
}

func isCodexHorizontalRuleLine(line string) bool {
	trimmed := strings.TrimSpace(stripCodexANSI(line))
	if trimmed == "" {
		return false
	}
	dashCount := 0
	otherCount := 0
	for _, r := range trimmed {
		switch r {
		case '─', '━', '╌', '╍':
			dashCount++
		case ' ':
		default:
			otherCount++
		}
	}
	return dashCount >= 20 && otherCount == 0
}

func codexSnapshotFingerprint(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

func isCodexToolContinuationLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "└") ||
		strings.HasPrefix(trimmed, "├") ||
		strings.HasPrefix(trimmed, "│") ||
		isCodexToolReplayContinuationLine(trimmed) ||
		isCodexErrorStatusLine(trimmed)
}

func isCodexPlanContinuationLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "└") ||
		strings.HasPrefix(trimmed, "├") ||
		strings.HasPrefix(trimmed, "│") ||
		strings.HasPrefix(trimmed, "✔") ||
		strings.HasPrefix(trimmed, "✓") ||
		strings.HasPrefix(trimmed, "□") ||
		strings.HasPrefix(trimmed, "☐")
}

func isCodexErrorStatusLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	return strings.HasPrefix(lower, "error: resources/") ||
		strings.HasPrefix(lower, "error: tools/") ||
		strings.HasPrefix(lower, "error: ") && strings.Contains(lower, "mcp server")
}

func isCodexInputEchoLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	return strings.Contains(lower, "sent immediately") ||
		trimmed == "immediately)" ||
		strings.HasPrefix(trimmed, "↳ ")
}

func isCodexQueuedInputLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(stripCodexANSI(line)))
	return strings.Contains(lower, "messages to be submitted after next tool call") ||
		strings.Contains(lower, "press esc to interrupt and send immediately")
}

var codexInlineTUIBoundaryPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(^|\s+)(Working \([0-9]+s\)?)`),
	regexp.MustCompile(`\s+•+\s*`),
	regexp.MustCompile(`\s+(Called|Calling) [A-Za-z0-9_-]+[\w.\-]*(?:\(|\{|\s|$)`),
	regexp.MustCompile(`\s+(Updated Plan|Searching the web|Searched https?://|Spawned [A-Z][^ ]*|Waiting for [A-Z][^ ]*|Finished waiting)(?:\s|$)`),
	regexp.MustCompile(`\s+(Error: (resources|tools)/[^ ]+ failed:|Error: .*unknown MCP server)`),
	regexp.MustCompile(`\s+([└├│✔✓□☐])\s+`),
}

func normalizeCodexInlineTUIBoundaries(text string) string {
	out := text
	for _, pattern := range codexInlineTUIBoundaryPatterns {
		out = pattern.ReplaceAllStringFunc(out, func(match string) string {
			return "\n" + strings.TrimLeft(match, " \t\r\n")
		})
	}
	return out
}

func isCodexTUILine(line string) bool {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	if isCodexBulletOnlyLine(trimmed) {
		return true
	}
	if isCodexTrustPromptLine(trimmed) {
		return true
	}
	if isCodexRateLimitReminderLine(trimmed) {
		return true
	}
	if isCodexAdditionalSafetyChecksLine(trimmed) {
		return true
	}
	if line == "›" || line == ">" || line == "❯" || strings.HasPrefix(line, "› ") ||
		strings.EqualFold(trimmed, "Codex") ||
		(strings.Contains(lower, "codex") && strings.ContainsAny(line, "▐▛▜▝▘▌█")) {
		return true
	}
	if strings.Contains(lower, "esc to interrupt") ||
		strings.Contains(lower, "ctrl+c") ||
		strings.Contains(lower, "ctrl+l is disabled") ||
		strings.Contains(lower, "ctrl+o") ||
		strings.Contains(lower, "pasted text") ||
		strings.Contains(lower, "conversation compacted") ||
		strings.Contains(lower, "openai codex") ||
		strings.Contains(lower, "chatgpt.com/codex") ||
		strings.Contains(lower, "app-landing-page=true") ||
		strings.Contains(lower, "shift+tab") ||
		strings.HasPrefix(lower, "tip:") ||
		isCodexToolStatusLine(trimmed) ||
		strings.HasPrefix(lower, "updated plan") ||
		strings.HasPrefix(lower, "searching the web") ||
		strings.HasPrefix(lower, "searched http") ||
		strings.HasPrefix(lower, "spawned ") ||
		strings.HasPrefix(lower, "waiting for ") ||
		strings.HasPrefix(lower, "finished waiting") ||
		strings.HasPrefix(lower, "working (") ||
		strings.Contains(lower, "directory:") ||
		strings.HasPrefix(lower, "field ") ||
		strings.HasPrefix(lower, "allow the ") && strings.Contains(lower, "mcp server") ||
		strings.HasPrefix(lower, "enter to submit") ||
		strings.Contains(lower, "run the tool and remember this choice") ||
		strings.Contains(lower, "calling api-bridge") ||
		strings.Contains(lower, "called api-bridge") ||
		isCodexQueuedInputLine(trimmed) ||
		strings.Contains(lower, "sent immediately") ||
		strings.Contains(lower, "edit last queued message") ||
		trimmed == "immediately)" ||
		strings.HasPrefix(trimmed, "↳ ") ||
		strings.Contains(lower, "tmux focus-events") ||
		// codex 0.142.3 injects a "• You have N usage limit resets available.
		// Run /usage to use one." system notice as its own event bullet. It is a
		// codex notification, not model output, so treat it as chrome to keep it
		// out of the extracted assistant answer.
		strings.Contains(lower, "usage limit resets available") ||
		strings.Contains(lower, "run /usage to use one") ||
		strings.Contains(lower, "codex") && strings.Contains(lower, "model") ||
		strings.HasPrefix(lower, "gpt-") && strings.Contains(lower, "·") ||
		isCodexTokenStatusLine(trimmed) ||
		isCodexActiveStatusLine(line) {
		return true
	}
	return isCodexBoxDrawingLine(line)
}

func isCodexTokenStatusLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	return strings.Contains(lower, "↑") && strings.Contains(lower, "tokens") ||
		strings.Contains(lower, "↓") && strings.Contains(lower, "tokens") ||
		strings.Contains(lower, "thinking with") && strings.Contains(lower, "tokens") ||
		strings.Contains(lower, "thought for") && strings.Contains(lower, "tokens")
}

func isCodexBulletOnlyLine(line string) bool {
	if line == "" {
		return false
	}
	for _, r := range line {
		if r == '•' || r == ' ' || r == '\t' {
			continue
		}
		return false
	}
	return true
}

func isCodexToolStatusLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "• "))
	lower := strings.ToLower(trimmed)
	if trimmed == "Called" || trimmed == "Calling" {
		return true
	}
	if strings.HasPrefix(lower, "called ") || strings.HasPrefix(lower, "calling ") {
		rest := strings.TrimSpace(trimmed[strings.Index(trimmed, " ")+1:])
		return strings.Contains(rest, "(") ||
			strings.Contains(rest, ".") ||
			strings.Contains(rest, "_") ||
			strings.Contains(strings.ToLower(rest), "api-bridge")
	}
	return false
}

var (
	codexToolJSONReplayPattern = regexp.MustCompile(`(^|[{\s,])"(stdout|stderr|exit_code|execution_time_ms)"\s*:`)
	codexShellListingPattern   = regexp.MustCompile(`^(total [0-9]+|[-dlcbsp][rwx-]{9}[@+]? +[0-9]+ +\S+)`)
	codexTruncatedLSLine       = regexp.MustCompile(`^[0-9]+ +[A-Za-z0-9_.-]+.*\.\.\.$`)
	codexTruncatedPathLine     = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]*\.\.\.$`)
	codexToolCommandPattern    = regexp.MustCompile(`(?i)(\$MCP_API_URL|https?://127\.0\.0\.1:[0-9]+/s/|/tools/(custom|mcp)/|authorization: bearer|content-type: application/json|curl -sS|requests\.post|json\.dumps|headers=\{|base='?https?://|python3 - <<|cat <<json|payload=|for m in |mkdir -p |touch [^ ]|ls -l|jq |\.read_mcp_resource\(|\.list_mcp_resources\(|\.list_mcp_resource_templates\(|\.get_api_spec\(|bridge\.get_api_spec\()`)
	codexBareNumberPattern     = regexp.MustCompile(`^[0-9]+$`)
	codexShortSentencePattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{1,24}\.$`)
)

func isCodexToolReplayLine(line string) bool {
	trimmed := strings.TrimSpace(strings.TrimPrefix(line, "• "))
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if codexToolJSONReplayPattern.MatchString(trimmed) ||
		codexToolCommandPattern.MatchString(trimmed) ||
		codexShellListingPattern.MatchString(trimmed) ||
		codexTruncatedLSLine.MatchString(trimmed) ||
		codexTruncatedPathLine.MatchString(trimmed) {
		return true
	}
	if strings.HasPrefix(trimmed, "# List supported and currently usable") {
		return true
	}
	if strings.HasPrefix(lower, "base: http://") ||
		strings.HasPrefix(lower, "auth: bearer") ||
		strings.HasPrefix(lower, "post /tools/") {
		return true
	}
	if strings.Contains(lower, "/api/llm-config/models/metadata") ||
		strings.Contains(lower, "provider: string (required)") ||
		strings.Contains(lower, "catalog the frontend uses") {
		return true
	}
	if strings.Contains(lower, "workspace-relative output_path") ||
		strings.Contains(lower, "relative output_path") ||
		strings.Contains(lower, "generated image files should be stored") ||
		strings.Contains(lower, "supports optional provider override") ||
		strings.Contains(lower, "aspect ratio, resolution") ||
		strings.Contains(lower, "aspect_ratio") ||
		strings.Contains(lower, "number_of_images") {
		return true
	}
	if strings.Contains(lower, "custom tool execution failed") ||
		strings.Contains(lower, "access denied") ||
		strings.Contains(lower, "operation not permitted") ||
		strings.Contains(lower, "permissionerror") ||
		strings.Contains(lower, "this step's allowed folders") ||
		strings.Contains(lower, "allowed folders") ||
		strings.Contains(lower, "cannot read from") ||
		strings.Contains(lower, "underlying check") ||
		strings.Contains(lower, "absolute host path") ||
		strings.Contains(lower, "allowed workspace roots") ||
		strings.Contains(lower, "writable folders") ||
		strings.Contains(lower, "did you mean:") {
		return true
	}
	if strings.Contains(lower, "unsupported image generation model") ||
		strings.Contains(lower, "supported image providers") ||
		strings.Contains(lower, "set_provider_auth(provider=") ||
		strings.Contains(lower, "output_path directory is outside") ||
		strings.Contains(lower, "prompt is required") {
		return true
	}
	if strings.HasPrefix(lower, "echo \"generating with") ||
		strings.HasPrefix(lower, "generating with model:") ||
		strings.HasPrefix(lower, "trying model=") {
		return true
	}
	if strings.HasSuffix(trimmed, `"})`) && (strings.Contains(trimmed, "&&") ||
		strings.Contains(trimmed, "{") ||
		strings.Contains(trimmed, "}") ||
		strings.Contains(trimmed, "PY")) {
		return true
	}
	if strings.Contains(lower, "indent=2))") ||
		strings.Contains(lower, "py\",\"timeout") ||
		strings.Contains(lower, `"timeout":`) && strings.Contains(lower, "})") {
		return true
	}
	if isCodexLikelyWrappedToolOutputLine(trimmed) {
		return true
	}
	return false
}

func isCodexToolReplayContinuationLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	if isCodexToolReplayLine(trimmed) || codexShellListingPattern.MatchString(trimmed) {
		return true
	}
	if strings.HasPrefix(trimmed, "...") ||
		strings.HasPrefix(trimmed, `", "stderr"`) ||
		strings.HasPrefix(trimmed, `"stderr"`) ||
		strings.HasPrefix(trimmed, `"exit_code"`) ||
		strings.HasPrefix(trimmed, `"execution_time_ms"`) ||
		isCodexJSONToolOutputLine(trimmed) ||
		isCodexShellScriptContinuationLine(trimmed) {
		return true
	}
	if strings.Contains(lower, "exit_code") && strings.Contains(lower, "execution_time_ms") {
		return true
	}
	return false
}

func isCodexContextualToolContinuationLine(currentLines []string, line, nextLine string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	if isCodexToolReplaySegment(currentLines) && !isCodexAssistantAnswerStartLine(trimmed) {
		return isCodexLikelyWrappedToolOutputLine(trimmed) ||
			isCodexToolReplayContinuationLine(trimmed) ||
			isCodexToolReplayLine(trimmed)
	}
	if codexBareNumberPattern.MatchString(trimmed) && codexShellListingPattern.MatchString(strings.TrimSpace(nextLine)) {
		return true
	}
	if isCodexAPISpecToolSegment(currentLines) {
		lower := strings.ToLower(trimmed)
		return strings.HasPrefix(trimmed, "# ") ||
			strings.Contains(lower, "supported and currently usable") ||
			strings.Contains(lower, "requires a workspace-") ||
			strings.Contains(lower, "relative output_path") ||
			strings.Contains(lower, "frontend uses") ||
			strings.Contains(lower, "provider: string") ||
			strings.Contains(lower, "provider/model") ||
			strings.Contains(lower, "capability")
	}
	if isCodexShellScriptSegment(currentLines) && isCodexShellScriptContinuationLine(trimmed) {
		return true
	}
	if isCodexJSONToolOutputSegment(currentLines) && isCodexJSONToolOutputLine(trimmed) {
		return true
	}
	if codexShortSentencePattern.MatchString(trimmed) && isCodexPlanMarkerLine(nextLine) {
		return true
	}
	return false
}

func isCodexToolReplaySegment(lines []string) bool {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isCodexToolReplayLine(trimmed) ||
			isCodexToolReplayContinuationLine(trimmed) ||
			codexShellListingPattern.MatchString(trimmed) {
			return true
		}
	}
	return false
}

func isCodexAssistantAnswerStartLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	return strings.HasPrefix(lower, "here's ") ||
		strings.HasPrefix(lower, "here’s ") ||
		strings.HasPrefix(lower, "here are ") ||
		strings.HasPrefix(lower, "here is ") ||
		strings.HasPrefix(lower, "the current ") ||
		strings.HasPrefix(lower, "i found ") ||
		strings.HasPrefix(lower, "i can ") ||
		strings.HasPrefix(lower, "done") ||
		strings.HasPrefix(lower, "all set")
}

func isCodexAssistantAnswerContinuationLine(currentLines []string, line string) bool {
	if len(currentLines) == 0 {
		return false
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if !codexAssistantLinesIntroducePath(currentLines) {
		return false
	}
	return isCodexAssistantPathLine(trimmed) ||
		strings.HasPrefix(lower, "(equivalent relative path") ||
		strings.HasPrefix(lower, "equivalent relative path") ||
		strings.HasPrefix(lower, "relative path") ||
		strings.Contains(lower, "workspace root:")
}

// isCodexPromptWrapContinuationLine reports whether line is a wrapped
// continuation of a "›" user prompt that the TUI split across terminal lines.
// Codex renders only the FIRST line of a long prompt with the "›" marker; the
// continuation lines are indented with no marker, so classifyCodexLine falls
// through to its default (assistant_text) case and the wrapped tail leaks into
// the extracted answer. We absorb such a line into the prompt's chrome segment,
// but only when the chrome segment we are extending is itself a "›" prompt and
// the candidate line would otherwise be plain assistant text — keeping this
// narrow so real assistant replies are never swallowed.
func isCodexPromptWrapContinuationLine(currentLines []string, line string) bool {
	if len(currentLines) == 0 {
		return false
	}
	// The chrome segment must be a "›" user-prompt block. codex 0.142.3 renders
	// a multi-line user prompt (or a terminal-wrapped one) as the "›" marker line
	// FOLLOWED by SEVERAL indented, marker-less continuation lines. The previous
	// implementation only matched when the marker was the IMMEDIATELY preceding
	// line, so it absorbed just the first continuation; the remaining echoed
	// prompt lines leaked into an assistant segment and corrupted final-answer
	// extraction (e.g. a 4-line prompt echo leaving "first"/"second" lines that
	// merged into the real answer). Match whenever this chrome segment already
	// contains a prompt-marker line so EVERY continuation of a multi-line prompt
	// echo is absorbed. Absorption naturally stops at the answer because codex
	// renders the answer's first line with a "•" bullet (not wrap-eligible), which
	// flushes the chrome segment.
	if !codexChromeSegmentHasPromptMarker(currentLines) {
		return false
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	// Only absorb lines that would otherwise be classified as assistant text;
	// anything with its own structural classification (tool/plan/chrome/etc.)
	// must keep it so the existing handling applies.
	return classifyCodexLine(trimmed) == codexSegmentAssistant
}

// codexChromeSegmentHasPromptMarker reports whether a chrome segment's lines
// include a rendered "›" user-prompt marker line, identifying the segment as a
// prompt-echo block whose marker-less indented continuations should be absorbed
// rather than leaked into the extracted assistant answer.
func codexChromeSegmentHasPromptMarker(lines []string) bool {
	for _, l := range lines {
		if isCodexPromptMarkerLine(l) {
			return true
		}
	}
	return false
}

// isCodexPromptMarkerLine reports whether line is a rendered "›" user-prompt
// line (the marker plus the first wrapped chunk of the prompt text).
func isCodexPromptMarkerLine(line string) bool {
	trimmed := strings.TrimSpace(stripCodexANSI(line))
	return trimmed == "›" || trimmed == ">" || trimmed == "❯" ||
		strings.HasPrefix(trimmed, "› ") ||
		strings.HasPrefix(trimmed, "❯ ")
}

func codexAssistantAnswerIntroducesPath(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	return strings.Contains(lower, "saved at") ||
		strings.Contains(lower, "stored at") ||
		strings.Contains(lower, "written to") ||
		strings.Contains(lower, "generated at") ||
		strings.Contains(lower, "files/folders in") ||
		strings.Contains(lower, "analyzed this image") ||
		strings.Contains(lower, "analysed this image") ||
		strings.Contains(lower, "read this image") ||
		strings.Contains(lower, "selected image") ||
		strings.Contains(lower, "image:")
}

func codexAssistantLinesIntroducePath(lines []string) bool {
	for _, line := range lines {
		if codexAssistantAnswerIntroducesPath(line) {
			return true
		}
	}
	return false
}

func isCodexAssistantAnswerPathSegment(lines []string) bool {
	if len(lines) == 0 || !codexAssistantLinesIntroducePath(lines) {
		return false
	}
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if isCodexAssistantPathLine(trimmed) ||
			strings.HasPrefix(lower, "(equivalent relative path") ||
			strings.HasPrefix(lower, "equivalent relative path") ||
			strings.HasPrefix(lower, "relative path") ||
			strings.Contains(lower, "workspace root:") {
			return true
		}
	}
	return false
}

func isCodexAssistantPathLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(trimmed, "/") ||
		strings.HasPrefix(trimmed, "~/") ||
		strings.HasPrefix(trimmed, "_users/") ||
		strings.HasPrefix(trimmed, "chats/") ||
		strings.Contains(lower, "/workspace-docs/") ||
		strings.Contains(lower, "_users/default/chats/")
}

var codexWrappedLSDatePattern = regexp.MustCompile(`\b[0-9]{1,2} [A-Za-z]{3} ([0-9]{2}:[0-9]{2}|[0-9]{4})\b`)

func isCodexLikelyWrappedToolOutputLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "./") ||
		strings.HasPrefix(trimmed, "../") ||
		codexTruncatedPathLine.MatchString(trimmed) ||
		strings.HasPrefix(trimmed, "&& ") ||
		strings.Contains(trimmed, "&& find ") ||
		strings.Contains(trimmed, "find . -maxdepth") ||
		strings.Contains(trimmed, "2>/dev/null") ||
		strings.Contains(trimmed, "| sort") ||
		strings.Contains(trimmed, "| head") ||
		strings.Contains(trimmed, "ls -la") {
		return true
	}
	if strings.Contains(lower, "default/chat_history/") ||
		strings.Contains(lower, "default/chats/") ||
		strings.Contains(lower, "cannot read from") ||
		strings.Contains(lower, "allowed folders") ||
		strings.Contains(lower, "writable folders") ||
		strings.Contains(lower, "underlying check") ||
		strings.Contains(lower, "operation not permitted") {
		return true
	}
	if strings.Contains(lower, " staff ") && codexWrappedLSDatePattern.MatchString(trimmed) {
		return true
	}
	return false
}

func isCodexAPISpecToolSegment(lines []string) bool {
	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		if strings.Contains(lower, "get_api_spec(") ||
			strings.HasPrefix(lower, "base: http://") ||
			strings.HasPrefix(lower, "post /tools/") {
			return true
		}
	}
	return false
}

func isCodexPlanMarkerLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "✔") ||
		strings.HasPrefix(trimmed, "✓") ||
		strings.HasPrefix(trimmed, "□") ||
		strings.HasPrefix(trimmed, "☐")
}

func isCodexShellScriptSegment(lines []string) bool {
	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		if strings.Contains(lower, "curl -ss") ||
			strings.Contains(lower, "payload=") ||
			strings.Contains(lower, "cat <<json") ||
			strings.Contains(lower, "for m in ") {
			return true
		}
	}
	return false
}

func isCodexShellScriptContinuationLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "payload=") ||
		strings.HasPrefix(lower, "echo ") ||
		strings.HasPrefix(lower, "done") ||
		strings.HasPrefix(lower, "json") ||
		strings.HasPrefix(lower, "try:") ||
		strings.HasPrefix(lower, "except ") ||
		strings.HasPrefix(lower, "print(") ||
		strings.HasPrefix(lower, "r=requests.post") ||
		strings.HasPrefix(lower, "for p in ") ||
		strings.HasPrefix(lower, "for m in ") ||
		strings.HasPrefix(lower, "-h ") ||
		strings.HasPrefix(lower, "-d ") ||
		strings.HasPrefix(trimmed, "{") ||
		strings.HasPrefix(trimmed, "}") ||
		strings.HasPrefix(trimmed, `"provider"`) ||
		strings.HasPrefix(trimmed, `"model_id"`) ||
		strings.HasPrefix(trimmed, `"prompt"`) ||
		strings.HasPrefix(trimmed, `"aspect_ratio"`) ||
		strings.HasPrefix(trimmed, `"resolution"`) ||
		strings.HasPrefix(trimmed, `"number_of_images"`) ||
		strings.HasPrefix(trimmed, `"output_path"`)
}

func isCodexJSONToolOutputSegment(lines []string) bool {
	for _, line := range lines {
		if codexToolJSONReplayPattern.MatchString(line) ||
			strings.Contains(line, "list_provider_models") ||
			strings.Contains(line, "/tools/custom/") {
			return true
		}
	}
	return false
}

func isCodexJSONToolOutputLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(trimmed, "{") ||
		strings.HasPrefix(trimmed, "}") ||
		strings.HasPrefix(trimmed, "[") ||
		strings.HasPrefix(trimmed, "]") ||
		strings.HasPrefix(trimmed, `"count"`) ||
		strings.HasPrefix(trimmed, `"models"`) ||
		strings.HasPrefix(trimmed, `"model_id"`) ||
		strings.HasPrefix(trimmed, `"context_window"`) ||
		strings.HasPrefix(trimmed, `"input_cost_per_1m"`) ||
		strings.HasPrefix(trimmed, `"output_cost_per_1m"`) ||
		strings.HasPrefix(trimmed, `"reasoning_cost"`) ||
		strings.HasPrefix(trimmed, `"supports"`) ||
		strings.Contains(lower, `\"model_id\"`) ||
		strings.Contains(lower, `\"context_window\"`) ||
		strings.Contains(lower, `\"input_cost_per_1m\"`) ||
		strings.Contains(lower, `\"output_cost_per_1m\"`)
}

func isCodexLikelyToolReplayAssistantSegment(lines []string, prevKind, nextKind codexSegmentKind) bool {
	if len(lines) == 0 {
		return false
	}
	if isCodexAssistantAnswerPathSegment(lines) {
		return false
	}
	nearTool := prevKind == codexSegmentToolStatus || nextKind == codexSegmentToolStatus ||
		prevKind == codexSegmentPlanStatus || nextKind == codexSegmentPlanStatus ||
		prevKind == codexSegmentErrorStatus || nextKind == codexSegmentErrorStatus
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if isCodexToolReplayLine(trimmed) {
			return true
		}
		if strings.EqualFold(trimmed, "environment.") ||
			strings.HasPrefix(trimmed, "# List supported and currently usable") {
			return true
		}
		if nearTool && (isCodexJSONToolOutputLine(trimmed) ||
			strings.Contains(lower, "model_id") ||
			strings.Contains(lower, "output_path") ||
			strings.Contains(lower, "aspect_ratio") ||
			strings.Contains(lower, "workspace-docs") ||
			strings.Contains(lower, "mcp_api_token") ||
			strings.Contains(lower, "access denied") ||
			strings.Contains(lower, "writable folders") ||
			strings.Contains(trimmed, `\"`) ||
			codexBareNumberPattern.MatchString(trimmed) ||
			codexTruncatedLSLine.MatchString(trimmed) ||
			codexTruncatedPathLine.MatchString(trimmed)) {
			return true
		}
	}
	return false
}

func hasCodexActivity(captured string) bool {
	if hasCodexQueuedInput(captured) {
		return true
	}
	lines := strings.Split(stripCodexANSI(captured), "\n")
	seenNonEmpty := 0
	seenReadyPrompt := false
	seenPromptWithInput := false
	for i := len(lines) - 1; i >= 0 && seenNonEmpty < codexActivityScanNonEmptyLines; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		seenNonEmpty++
		lower := strings.ToLower(line)
		if line == "›" || line == ">" || line == "❯" || strings.HasPrefix(line, "› ") || strings.HasSuffix(line, "›") {
			seenReadyPrompt = true
			if isCodexPromptWithInputLine(line) {
				seenPromptWithInput = true
			}
			continue
		}
		if seenReadyPrompt && isCodexCompletedStatusLine(line) {
			return false
		}
		if strings.Contains(lower, "esc to interrupt") || isCodexActiveStatusLine(line) {
			if seenPromptWithInput {
				continue
			}
			return true
		}
	}
	return false
}

func hasCodexReadyPrompt(captured string) bool {
	if !hasCodexPromptCandidate(captured) {
		return false
	}
	return !hasCodexActivity(captured)
}

// PaneReadyForInput reports whether Codex CLI's current tmux screen is at its
// empty, idle composer. Host runtimes call it only at a stream-driven quiet
// boundary; retained scrollback activity is rejected by the canonical Codex
// prompt and activity checks above.
func PaneReadyForInput(captured string) bool {
	return hasCodexReadyPrompt(captured)
}

func hasCodexExplicitCompletedMarker(captured string) bool {
	for _, line := range strings.Split(stripCodexANSI(captured), "\n") {
		if strings.EqualFold(strings.TrimSpace(line), "STATUS: COMPLETED") {
			return true
		}
	}
	return false
}

func hasCodexPromptCandidate(captured string) bool {
	if hasCodexTrustPrompt(captured) || hasCodexHookTrustReviewPrompt(captured) || hasCodexRateLimitReminderModal(captured) || hasCodexAdditionalSafetyChecksModal(captured) || hasCodexQueuedInput(captured) {
		return false
	}
	if codexPaneIsResuming(captured) {
		return false
	}
	lines := strings.Split(stripCodexANSI(captured), "\n")
	seenNonEmpty := 0
	for i := len(lines) - 1; i >= 0 && seenNonEmpty < 12; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		seenNonEmpty++
		if line == "›" || line == ">" || line == "❯" || strings.HasPrefix(line, "› ") || strings.HasSuffix(line, "›") {
			return true
		}
	}
	return false
}

func hasCodexQueuedInput(captured string) bool {
	lines := strings.Split(stripCodexANSI(captured), "\n")
	seenNonEmpty := 0
	seenLaterPromptWithInput := false
	seenLaterCompletion := false
	for i := len(lines) - 1; i >= 0 && seenNonEmpty < 80; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		seenNonEmpty++
		// A later "›" prompt — whether it carries typed input OR shows the
		// rotating ghost placeholder of an EMPTY composer — means the composer
		// returned to its interactive/ready state below this banner, so an
		// earlier "messages queued after next tool call" banner is historical.
		// (During an active queue the live "• Working" line keeps the pane
		// active via hasCodexActivity, so not flagging queued here is safe.)
		if isCodexPromptWithInputLine(line) || isCodexGhostPlaceholderPromptLine(line) {
			seenLaterPromptWithInput = true
			continue
		}
		if isCodexCompletedStatusLine(line) {
			seenLaterCompletion = true
			continue
		}
		if isCodexQueuedInputLine(line) {
			return !seenLaterPromptWithInput && !seenLaterCompletion
		}
	}
	return false
}

func isCodexPromptWithInputLine(line string) bool {
	trimmed := strings.TrimSpace(stripCodexANSI(line))
	if len(trimmed) <= len("› ") {
		return false
	}
	for _, prefix := range []string{"› ", "❯ ", "> "} {
		if strings.HasPrefix(trimmed, prefix) {
			body := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			// codex 0.142.3+ renders a rotating dim "ghost" example prompt
			// inside the EMPTY composer (e.g. "› Implement {feature}").
			// That is a placeholder hint, NOT user-typed/queued input, so it
			// must not be mistaken for an active prompt-with-input — doing so
			// masks the "• Working"/"• Calling" activity lines that sit ABOVE
			// the composer in the new TUI layout.
			if isCodexGhostPlaceholderText(body) {
				return false
			}
			return body != ""
		}
	}
	return false
}

// codexGhostPlaceholders is the set of rotating example prompts codex
// 0.142.3 shows as dim ghost text inside the empty composer. Sourced from
// the codex binary's string table (the composer placeholder rotation).
// When one of these appears after the "›" marker the composer is EMPTY.
var codexGhostPlaceholders = map[string]struct{}{
	"explain this codebase":                               {},
	"summarize recent commits":                            {},
	"implement {feature}":                                 {},
	"find and fix a bug in @filename":                     {},
	"write tests for @filename":                           {},
	"improve documentation in @filename":                  {},
	"run /review on my current changes":                   {},
	"use /skills to list available skills":                {},
	"check recently modified functions for compatibility": {},
	"how many files have been modified?":                  {},
	"will this algorithm scale well?":                     {},
	// codex 0.153: a single fixed placeholder replaced the rotation. Not
	// knowing it made the empty composer read as typed input, which hid the
	// "Working (… esc to interrupt)" line above it: every submission then
	// timed out as "input remained unconfirmed" while Codex was in fact
	// already answering.
	"ask codex to do anything": {},
}

// isCodexGhostPlaceholderText reports whether body (the text after the "›"
// composer marker) is one of codex's rotating empty-composer ghost
// placeholders rather than real user input.
func isCodexGhostPlaceholderText(body string) bool {
	_, ok := codexGhostPlaceholders[strings.ToLower(strings.TrimSpace(body))]
	return ok
}

// isCodexGhostPlaceholderPromptLine reports whether line is a "›" composer
// prompt whose body is a rotating ghost placeholder — i.e. an EMPTY composer.
func isCodexGhostPlaceholderPromptLine(line string) bool {
	trimmed := strings.TrimSpace(stripCodexANSI(line))
	for _, prefix := range []string{"› ", "❯ ", "> "} {
		if strings.HasPrefix(trimmed, prefix) {
			return isCodexGhostPlaceholderText(strings.TrimPrefix(trimmed, prefix))
		}
	}
	return false
}

// codexPromptCandidateStabilitySnapshot removes the rotating empty-composer
// hint from readiness stability checks. Codex changes this ghost text while
// idle; comparing the raw pane made a ready session wait an arbitrary extra
// rotation before it happened to remain byte-identical long enough.
func codexPromptCandidateStabilitySnapshot(captured string) string {
	lines := strings.Split(stripCodexANSI(captured), "\n")
	for i, line := range lines {
		if isCodexGhostPlaceholderPromptLine(line) {
			lines[i] = "› <empty-composer>"
		}
	}
	// capture-pane normally appends a newline (and can append blank terminal
	// rows), while synthetic baselines and archived snapshots may not. Those
	// transport-only suffixes are not a real pane transition.
	return strings.TrimRight(strings.Join(lines, "\n"), " \t\r\n")
}

func hasCodexTrustPrompt(captured string) bool {
	lines := strings.Split(stripCodexANSI(captured), "\n")
	lastTrustQuestion := -1
	lastSelectedTrustOption := -1
	lastLaterInputPrompt := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "do you trust the contents of this directory") {
			lastTrustQuestion = i
			continue
		}
		if strings.HasPrefix(trimmed, "› ") {
			option := strings.TrimSpace(strings.TrimPrefix(trimmed, "› "))
			optionLower := strings.ToLower(option)
			if strings.HasPrefix(optionLower, "1. yes, continue") || strings.HasPrefix(optionLower, "2. no, quit") {
				lastSelectedTrustOption = i
				continue
			}
			lastLaterInputPrompt = i
		}
	}
	return lastTrustQuestion >= 0 &&
		lastSelectedTrustOption > lastTrustQuestion &&
		lastLaterInputPrompt < lastTrustQuestion
}

func isCodexTrustPromptLine(line string) bool {
	trimmed := strings.TrimSpace(strings.TrimPrefix(line, "› "))
	lower := strings.ToLower(trimmed)
	return strings.Contains(lower, "do you trust the contents of this directory") ||
		strings.Contains(lower, "working with untrusted contents") ||
		strings.Contains(lower, "trusting the directory allows") ||
		strings.HasPrefix(lower, "1. yes, continue") ||
		strings.HasPrefix(lower, "2. no, quit") ||
		strings.Contains(lower, "press enter to continue")
}

func hasCodexRateLimitReminderModal(captured string) bool {
	// capture-pane includes scrollback, so a reminder answered on an earlier
	// turn must not make the current turn look blocked. The live menu is always
	// rendered in the visible tail of the pane.
	lower := strings.ToLower(lastNCodexLines(stripCodexANSI(captured), 40))
	return strings.Contains(lower, "approaching rate limits") &&
		strings.Contains(lower, "switch to ") &&
		strings.Contains(lower, "keep current model") &&
		strings.Contains(lower, "press enter to confirm")
}

// hasCodexAdditionalSafetyChecksModal detects Codex's runtime safety-review
// choice. Automated turns must preserve the configured model, so this modal is
// always answered with option 2 ("Keep waiting") rather than retrying with a
// faster model.
func hasCodexAdditionalSafetyChecksModal(captured string) bool {
	lower := strings.ToLower(lastNCodexLines(stripCodexANSI(captured), 40))
	return strings.Contains(lower, "additional safety checks") &&
		strings.Contains(lower, "retry with a faster model") &&
		strings.Contains(lower, "keep waiting") &&
		strings.Contains(lower, "learn more")
}

func isCodexRateLimitReminderLine(line string) bool {
	trimmed := strings.TrimSpace(strings.TrimPrefix(line, "› "))
	lower := strings.ToLower(trimmed)
	return strings.Contains(lower, "heads up") && strings.Contains(lower, "limit left") ||
		strings.Contains(lower, "approaching rate limits") ||
		strings.Contains(lower, "switch to ") && strings.Contains(lower, "credit usage") ||
		strings.HasPrefix(lower, "1. switch to ") ||
		strings.HasPrefix(lower, "2. keep current model") ||
		strings.HasPrefix(lower, "3. keep current model") ||
		strings.Contains(lower, "hide future rate limit reminders") ||
		strings.Contains(lower, "press enter to confirm or esc to go back")
}

func isCodexAdditionalSafetyChecksLine(line string) bool {
	trimmed := strings.TrimSpace(strings.TrimPrefix(line, "› "))
	lower := strings.ToLower(trimmed)
	return strings.Contains(lower, "additional safety checks") ||
		strings.Contains(lower, "request requires additional safety checks") ||
		strings.Contains(lower, "retry with a faster model") ||
		strings.HasPrefix(lower, "1. retry with a faster model") ||
		strings.HasPrefix(lower, "2. keep waiting") ||
		strings.HasPrefix(lower, "3. learn more") ||
		strings.Contains(lower, "hang tight or retry with a faster") ||
		strings.Contains(lower, "less capable of handling complex requests")
}

func dismissCodexTrustPrompt(ctx context.Context, sessionName, captured string) error {
	selected := selectedCodexTrustPromptOption(captured)
	keys := make([]string, 0, 2)
	if selected == 2 {
		keys = append(keys, "Up")
	}
	keys = append(keys, "C-m")
	args := []string{"send-keys", "-t", sessionName}
	args = append(args, keys...)
	return runCodexCommand(ctx, nil, "tmux", args...)
}

// hasCodexHookTrustReviewPrompt detects EITHER of codex's two hook
// trust prompts. Both are distinct from the workspace-trust prompt
// (hasCodexTrustPrompt) and the rate-limit reminder. Both fire when
// the workspace has a .codex/hooks.json that codex hasn't seen this
// invocation, even with --dangerously-bypass-hook-trust on the
// command line (the flag enables hook EXECUTION but does not
// auto-dismiss the visual prompts on v0.131.0).
//
// Form 1 — initial menu (this is what fresh sessions see):
//
//	Hooks need review
//	1 hook is new or changed.
//	Hooks can run outside the sandbox after you trust them.
//
//	› 1. Review hooks
//	  2. Trust all and continue
//	  3. Continue without trusting (hooks won't run)
//
//	Press enter to confirm or esc to go back
//
// Form 2 — expanded review table (reached via "1. Review hooks"):
//
//	Hooks
//	⚠ N hook(s) need review before [they|it] can run.
//	Event             Installed   Active   Review   Description
//	PreToolUse        1           0        1        ...
//	Press t to trust all; enter to review hooks; esc to close
//
// We match on distinctive bottom-line directives that are stable
// across hook-count variations. Either form returns true.
func hasCodexHookTrustReviewPrompt(captured string) bool {
	// captureCodexPane includes a deep scrollback window. Anchor text from a dismissed prompt
	// stays in scrollback indefinitely, so a naïve substring match
	// over the full buffer reports the prompt as "still showing" long
	// after we dismissed it — which then makes waitForCodexPrompt
	// loop forever in the dismissed branch and never reach
	// hasCodexReadyPrompt. Only inspect the tail of the buffer so
	// detection reflects what's currently RENDERED, not what was
	// ever shown.
	stripped := stripCodexANSI(captured)
	tail := lastNCodexLines(stripped, 40)
	// Form 1 — initial menu. We saw in live capture that tmux's
	// capture-pane sometimes omits intermediate lines from this menu
	// (e.g. the "Trust all and continue" row may be missing while the
	// header + option-1 row are present). Match on the distinctive
	// header AND the option-1 row — both are stable across captures
	// AND specific enough to never false-positive against codex's
	// other prompts.
	if strings.Contains(tail, "Hooks need review") &&
		strings.Contains(tail, "1. Review hooks") {
		return true
	}
	// Form 2 — expanded review table. The "Press t to trust all" line
	// is the bottom-line directive and is the most distinctive anchor.
	if strings.Contains(tail, "Press t to trust all") &&
		strings.Contains(tail, "esc to close") {
		return true
	}
	return false
}

// lastNCodexLines returns the last n lines of s, joined with newlines.
// Used to scope substring matches to the currently-rendered portion of
// a tmux capture-pane output (which includes scrollback we don't want
// to false-positive on).
func lastNCodexLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// dismissCodexHookTrustReviewPrompt sends the right key sequence for
// whichever hook trust prompt is currently showing. The two forms have
// disjoint key contracts so we must branch — sending "Down/Enter" to
// the expanded review form leaves stray input, and sending "t" to the
// menu form is interpreted as a non-existent option-letter and stays
// stuck. We detect by looking for each form's unique anchor text.
//
// Menu form ("Hooks need review" + numbered options): send Down
// (move from default "1. Review hooks" to "2. Trust all and
// continue") then Enter (confirm). Trust all + the
// --dangerously-bypass-hook-trust flag give equivalent runtime trust;
// either way the hooks fire on tool calls.
//
// Expanded review form (full hooks table + "Press t to trust all"):
// send "t" (trust all) then Escape (close the resulting "Press enter
// to view hooks; esc to close" follow-up modal).
//
// Short sleeps between keystrokes prevent tmux from coalescing the
// pair so codex sees discrete presses.
func dismissCodexHookTrustReviewPrompt(ctx context.Context, sessionName string) error {
	captured, err := captureCodexPane(ctx, sessionName)
	if err != nil {
		// Fall back to menu-form dismissal — it's the form fresh sessions
		// hit, so it's the safer default when capture races the prompt.
		captured = ""
	}
	stripped := stripCodexANSI(captured)

	if strings.Contains(stripped, "Press t to trust all") &&
		strings.Contains(stripped, "esc to close") {
		// Expanded review form: t → Escape.
		if err := runCodexCommand(ctx, nil, "tmux", "send-keys", "-t", sessionName, "t"); err != nil {
			return err
		}
		time.Sleep(150 * time.Millisecond)
		return runCodexCommand(ctx, nil, "tmux", "send-keys", "-t", sessionName, "Escape")
	}

	// Default: menu form. Down → Enter selects "Trust all and
	// continue" (codex registers the choice and shows "Trusting
	// hooks..."). Codex does NOT auto-close the modal after the
	// trust applies — the menu re-renders without the selected
	// option and the user is expected to dismiss with Escape. We
	// pause briefly to let the trust action settle, then send
	// Escape to fully close the modal so waitForCodexPrompt's next
	// poll sees the normal ready prompt.
	if err := runCodexCommand(ctx, nil, "tmux", "send-keys", "-t", sessionName, "Down"); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)
	if err := runCodexCommand(ctx, nil, "tmux", "send-keys", "-t", sessionName, "Enter"); err != nil {
		return err
	}
	time.Sleep(400 * time.Millisecond)
	return runCodexCommand(ctx, nil, "tmux", "send-keys", "-t", sessionName, "Escape")
}

func selectedCodexTrustPromptOption(captured string) int {
	lines := strings.Split(stripCodexANSI(captured), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "› ") {
			continue
		}
		option := strings.TrimSpace(strings.TrimPrefix(trimmed, "› "))
		switch {
		case strings.HasPrefix(option, "1."):
			return 1
		case strings.HasPrefix(option, "2."):
			return 2
		}
	}
	return 0
}

func dismissCodexRateLimitReminder(ctx context.Context, sessionName, captured string) error {
	selected := selectedCodexRateLimitReminderOption(captured)
	if selected < 1 || selected > 3 {
		selected = 1
	}
	// Keep the configured model, but leave the user-visible reminder enabled.
	// Choosing option 3 would mutate the user's Codex preference to hide future
	// capacity warnings; a background workflow must not make that durable choice.
	keys := make([]string, 0, 2)
	for selected < 2 {
		keys = append(keys, "Down")
		selected++
	}
	keys = append(keys, "C-m")
	args := []string{"send-keys", "-t", sessionName}
	args = append(args, keys...)
	return runCodexCommand(ctx, nil, "tmux", args...)
}

// dismissCodexRateLimitReminderSerialized coordinates the runtime modal with
// all other writers to this tmux pane. It re-checks the modal after acquiring
// the broker so stale Down/Enter keys cannot be injected into a newer prompt.
func dismissCodexRateLimitReminderSerialized(ctx context.Context, sessionName string) (bool, error) {
	handled := false
	_, err := tmuxinput.Default.Do(ctx, tmuxinput.Request{
		SessionID: sessionName,
		Source:    "codex-rate-limit-reminder",
		Priority:  tmuxinput.PriorityInterrupt,
	}, func(ctx context.Context) error {
		captured, err := captureCodexPane(ctx, sessionName)
		if err != nil {
			return err
		}
		if !hasCodexRateLimitReminderModal(captured) {
			return nil
		}
		if err := dismissCodexRateLimitReminder(ctx, sessionName, captured); err != nil {
			return err
		}
		handled = true
		return nil
	})
	return handled, err
}

func selectedCodexRateLimitReminderOption(captured string) int {
	lines := strings.Split(stripCodexANSI(captured), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "› ") {
			continue
		}
		option := strings.TrimSpace(strings.TrimPrefix(trimmed, "› "))
		switch {
		case strings.HasPrefix(option, "1."):
			return 1
		case strings.HasPrefix(option, "2."):
			return 2
		case strings.HasPrefix(option, "3."):
			return 3
		}
	}
	return 0
}

func dismissCodexAdditionalSafetyChecks(ctx context.Context, sessionName, captured string) error {
	selected := selectedCodexAdditionalSafetyChecksOption(captured)
	if selected < 1 || selected > 3 {
		selected = 1
	}
	keys := make([]string, 0, 2)
	switch selected {
	case 1:
		keys = append(keys, "Down")
	case 3:
		keys = append(keys, "Up")
	}
	// Option 2 is an invariant: keep waiting on the configured model. Never
	// accept Codex's default option 1, which silently changes the model.
	keys = append(keys, "C-m")
	args := []string{"send-keys", "-t", sessionName}
	args = append(args, keys...)
	return runCodexCommand(ctx, nil, "tmux", args...)
}

// dismissCodexAdditionalSafetyChecksSerialized re-checks the modal while
// holding the per-pane input broker, preventing stale navigation keys from
// landing in Codex after the modal has already disappeared.
func dismissCodexAdditionalSafetyChecksSerialized(ctx context.Context, sessionName string) (bool, error) {
	handled := false
	_, err := tmuxinput.Default.Do(ctx, tmuxinput.Request{
		SessionID: sessionName,
		Source:    "codex-additional-safety-checks",
		Priority:  tmuxinput.PriorityInterrupt,
	}, func(ctx context.Context) error {
		captured, err := captureCodexPane(ctx, sessionName)
		if err != nil {
			return err
		}
		if !hasCodexAdditionalSafetyChecksModal(captured) {
			return nil
		}
		if err := dismissCodexAdditionalSafetyChecks(ctx, sessionName, captured); err != nil {
			return err
		}
		handled = true
		return nil
	})
	return handled, err
}

func selectedCodexAdditionalSafetyChecksOption(captured string) int {
	lines := strings.Split(stripCodexANSI(captured), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trimmed, "› ") {
			continue
		}
		option := strings.TrimSpace(strings.TrimPrefix(trimmed, "› "))
		switch {
		case strings.HasPrefix(option, "1."):
			return 1
		case strings.HasPrefix(option, "2."):
			return 2
		case strings.HasPrefix(option, "3."):
			return 3
		default:
			return 0
		}
	}
	return 0
}

var (
	codexStatusLineStreamMu sync.Mutex
	codexStatusLineStreamed = make(map[string]string) // sessionName -> last streamed token signature
)

// codexWorkingDirForSession maps a tmux session name to its working dir via the
// persistent registry, so the statusline reader can locate the right rollout.
func codexWorkingDirForSession(sessionName string) string {
	_, sess, ok := codexPersistentRegistry.Find(func(sess *codexInteractiveSession) bool {
		return sess != nil && sess.tmuxSessionName == sessionName
	})
	if ok && sess != nil {
		return sess.workingDir
	}
	return ""
}

func codexIntFromRef(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

// streamCodexStatusLine emits a generic StatusLine chunk built from codex's local
// rollout JSONL (~/.codex/sessions/.../rollout-*.jsonl). Codex in tmux mode has
// no statusline command hook (unlike claude) and no stdout JSON, so the
// rollout's token_count snapshot is the structured source — see
// readCodexTranscriptUsage. Token counts refresh per completed turn, not
// sub-second; the dedup below suppresses no-op re-emits.
func streamCodexStatusLine(ctx context.Context, sessionName string, streamChan chan<- llmtypes.StreamChunk) bool {
	if streamChan == nil {
		return false
	}
	status := buildCodexStatusLine(sessionName, codexWorkingDirForSession(sessionName))
	if status == nil {
		return false
	}

	sig := fmt.Sprintf("%d:%d:%d:%s", status.InputTokens, status.OutputTokens, status.CacheReadInputTokens, status.Model)
	codexStatusLineStreamMu.Lock()
	if codexStatusLineStreamed[sessionName] == sig {
		codexStatusLineStreamMu.Unlock()
		return false
	}
	codexStatusLineStreamed[sessionName] = sig
	codexStatusLineStreamMu.Unlock()

	select {
	case streamChan <- llmtypes.StreamChunk{
		Type:       llmtypes.StreamChunkTypeStatusLine,
		StatusLine: status,
	}:
		return true
	default:
		return false
	}
}

// buildCodexStatusLine reads the owning terminal's bound rollout and assembles a
// generic StatusLine (codex-cli, real model, input/output/cached tokens). Legacy
// unregistered callers resolve by workingDir. Returns nil without current usage.
func buildCodexStatusLine(tmuxSession, workingDir string) *llmtypes.StatusLine {
	// Require the session's working dir: readCodexTranscriptUsage with an empty
	// dir would match ANY freshest rollout under ~/.codex/sessions (a different
	// project/session, or none belonging to us), so without it we'd attribute
	// unrelated telemetry — and could starve the terminal chunk by emitting
	// spuriously into the stream channel.
	if strings.TrimSpace(workingDir) == "" {
		return nil
	}
	_, session, found := codexPersistentRegistry.Find(func(session *codexInteractiveSession) bool {
		return session != nil && session.tmuxSessionName == tmuxSession
	})
	var gi *llmtypes.GenerationInfo
	var model string
	if found {
		// A live terminal's status belongs to its exact conversation. Until
		// submission binds that rollout there is no usage to publish; scanning
		// by cwd could attribute a parallel session's usage and, in a cold
		// workspace, decode the account's entire history before rendering.
		path, _ := codexRolloutIdentity(session)
		if path == "" {
			return nil
		}
		gi, model, _, _ = readCodexTranscriptUsageFile(path, time.Time{})
	} else {
		gi, model, _ = readCodexTranscriptUsage(time.Time{}, workingDir)
	}
	if gi == nil {
		return nil
	}
	input := codexIntFromRef(gi.PromptTokens)
	output := codexIntFromRef(gi.CompletionTokens)
	cached := codexIntFromRef(gi.CachedContentTokens)
	if input == 0 && output == 0 && cached == 0 {
		return nil
	}
	meta := map[string]interface{}{}
	if strings.TrimSpace(tmuxSession) != "" {
		meta["tmux_session"] = tmuxSession
	}
	status := &llmtypes.StatusLine{
		Provider:             "codex-cli",
		Model:                model,
		InputTokens:          input,
		OutputTokens:         output,
		CacheReadInputTokens: cached,
		TotalInputTokens:     input + cached,
		TotalOutputTokens:    output,
		Metadata:             meta,
	}
	// Expose plan rate-limit usage (parsed from the rollout's token_count
	// rate_limits) as generic statusline extras, same contract as other CLIs.
	if gi.Additional != nil {
		if extras, ok := gi.Additional[llmtypes.StatusExtrasMetaKey].([]string); ok {
			status.SetStatusExtras(extras)
		}
		if windows, ok := gi.Additional[llmtypes.RateLimitWindowsMetaKey].([]llmtypes.RateLimitWindow); ok {
			status.SetRateLimitWindows(windows)
		}
		used, _ := gi.Additional[llmtypes.ContextUsedTokensMetaKey].(int)
		window, _ := gi.Additional[llmtypes.ContextWindowTokensMetaKey].(int)
		status.SetContextUsage(used, window)
	}
	return status
}

// GetStatusLine returns a snapshot of the current statusline for the session,
// satisfying llmtypes.StatusLineProvider. Codex sources it from the local
// rollout JSONL — see streamCodexStatusLine for the tmux-mode rationale.
func (c *CodexCLIAdapter) GetStatusLine(ctx context.Context, sessionID string) (*llmtypes.StatusLine, error) {
	s := codexAppSessionForOwner(sessionID)
	if s == nil {
		return nil, fmt.Errorf("no Codex app-server session for owner")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status == nil {
		return nil, fmt.Errorf("Codex has not reported usage yet")
	}
	return s.status, nil
}

func streamCodexTerminalSnapshot(ctx context.Context, sessionName string, streamChan chan<- llmtypes.StreamChunk, lastTerminalSnapshot *string) bool {
	streamCodexStatusLine(ctx, sessionName, streamChan)
	return streamCodexTerminalPaneSnapshot(ctx, sessionName, streamChan, lastTerminalSnapshot)
}

func streamCodexTerminalPaneSnapshot(ctx context.Context, sessionName string, streamChan chan<- llmtypes.StreamChunk, lastTerminalSnapshot *string) bool {
	snapshot, err := captureCodexPaneForDisplay(ctx, sessionName)
	if err != nil {
		return false
	}
	snapshot = strings.TrimRight(stripCodexANSIPreserveColors(snapshot), "\n")
	if strings.TrimSpace(snapshot) == "" || snapshot == *lastTerminalSnapshot {
		return false
	}
	select {
	case streamChan <- llmtypes.StreamChunk{
		Type:    llmtypes.StreamChunkTypeTerminal,
		Content: snapshot,
		Metadata: map[string]interface{}{
			"tmux_session":              sessionName,
			"codex_interactive_session": sessionName,
		},
	}:
		*lastTerminalSnapshot = snapshot
		return true
	default:
		return false
	}
}

func stripCodexEchoedUserPrompt(text, prompt string) string {
	text = strings.TrimSpace(text)
	prompt = strings.TrimSpace(prompt)
	if text == "" || prompt == "" {
		return text
	}

	lines := nonEmptyCodexLines(text)
	promptLines := nonEmptyCodexLines(prompt)
	if len(lines) == 0 || len(promptLines) == 0 {
		return text
	}

	bestStart := -1
	bestLen := 0
	for start := 0; start < len(lines) && start < 32; start++ {
		for promptStart := 0; promptStart < len(promptLines); promptStart++ {
			matchLen := 0
			for start+matchLen < len(lines) &&
				promptStart+matchLen < len(promptLines) &&
				codexPromptLinesEqual(lines[start+matchLen], promptLines[promptStart+matchLen]) {
				matchLen++
			}
			if matchLen > bestLen {
				bestStart = start
				bestLen = matchLen
			}
		}
	}

	if bestLen < 2 {
		return text
	}
	// If the matched run covers the ENTIRE extracted answer, this is not a leaked
	// prompt echo — segmentation already routes true prompt echoes to chrome, so
	// reaching here with a full-text match means the model's answer legitimately
	// reproduces (quoted) prompt content verbatim (e.g. "reply with exactly these
	// lines" tasks). Stripping it would yield an empty, failed extraction, so keep
	// the answer intact.
	if bestStart == 0 && bestLen == len(lines) {
		return text
	}
	out := make([]string, 0, len(lines)-bestLen)
	out = append(out, lines[:bestStart]...)
	out = append(out, lines[bestStart+bestLen:]...)
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func codexPromptLinesEqual(a, b string) bool {
	a = normalizeCodexPromptLine(a)
	b = normalizeCodexPromptLine(b)
	return a != "" && a == b
}

func normalizeCodexPromptLine(line string) string {
	line = strings.TrimSpace(stripCodexANSI(line))
	line = strings.TrimPrefix(line, "│")
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, ">")
	line = strings.TrimSpace(line)
	return line
}

func stripCodexHistoricalAssistantText(text string, historicalAssistantTexts []string) string {
	text = strings.TrimSpace(text)
	if text == "" || len(historicalAssistantTexts) == 0 {
		return text
	}
	for i := len(historicalAssistantTexts) - 1; i >= 0; i-- {
		historical := strings.TrimSpace(historicalAssistantTexts[i])
		if historical == "" {
			continue
		}
		if stripped, ok := stripCodexHistoricalPrefix(text, historical); ok {
			text = strings.TrimSpace(stripped)
			i = len(historicalAssistantTexts)
		}
	}
	return text
}

func stripCodexHistoricalPrefix(text, historical string) (string, bool) {
	if text == historical {
		return "", true
	}
	if strings.HasPrefix(text, historical) {
		return text[len(historical):], true
	}

	historicalLines := nonEmptyCodexLines(historical)
	if len(historicalLines) == 0 {
		return text, false
	}
	for start := 0; start < len(historicalLines); start++ {
		suffix := strings.Join(historicalLines[start:], "\n")
		if suffix == "" {
			continue
		}
		if text == suffix {
			return "", true
		}
		if strings.HasPrefix(text, suffix) {
			return text[len(suffix):], true
		}
	}
	return text, false
}

func nonEmptyCodexLines(text string) []string {
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

func isCodexActiveStatusLine(line string) bool {
	trimmed := strings.TrimSpace(stripCodexANSI(line))
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "generating") ||
		strings.HasPrefix(lower, "working (") ||
		strings.HasPrefix(lower, "thinking") ||
		strings.HasPrefix(lower, "processing") ||
		strings.HasPrefix(lower, "running") ||
		strings.HasPrefix(lower, "executing") ||
		strings.HasPrefix(lower, "calling ") ||
		strings.HasPrefix(lower, "calling api-bridge") ||
		strings.Contains(lower, "ctrl+l is disabled while a task is in progress") ||
		strings.Contains(lower, "esc to interrupt")
}

func isCodexCompletedStatusLine(line string) bool {
	trimmed := strings.TrimSpace(stripCodexANSI(line))
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "esc to interrupt") ||
		strings.Contains(lower, "still thinking") ||
		strings.Contains(lower, "thinking with") ||
		strings.Contains(lower, "working (") {
		return false
	}
	if !strings.Contains(lower, " for ") {
		return false
	}
	// Codex 0.144.1 renders the terminal turn footer with box-drawing
	// characters instead of the older star glyphs, for example:
	//
	//   ─ Worked for 4m 23s ────────
	//
	// Keep this deliberately narrower than accepting any box-drawing line that
	// contains " for ": a separator containing prose such as "waiting for
	// approval" must not make a live turn look complete.
	if strings.HasPrefix(trimmed, "─ ") || strings.HasPrefix(trimmed, "━ ") {
		footer := strings.ToLower(strings.TrimSpace(strings.TrimLeft(trimmed, "─━═—- ")))
		return strings.HasPrefix(footer, "worked for ") ||
			strings.HasPrefix(footer, "cogitated for ")
	}
	return strings.HasPrefix(trimmed, "✻ ") ||
		strings.HasPrefix(trimmed, "✽ ") ||
		strings.HasPrefix(trimmed, "✳ ") ||
		strings.HasPrefix(trimmed, "✶ ") ||
		strings.HasPrefix(trimmed, "✢ ") ||
		strings.HasPrefix(trimmed, "· ")
}

func isCodexBoxDrawingLine(line string) bool {
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

func interruptCodexInteractiveSession(sessionName string, logger interfaces.Logger) {
	interruptCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := tmuxinput.Default.Do(interruptCtx, tmuxinput.Request{
		SessionID: sessionName,
		Source:    "codex-cli-internal-interrupt",
		Priority:  tmuxinput.PriorityInterrupt,
	}, func(ctx context.Context) error {
		return runCodexCommand(ctx, nil, "tmux", "send-keys", "-t", sessionName, "Escape")
	})
	if err != nil && logger != nil {
		logger.Debugf("Failed to send Escape to Codex interactive session %s: %v", sessionName, err)
	}
}

func resetCodexPaneForTurn(ctx context.Context, sessionName string) {
	_ = runCodexCommand(ctx, nil, "tmux", "send-keys", "-t", sessionName, "C-u")
}

func captureCodexPane(ctx context.Context, sessionName string) (string, error) {
	return tmuxexec.CapturePane(ctx, sessionName, tmuxexec.DefaultScrollbackLines)
}

func captureCodexPaneForDisplay(ctx context.Context, sessionName string) (string, error) {
	// -e preserves ANSI SGR (color, bold, dim, etc.) so the frontend can
	// colorize the snapshot via ansi_up. Cursor positioning sequences are
	// stripped by stripCodexANSIPreserveColors before the snapshot leaves
	// the adapter so they don't garble the rendered output.
	// -J joins wrapped lines so the frontend can handle wrapping natively without
	// hard splitting words mid-line.
	return tmuxexec.CapturePaneANSI(ctx, sessionName, tmuxexec.DefaultScrollbackLines)
}

func codexCapturedAfterBaseline(captured, baseline string) string {
	if baseline != "" {
		if idx := strings.LastIndex(captured, baseline); idx >= 0 {
			return captured[idx+len(baseline):]
		}
	}
	return captured
}

func killCodexTmuxSession(ctx context.Context, sessionName string) error {
	if strings.TrimSpace(sessionName) == "" {
		return nil
	}
	// Reap the pane process trees (CLI + spawned MCP node subprocesses) before
	// killing the session — kill-session only SIGHUPs the pane process, so the
	// children would otherwise orphan and leak.
	tmuxcontrol.ReapSessionProcessTree(ctx, sessionName)
	if err := runCodexCommand(ctx, nil, "tmux", "kill-session", "-t", sessionName); err != nil {
		if isCodexTmuxSessionLostError(err) {
			return nil
		}
		return err
	}
	return nil
}

func isCodexTmuxSessionLostError(err error) bool {
	if err == nil {
		return false
	}
	if llmtypes.IsCodingAgentTmuxSessionLostError(err) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "no server running") ||
		strings.Contains(lower, "failed to connect to server") ||
		strings.Contains(lower, "can't find pane") ||
		strings.Contains(lower, "can't find session") ||
		strings.Contains(lower, "target pane not found") ||
		strings.Contains(lower, "no current target")
}

func codexInteractiveSessionPrefix() string {
	prefix := strings.TrimSpace(os.Getenv(EnvCodexInteractiveSessionPrefix))
	if prefix == "" {
		prefix = "mlp-codex-cli-int"
	}
	return sanitizeCodexTmuxSessionName(prefix)
}

func newCodexTmuxSessionName() string {
	return sanitizeCodexTmuxSessionName(fmt.Sprintf("%s-%d-%s", codexInteractiveSessionPrefix(), time.Now().UnixNano(), codexRandomHex(4)))
}

func codexInteractiveTimeout() time.Duration {
	return codexDurationFromEnvAllowZero(EnvCodexInteractiveTimeoutSeconds, defaultCodexInteractiveTimeout)
}

func codexInteractiveCallContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := codexInteractiveTimeout()
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func codexInteractiveIdleTimeout() time.Duration {
	return codexDurationFromEnv(EnvCodexInteractiveIdleTimeoutSeconds, defaultCodexInteractiveIdleTimeout)
}

func codexInteractiveRetention() time.Duration {
	return tmuxlaunch.Retention(defaultCodexInteractiveRetention)
}

func codexInteractivePromptWait() time.Duration {
	if strings.TrimSpace(os.Getenv(EnvCodexInteractivePromptWaitSeconds)) != "" {
		return codexDurationFromEnv(EnvCodexInteractivePromptWaitSeconds, defaultCodexPromptInactivityWait)
	}
	if strings.TrimSpace(os.Getenv(tmuxlaunch.EnvPromptWaitSeconds)) != "" {
		return tmuxlaunch.PromptWait(EnvCodexInteractivePromptWaitSeconds)
	}
	return defaultCodexPromptInactivityWait
}

func codexInteractivePromptMaxWait() time.Duration {
	return codexDurationFromEnv(EnvCodexInteractivePromptMaxWaitSeconds, defaultCodexPromptMaxWait)
}

// codexInteractiveStreamTmuxScreenEnabled reports whether to push raw
// terminal-pane snapshots to the caller's StreamChan mid-turn. Default ON, set
// per call via WithStreamTmuxScreen — there is no environment-variable
// fallback.
func codexInteractiveStreamTmuxScreenEnabled(opts *llmtypes.CallOptions) bool {
	if opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		if v, ok := opts.Metadata.Custom[MetadataKeyStreamTmuxScreen].(bool); ok {
			return v
		}
	}
	return true
}

// codexInteractiveStalePaneBackstop bounds how long the response-wait loop will
// tolerate a byte-frozen pane after the turn has produced activity but no ready
// prompt was detected. Detection-independent backstop against a silent hang.
func codexInteractiveStalePaneBackstop() time.Duration {
	return codexDurationFromEnv(EnvCodexInteractiveStalePaneBackstopSeconds, defaultCodexInteractiveStalePaneBackstop)
}

func codexDurationFromEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func codexDurationFromEnvAllowZero(key string, fallback time.Duration) time.Duration {
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

func runCodexCommand(ctx context.Context, stdin io.Reader, name string, args ...string) error {
	_, err := runCodexCommandOutput(ctx, stdin, name, args...)
	return err
}

func runCodexCommandOutput(ctx context.Context, stdin io.Reader, name string, args ...string) (string, error) {
	return tmuxexec.RunCommandOutput(ctx, stdin, nil, name, args...)
}

func codexRandomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func sanitizeCodexTmuxSessionName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "codex"
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

func stripCodexANSI(s string) string {
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

// stripCodexANSIPreserveColors strips ANSI cursor positioning / clear-screen
// sequences but preserves SGR (Select Graphic Rendition: color, bold, dim,
// underline, etc., terminated with `m`). The frontend feeds this output
// through ansi_up to colorize the rendered pane snapshot. Cursor positioning
// is dropped because ansi_up does not emulate VT100 movement.
func stripCodexANSIPreserveColors(s string) string {
	return paneview.StripANSIPreserveColors(s)
}
