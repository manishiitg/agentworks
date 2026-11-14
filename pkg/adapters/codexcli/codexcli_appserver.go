package codexcli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/procshutdown"
)

type codexAppSession struct {
	promptEpoch                  uint64
	owner                        string
	goal                         *llmtypes.NativeGoal
	goalActivationPending        bool
	goalPausedForWake            bool
	goalStateChanged             chan struct{}
	compacting                   bool
	turnMu                       sync.Mutex
	mu                           sync.Mutex
	rpc                          *codexRPC
	threadID, turnID, cwd, model string
	fingerprint                  [32]byte
	processFingerprint           [32]byte
	processEnvParts              map[string][32]byte
	fingerprintParts             map[string][32]byte
	fingerprintCustom            map[string][32]byte
	status                       *llmtypes.StatusLine
	// A successful steer response acknowledges acceptance; the corresponding
	// userMessage item is the durable acknowledgement, scoped to this send.
	receipts []*codexAppReceipt
	// Steers the server accepted whose userMessage item has not been seen
	// (receipt id -> text). Codex runs a steer its turn did not consume as its
	// own next turn, which consumeAppTurn must then follow.
	steers     map[string]string
	rateLimits *codexRateLimits
	idle       *time.Timer
}
type codexAppReceipt struct {
	id        string
	text      string
	accepted  time.Time
	durable   chan struct{}
	confirmed bool
}

var codexAppSessions = struct {
	sync.Mutex
	sessions map[string]*codexAppSession
}{sessions: make(map[string]*codexAppSession)}

func codexAppSessionForOwner(owner string) *codexAppSession {
	codexAppSessions.Lock()
	defer codexAppSessions.Unlock()
	return codexAppSessions.sessions[owner]
}
func closeCodexAppSession(owner string) {
	codexAppSessions.Lock()
	s := codexAppSessions.sessions[owner]
	delete(codexAppSessions.sessions, owner)
	codexAppSessions.Unlock()
	if s != nil {
		s.mu.Lock()
		r := s.rpc
		if s.idle != nil {
			s.idle.Stop()
			s.idle = nil
		}
		s.mu.Unlock()
		if r != nil {
			r.close()
		}
	}
}
func closeCodexAppSessions() {
	codexAppSessions.Lock()
	owners := make([]string, 0, len(codexAppSessions.sessions))
	for o := range codexAppSessions.sessions {
		owners = append(owners, o)
	}
	codexAppSessions.Unlock()
	for _, o := range owners {
		closeCodexAppSession(o)
	}
}
func codexOptionString(opts *llmtypes.CallOptions, key string) string {
	if opts.Metadata == nil {
		return ""
	}
	v, _ := opts.Metadata.Custom[key].(string)
	return strings.TrimSpace(v)
}
func codexOptionBool(opts *llmtypes.CallOptions, key string) bool {
	if opts.Metadata == nil {
		return false
	}
	v, _ := opts.Metadata.Custom[key].(bool)
	return v
}

func codexAppLaunchArgs(opts *llmtypes.CallOptions) []string {
	args := []string{}
	args = appendCodexDisableUpdateArgs(args)
	args = appendCodexAccountArgs(args, opts)
	if codexOptionBool(opts, MetadataKeyDisableShellTool) {
		for _, feature := range codexBridgeOnlyDisabledFeatures {
			args = append(args, "--disable", feature)
		}
	}
	args = appendCodexFeatureCSV(args, "--disable", codexOptionString(opts, MetadataKeyDisableFeatures))
	args = appendCodexFeatureCSV(args, "--enable", codexOptionString(opts, MetadataKeyEnableFeatures))
	if opts.Metadata != nil {
		if overrides, ok := opts.Metadata.Custom[MetadataKeyConfigOverrides].([]string); ok {
			for _, v := range overrides {
				args = append(args, "-c", v)
			}
		}
	}
	args = append(args, "app-server", "--listen", "stdio://")
	return args
}

var startCodexAppServer = func(c *CodexCLIAdapter, ctx context.Context, opts *llmtypes.CallOptions, cwd string) (*codexRPC, error) {
	return c.launchAppServer(ctx, opts, cwd)
}

// The child retains its executable/account/environment and OS sandbox. Thread
// instructions, MCP servers and Codex-level modes can be refreshed over RPC.
func (c *CodexCLIAdapter) appProcessFingerprint(opts *llmtypes.CallOptions, cwd string, environment map[string]string) ([32]byte, error) {
	bin, err := exec.LookPath("codex")
	if err != nil {
		return [32]byte{}, err
	}
	info, err := os.Stat(bin)
	if err != nil {
		return [32]byte{}, err
	}
	reads, err := strictCodexMCPRuntimePaths(opts)
	if err != nil {
		return [32]byte{}, err
	}
	sandboxCWD := ""
	if opts.CLISecurity != nil && opts.CLISecurity.Confined() {
		sandboxCWD = cwd
	}
	raw, err := json.Marshal(map[string]any{"sandbox_cwd": sandboxCWD, "binary": bin, "size": info.Size(), "mtime": info.ModTime().UnixNano(), "argv": codexAppLaunchArgs(opts), "security": opts.CLISecurity, "mcp_reads": reads, "env": environment})
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(raw), nil
}

func (c *CodexCLIAdapter) launchAppServer(ctx context.Context, opts *llmtypes.CallOptions, cwd string) (*codexRPC, error) {
	bin, err := exec.LookPath("codex")
	if err != nil {
		return nil, err
	}
	args := codexAppLaunchArgs(opts)
	life, stop := context.WithCancel(context.Background())
	cmd := exec.CommandContext(life, bin, args...)
	cmd.Dir = cwd
	procshutdown.SetGroup(cmd)
	procshutdown.KillGroupOnCancel(cmd)
	cmd.Env = llmtypes.MergeCodingAgentSecretEnvironment(buildCodexStructuredEnv(c.apiKey), opts)
	// A nil stderr goes directly to the null device (no copying goroutine
	// or inherited stderr pipe can delay process cleanup). Protocol errors
	// arrive on stdout; stderr may contain credentials.
	if llmtypes.NormalizeCLISecurityMode(policyMode(opts)) != llmtypes.CLISecurityModeCompatibility && !opts.CLISecurity.Confined() {
		stop()
		return nil, fmt.Errorf("Codex app-server requires enforced CLI confinement")
	}
	// MCP commands arrive later in thread configuration, so argv discovery
	// cannot grant their executable at launch (PLAT-872-57).
	mcpReads, err := strictCodexMCPRuntimePaths(opts)
	if err != nil {
		stop()
		return nil, err
	}
	cleanupSandbox, err := clisandbox.LandlockCmd(opts.CLISecurity, cmd, cwd, mcpReads, nil)
	if err != nil {
		stop()
		return nil, err
	}
	cleanup := func() { cleanupSandbox() }
	stderr := &clisandbox.StderrTail{}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		stop()
		cleanup()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stop()
		stdin.Close()
		cleanup()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		stop()
		stdin.Close()
		cleanup()
		return nil, err
	}
	r := &codexRPC{logger: c.logger, cmd: cmd, stdin: stdin, pending: make(map[string]chan codexRPCMessage), events: make(chan codexRPCMessage, 1024), done: make(chan struct{}), exited: make(chan struct{}), stop: stop, cleanup: cleanup}
	go r.read(stdout)
	go func() {
		err := cmd.Wait()
		if tail := strings.TrimSpace(stderr.String()); tail != "" {
			c.logger.Infof("[CLI_EXIT] provider=codex-cli app-server exited (%v): %s", err, tail)
		}
		r.finish(fmt.Errorf("codex app-server exited: %v", err))
		cleanup()
		close(r.exited)
	}()
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err = r.call(initCtx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "agentworks", "title": "AgentWorks", "version": "1"}, "capabilities": map[string]any{"experimentalApi": true}}, nil); err != nil {
		r.close()
		return nil, err
	}
	if err = r.write(map[string]any{"method": "initialized"}); err != nil {
		r.close()
		return nil, err
	}
	return r, nil
}

func (c *CodexCLIAdapter) generateContentAppServer(ctx context.Context, messages []llmtypes.MessageContent, opts *llmtypes.CallOptions) (*llmtypes.ContentResponse, error) {
	if opts.StreamChan != nil {
		defer close(opts.StreamChan)
	}
	emit := func(ch llmtypes.StreamChunk) {
		if opts.StreamChan != nil {
			select {
			case opts.StreamChan <- ch:
			case <-ctx.Done():
			}
		}
	}
	system, conversation := splitCodexSystemPrompt(messages)
	prompt := buildCodexInteractivePrompt(conversation)
	launchOnly := llmtypes.CodingProviderLaunchOnlyFromOptions(opts)
	idleCompact := llmtypes.CodingProviderIdleCompactFromOptions(opts)
	if prompt == "" && !launchOnly && !idleCompact && len(collectCodexImageContent(llmtypes.PendingHumanMessages(messages))) == 0 && len(llmtypes.NativeImageInputsForSession(ctx, codexInteractiveSessionIDFromOptions(opts))) == 0 {
		return nil, fmt.Errorf("codex-cli prompt is empty")
	}
	resume, err := codexValidatedResumeSessionIDFromOptions(opts)
	if err != nil {
		return nil, err
	}
	if idleCompact && resume == "" {
		return llmtypes.IdleCompactSkipResponse("codex-cli", llmtypes.IdleCompactReasonNoSession), nil
	}
	cwd := codexWorkingDirFromOptions(opts)
	system += llmtypes.CLIPrivateSkillInstructions(opts, codexSkillsSubdir)
	if skills := llmtypes.AttachedSkillsFromOptions(opts); len(skills) > 0 {
		if err = c.ProjectSkills(llmtypes.CLIArtifactDirectory(opts, cwd), skills); err != nil {
			return nil, err
		}
	}
	owner := codexInteractiveSessionIDFromOptions(opts)
	if owner == "" {
		owner = "codex-app-" + codexRandomHex(12)
	}
	ctx = llmtypes.ScopeCodingAgentQuestions(ctx, "codex-cli", owner)
	persistent := codexPersistentInteractiveFromOptions(opts)
	codexAppSessions.Lock()
	s := codexAppSessions.sessions[owner]
	if s == nil {
		s = &codexAppSession{owner: owner}
		codexAppSessions.sessions[owner] = s
	}
	codexAppSessions.Unlock()
	if !s.turnMu.TryLock() {
		return nil, fmt.Errorf("Codex session already has a turn; use live input")
	}
	defer s.turnMu.Unlock()
	s.mu.Lock()
	if s.idle != nil {
		s.idle.Stop()
		s.idle = nil
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		keep := persistent || (s.goalPausedForWake && ctx.Err() == nil)
		if !keep {
			s.mu.Unlock()
			closeCodexAppSession(owner)
			return
		}
		s.idle = time.AfterFunc(codexInteractiveIdleTimeout(), func() {
			if !s.turnMu.TryLock() {
				return
			}
			defer s.turnMu.Unlock()
			codexAppSessions.Lock()
			if codexAppSessions.sessions[owner] != s {
				codexAppSessions.Unlock()
				return
			}
			delete(codexAppSessions.sessions, owner)
			codexAppSessions.Unlock()
			s.mu.Lock()
			r := s.rpc
			s.mu.Unlock()
			if r != nil {
				r.close()
			}
		})
		s.mu.Unlock()
	}()
	// Never reuse a process after its account, secret grants, sandbox or launch
	// config changes. Store only a digest, not another copy of credentials.
	config := map[string]any{"model": c.modelID, "cwd": cwd, "runtime_dir": opts.CLIRuntimeDir, "system": system, "native_questions": llmtypes.CodingAgentQuestionsAvailable(ctx), "security": opts.CLISecurity, "env": llmtypes.MergeCodingAgentSecretEnvironment(buildCodexStructuredEnv(c.apiKey), opts)}
	if opts.Metadata != nil {
		for k, v := range opts.Metadata.Custom {
			if k != MetadataKeyResumeSessionID && k != llmtypes.CodingProviderIdleCompactMetadataKey {
				config[k] = v
			}
		}
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	fingerprint := sha256.Sum256(raw)
	environment := map[string]string{}
	for _, entry := range llmtypes.MergeCodingAgentSecretEnvironment(buildCodexStructuredEnv(c.apiKey), opts) {
		if key, value, ok := strings.Cut(entry, "="); ok {
			environment[key] = value
		}
	}
	envParts := map[string][32]byte{}
	for key, value := range environment {
		envParts[key] = sha256.Sum256([]byte(value))
	}
	processFingerprint, err := c.appProcessFingerprint(opts, cwd, environment)
	if err != nil {
		return nil, err
	}
	parts := codexFingerprintParts(config)
	customParts := codexCustomFingerprintParts(config)
	s.mu.Lock()
	r := s.rpc
	thread := s.threadID
	oldFingerprint := s.fingerprint
	oldProcessFingerprint := s.processFingerprint
	oldEnvParts := s.processEnvParts
	oldParts := s.fingerprintParts
	oldCustomParts := s.fingerprintCustom
	s.mu.Unlock()
	reason := "cold"
	if r != nil {
		reason = "reuse"
	}
	if r != nil {
		select {
		case <-r.done:
			reason = "process_exited"
			r.close()
			r = nil
		default:
		}
	}
	if r != nil && oldProcessFingerprint != processFingerprint {
		reason = "process_config_changed"
		if c.logger != nil {
			c.logger.Infof("[CODEX_WARM] owner=%s action=process_restart reason=process_config_changed env_keys=%s", owner, codexCustomFingerprintChanges(oldEnvParts, envParts))
		}
		r.close()
		r = nil
	}
	reloadThread := r == nil || oldFingerprint != fingerprint || (resume != "" && resume != thread)
	if c.logger != nil {
		c.logger.Infof("[CODEX_WARM] owner=%s persistent=%t action=%s changed=%s custom_keys=%s", owner, persistent, reason, codexFingerprintChanges(oldParts, parts), codexCustomFingerprintChanges(oldCustomParts, customParts))
	}
	if r == nil {
		// A different process boundary may be a different account. Resume only
		// an identity supplied by the host's current admitted request.
		if oldProcessFingerprint != processFingerprint {
			thread = ""
		}
		r, err = startCodexAppServer(c, ctx, opts, cwd)
		if err != nil {
			if c.logger != nil {
				c.logger.Infof("[CODEX_WARM] owner=%s action=process_failed reason=launch_error", owner)
			}
			return nil, err
		}
		if c.logger != nil {
			c.logger.Infof("[CODEX_WARM] owner=%s action=process_started reason=%s", owner, reason)
		}
	}
	if reloadThread {
		if resume != "" {
			thread = resume
		}
		sandbox := codexOptionString(opts, MetadataKeySandbox)
		if sandbox == "" {
			sandbox = "workspace-write"
		}
		sandbox = codexSandboxUnderConfinement(opts, sandbox)
		approval := codexOptionString(opts, MetadataKeyApprovalPolicy)
		if approval == "" {
			approval = "never"
			if llmtypes.CodingAgentQuestionsAvailable(ctx) {
				approval = "on-request"
			}
		}
		threadConfig, configErr := codexAppThreadConfig(opts, approval)
		if configErr != nil {
			r.close()
			return nil, configErr
		}
		threadConfig["features.default_mode_request_user_input"] = llmtypes.CodingAgentQuestionsAvailable(ctx)
		params := map[string]any{"cwd": cwd, "sandbox": sandbox, "approvalPolicy": approval, "developerInstructions": system, "config": threadConfig}
		model := codexOptionString(opts, MetadataKeyCodexModel)
		if model == "" {
			model = c.modelID
		}
		if model != "" && model != "codex-cli" {
			params["model"] = resolveCodexCLIModelID(model)
		}
		method := "thread/start"
		if thread != "" {
			method = "thread/resume"
			params["threadId"] = thread
			params["excludeTurns"] = true
			// Codex can retain the old developer instructions on the first
			// resumed turn. Its native baseInstructions override takes effect
			// immediately; keep the current full AgentWorks instructions above
			// historical developer messages without rewriting the rollout.
			if strings.TrimSpace(system) != "" {
				params["baseInstructions"] = system
			}
		}
		var result struct {
			Thread struct {
				ID string `json:"id"`
			}
			Model string `json:"model"`
		}
		if err = r.call(ctx, method, params, &result); err != nil {
			if c.logger != nil {
				c.logger.Infof("[CODEX_WARM] owner=%s action=thread_load_failed method=%s reason=rpc_error", owner, method)
			}
			r.close()
			return nil, err
		}
		if result.Thread.ID == "" {
			r.close()
			return nil, fmt.Errorf("codex app-server returned no thread id")
		}
		thread = result.Thread.ID
		if c.logger != nil {
			c.logger.Infof("[CODEX_WARM] owner=%s action=thread_loaded method=%s reason=config_or_identity_changed", owner, method)
		}
		s.mu.Lock()
		s.rpc = r
		s.threadID = thread
		s.cwd = cwd
		s.model = result.Model
		s.fingerprint = fingerprint
		s.processFingerprint = processFingerprint
		s.processEnvParts = envParts
		s.fingerprintParts = parts
		s.fingerprintCustom = customParts
		s.status = nil
		s.rateLimits = nil
		s.mu.Unlock()
	}
	if !reloadThread && c.logger != nil {
		c.logger.Infof("[CODEX_WARM] owner=%s action=thread_reuse reason=unchanged", owner)
	}
	// Account snapshots are private to this process/account. Some short turns
	// emit no rateLimits/updated notification, so seed from the native read.
	rateCtx, rateCancel := context.WithTimeout(ctx, 10*time.Second)
	var limits struct {
		RateLimits json.RawMessage            `json:"rateLimits"`
		ByID       map[string]json.RawMessage `json:"rateLimitsByLimitId"`
	}
	if r.call(rateCtx, "account/rateLimits/read", map[string]any{}, &limits) == nil {
		raw := limits.RateLimits
		if codex, ok := limits.ByID["codex"]; ok {
			raw = codex
		}
		s.mu.Lock()
		s.rateLimits = codexAppRateLimits(raw)
		s.mu.Unlock()
	}
	rateCancel()
	if launchOnly {
		return codexAppResponse(s, "", codexAppUsage{}), nil
	}
	if idleCompact {
		return c.compactAppThread(ctx, s, r, thread, emit)
	}
	if pause, ok := llmtypes.NativeGoalTurnPauseForSession(ctx, owner); ok {
		if err := pauseCodexGoalForTurn(ctx, s, r, thread, pause, emit); err != nil {
			return nil, err
		}
	}
	params, closeImages, err := codexParamsWithImages(ctx, owner, codexAppTurnParams(thread, prompt, opts), messages)
	if err != nil {
		return nil, err
	}
	defer closeImages()
	goalCommand, hasGoalCommand := llmtypes.NativeGoalCommandForSession(ctx, owner)
	activateGoal := false
	if hasGoalCommand {
		originalPrompt := prompt
		var reply *llmtypes.ContentResponse
		prompt, activateGoal, reply, err = c.prepareCodexGoalCommand(ctx, s, r, thread, goalCommand, emit)
		if err != nil || reply != nil {
			return reply, err
		}
		// Automatic restart/wake recovery must retain its safety notice and
		// current input, rather than replacing it with only the objective.
		if goalCommand.Action == "resume" && strings.TrimSpace(originalPrompt) != "" {
			prompt += "\n\n" + originalPrompt
		}
	}
	switch p := params.(type) {
	case map[string]any:
		p["input"] = codexAppTextInput(prompt)
	case *codexImageParams:
		p.base["input"] = codexAppTextInput(prompt)
	}
	turn, err := startCodexGoalTurn(ctx, s, r, thread, params, activateGoal, emit)
	finishCodexImageDelivery(owner, params, err)
	closeImages()
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	if s.turnID != turn {
		s.promptEpoch++
	}
	s.turnID = turn
	s.steers = nil
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.turnID = ""; s.mu.Unlock() }()
	emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypePromptAccepted, Metadata: map[string]any{"codex_thread_id": thread, "codex_turn_id": turn}})
	if c.logger != nil {
		c.logger.Infof("codex app-server turn started owner=%s thread=%s turn=%s", owner, thread, turn)
	}
	return c.consumeAppTurnWithCapacityRetry(ctx, s, r, owner, thread, turn, prompt, opts, emit)
}

func (c *CodexCLIAdapter) consumeAppTurnWithCapacityRetry(ctx context.Context, s *codexAppSession, r *codexRPC, owner, thread, turn, originalPrompt string, opts *llmtypes.CallOptions, emit func(llmtypes.StreamChunk)) (*llmtypes.ContentResponse, error) {
	var started struct {
		Turn codexAppTurn `json:"turn"`
	}
	var retry *llmtypes.NativeRetry
	finishRetry := func(success bool) {
		if retry == nil {
			return
		}
		end := *retry
		end.Phase = "end"
		end.Success = success
		retry = nil
		emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeNativeRetry, NativeRetry: &end})
	}
	defer func() { finishRetry(false) }()
	for attempt := 0; ; attempt++ {
		response, turnErr := c.consumeAppTurn(ctx, s, r, thread, turn, emit)
		finishRetry(turnErr == nil)
		var nativeErr *CodexTurnError
		// /compact is a command, not unfinished task input. Never turn a failed
		// compaction into a fresh empty-input continuation.
		if strings.EqualFold(strings.TrimSpace(originalPrompt), "/compact") || !errors.As(turnErr, &nativeErr) || !nativeErr.Capacity() || attempt >= 5 {
			if attempt > 0 && c.logger != nil {
				outcome := "done"
				reason := "completed"
				if turnErr != nil {
					outcome = "failed"
					reason = "turn_error"
				}
				if nativeErr != nil && nativeErr.Capacity() && attempt >= 5 {
					reason = "retry_limit"
				}
				c.logger.Infof("[CODEX_RETRY] owner=%s thread=%s turn=%s attempts=%d action=%s reason=%s", owner, thread, turn, attempt, outcome, reason)
			}
			return response, turnErr
		}
		delay := codexCapacityRetryWait(attempt)
		retry = &llmtypes.NativeRetry{Provider: "codex-cli", NativeSessionID: thread, NativeTurnID: turn, Phase: "start", Attempt: attempt + 1, MaxAttempts: 5, DelayMS: delay.Milliseconds()}
		emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeNativeRetry, NativeRetry: retry})
		if c.logger != nil {
			c.logger.Infof("[CODEX_RETRY] owner=%s thread=%s turn=%s attempt=%d delay_ms=%d action=waiting reason=server_overloaded", owner, thread, turn, attempt+1, delay.Milliseconds())
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			if c.logger != nil {
				c.logger.Infof("[CODEX_RETRY] owner=%s thread=%s turn=%s action=skipped reason=cancelled", owner, thread, turn)
			}
			return nil, ctx.Err()
		case <-r.done:
			timer.Stop()
			if c.logger != nil {
				c.logger.Infof("[CODEX_RETRY] owner=%s thread=%s turn=%s action=failed reason=process_exited", owner, thread, turn)
			}
			return nil, r.failure()
		case <-timer.C:
		}
		// Codex already persisted the input. Continue that thread with an empty
		// input; never resend the person's original message or attachments.
		retryParams := codexAppTurnParams(thread, "", opts)
		retryParams["input"] = []map[string]any{}
		started.Turn = codexAppTurn{}
		if err := r.call(ctx, "turn/start", retryParams, &started); err != nil {
			return nil, err
		}
		turn = started.Turn.ID
		if turn == "" {
			return nil, fmt.Errorf("codex retry returned no turn id")
		}
		s.mu.Lock()
		if s.turnID != turn {
			s.promptEpoch++
		}
		s.turnID = turn
		s.mu.Unlock()
		if c.logger != nil {
			c.logger.Infof("[CODEX_RETRY] owner=%s thread=%s turn=%s attempt=%d action=started", owner, thread, turn, attempt+1)
		}
	}
}

func codexAppTextInput(text string) []map[string]any {
	return []map[string]any{{"type": "text", "text": text, "text_elements": []any{}}}
}

type codexAppTurn struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  *struct {
		Message string          `json:"message"`
		Info    json.RawMessage `json:"codexErrorInfo"`
	} `json:"error"`
}
type codexAppUsage struct {
	Input      int `json:"inputTokens"`
	Cached     int `json:"cachedInputTokens"`
	CacheWrite int `json:"cacheWriteInputTokens"`
	Output     int `json:"outputTokens"`
	Reasoning  int `json:"reasoningOutputTokens"`
	Total      int `json:"totalTokens"`
}
type codexAppEvent struct {
	ThreadID   string          `json:"threadId"`
	TurnID     string          `json:"turnId"`
	ItemID     string          `json:"itemId"`
	Delta      string          `json:"delta"`
	Turn       codexAppTurn    `json:"turn"`
	Item       json.RawMessage `json:"item"`
	TokenUsage struct {
		Total  codexAppUsage `json:"total"`
		Last   codexAppUsage `json:"last"`
		Window int           `json:"modelContextWindow"`
	} `json:"tokenUsage"`
}

func codexAppResponse(s *codexAppSession, content string, usage codexAppUsage) *llmtypes.ContentResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	info := &llmtypes.GenerationInfo{InputTokens: intPtrIfNonZero(usage.Input), OutputTokens: intPtrIfNonZero(usage.Output), TotalTokens: intPtrIfNonZero(usage.Total), CachedContentTokens: intPtrIfNonZero(usage.Cached), ReasoningTokens: intPtrIfNonZero(usage.Reasoning), Additional: map[string]any{"provider": "codex-cli", "codex_mode": "app-server", "codex_thread_id": s.threadID, "codex_model": s.model, "prompt_tokens_include_cache": true, "cache_read_input_tokens": usage.Cached, "cache_creation_input_tokens": usage.CacheWrite}}
	if s.status != nil {
		for key, value := range s.status.Metadata {
			info.Additional[key] = value
		}
	}
	if windows := codexStructuredRateLimitWindows(s.rateLimits); len(windows) > 0 {
		info.Additional[llmtypes.RateLimitWindowsMetaKey] = windows
	}
	llmtypes.AttachCodingProviderSessionHandle(info, llmtypes.CodingProviderSessionHandle{Provider: "codex-cli", Transport: "app-server", NativeSessionID: s.threadID, WorkingDir: s.cwd, ProjectDirID: s.cwd, Model: s.model, Status: llmtypes.CodingProviderSessionStatusIdle})
	return &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{Content: content, StopReason: "stop", GenerationInfo: info}}, Usage: &llmtypes.Usage{InputTokens: usage.Input, OutputTokens: usage.Output, TotalTokens: usage.Total, CacheTokens: intPtrIfNonZero(usage.Cached), ReasoningTokens: intPtrIfNonZero(usage.Reasoning)}}
}

func SendCodexAppServerInput(ctx context.Context, owner, message string) error {
	s := codexAppSessionForOwner(owner)
	if s == nil {
		return fmt.Errorf("%w: no Codex app-server session for owner", llmtypes.ErrCodingAgentInputNotSent)
	}
	s.mu.Lock()
	r, thread, turn := s.rpc, s.threadID, s.turnID
	if r == nil || turn == "" || s.compacting {
		s.mu.Unlock()
		return fmt.Errorf("%w: no active Codex turn; start the next turn through GenerateContent", llmtypes.ErrCodingAgentInputNotSent)
	}
	s.mu.Unlock()
	params, closeImages, err := codexParamsWithImages(ctx, owner, map[string]any{"threadId": thread, "expectedTurnId": turn, "input": codexAppTextInput(message)}, nil)
	if err != nil {
		return err
	}
	defer closeImages()
	s.mu.Lock()
	r, thread, turn = s.rpc, s.threadID, s.turnID
	if r == nil || turn == "" || s.compacting {
		s.mu.Unlock()
		return fmt.Errorf("%w: native turn ended during image preparation", llmtypes.ErrCodingAgentInputNotSent)
	}
	switch p := params.(type) {
	case map[string]any:
		p["threadId"], p["expectedTurnId"] = thread, turn
	case *codexImageParams:
		p.base["threadId"], p.base["expectedTurnId"] = thread, turn
	}

	receipt := &codexAppReceipt{id: codexRandomHex(16), text: message, accepted: time.Now(), durable: make(chan struct{})}
	s.receipts = append(s.receipts, receipt)
	if s.steers == nil {
		s.steers = map[string]string{}
	}
	s.steers[receipt.id] = message
	s.mu.Unlock()
	switch p := params.(type) {
	case map[string]any:
		p["clientUserMessageId"] = receipt.id
	case *codexImageParams:
		p.base["clientUserMessageId"] = receipt.id
	}
	err = r.call(ctx, "turn/steer", params, nil)
	finishCodexImageDelivery(owner, params, err)
	if err != nil {
		s.mu.Lock()
		delete(s.steers, receipt.id)
		for i, v := range s.receipts {
			if v == receipt {
				s.receipts = append(s.receipts[:i], s.receipts[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
	}
	return err
}
func InterruptCodexAppServer(ctx context.Context, owner string) error {
	s := codexAppSessionForOwner(owner)
	if s == nil {
		return fmt.Errorf("%w: no Codex app-server session for owner", llmtypes.ErrCodingAgentInputNotSent)
	}
	s.mu.Lock()
	r, thread, turn := s.rpc, s.threadID, s.turnID
	s.mu.Unlock()
	if r == nil || turn == "" {
		return fmt.Errorf("no active Codex turn")
	}
	return r.call(ctx, "turn/interrupt", map[string]any{"threadId": thread, "turnId": turn}, nil)
}

// app-server does not accept the CLI --profile flag. Send session config over
// its private stdio connection; MCP credentials never enter argv or a shared
// project config. Reuse the same config renderer and account-scoped exclusions.
func codexAppThreadConfig(opts *llmtypes.CallOptions, approval string) (map[string]any, error) {
	var servers map[string]codexMCPServerSpec
	raw := codexOptionString(opts, MetadataKeyMCPServers)
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &servers); err != nil {
			return nil, fmt.Errorf("parse Codex MCP config: %w", err)
		}
		if len(servers) == 0 {
			return nil, fmt.Errorf("Codex MCP config is empty")
		}
	}
	for name, spec := range servers {
		if approval == "never" && spec.DefaultToolsApprovalMode == "" {
			spec.DefaultToolsApprovalMode = "approve"
			servers[name] = spec
		}
	}
	home := llmtypes.CLIHomeEnvironment(opts)["CODEX_HOME"]
	if home == "" {
		home = os.Getenv("CODEX_HOME")
	}
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = filepath.Join(userHome, ".codex")
	}
	config := map[string]any{}
	if profile := codexOptionString(opts, MetadataKeyConfigProfile); profile != "" {
		if filepath.Base(profile) != profile {
			return nil, fmt.Errorf("invalid Codex config profile")
		}
		data, err := os.ReadFile(filepath.Join(home, profile+".config.toml"))
		if err != nil {
			return nil, err
		}
		if err = toml.Unmarshal(data, &config); err != nil {
			return nil, err
		}
	}
	defaults := map[string]any{}
	if err := toml.Unmarshal([]byte(renderCodexConfigTOML(servers, personalCodexMCPServers(home))), &defaults); err != nil {
		return nil, err
	}
	for key, value := range defaults {
		config[key] = value
	}
	// thread/start config replaces the features table supplied at process
	// startup. Carry the selected tool policy into this table too; otherwise
	// the defaults above can resurrect shell tools in an MCP-only session.
	features, _ := config["features"].(map[string]any)
	if features == nil {
		features = map[string]any{}
	}
	features["concurrent_reasoning_summaries"] = true
	if codexOptionBool(opts, MetadataKeyDisableShellTool) {
		for _, name := range codexBridgeOnlyDisabledFeatures {
			features[name] = false
		}
	}
	for _, override := range []struct {
		key     string
		enabled bool
	}{{MetadataKeyDisableFeatures, false}, {MetadataKeyEnableFeatures, true}} {
		for _, name := range strings.Split(codexOptionString(opts, override.key), ",") {
			if name = strings.TrimSpace(name); name != "" {
				features[name] = override.enabled
			}
		}
	}
	config["features"] = features
	if dirs := codexOptionString(opts, MetadataKeyAdditionalDirs); dirs != "" {
		roots := []string{}
		for _, dir := range strings.Split(dirs, ",") {
			if dir = strings.TrimSpace(dir); dir != "" {
				roots = append(roots, dir)
			}
		}
		config["sandbox_workspace_write.writable_roots"] = roots
	}
	return config, nil
}

// Request summaries explicitly: a model catalog may otherwise default to none.
func codexAppTurnParams(thread, prompt string, opts *llmtypes.CallOptions) map[string]any {
	params := map[string]any{"threadId": thread, "input": codexAppTextInput(prompt)}
	if effort := codexOptionString(opts, MetadataKeyReasoningEffort); effort != "" {
		params["effort"] = effort
	}
	summary := codexOptionString(opts, MetadataKeyReasoningSummary)
	if summary == "" {
		summary = "auto"
	}
	params["summary"] = summary
	return params
}

// Digests are retained privately only; logs expose fixed category names, never
// prompts, paths, environment, custom metadata keys or their values.
func codexFingerprintParts(config map[string]any) map[string][32]byte {
	parts := map[string][32]byte{}
	custom := map[string]any{}
	for k, value := range config {
		switch k {
		case "model", "cwd", "runtime_dir", "system", "native_questions", "security", "env":
			raw, _ := json.Marshal(value)
			parts[k] = sha256.Sum256(raw)
		default:
			custom[k] = value
		}
	}
	raw, _ := json.Marshal(custom)
	parts["custom"] = sha256.Sum256(raw)
	return parts
}

func codexFingerprintChanges(old, current map[string][32]byte) string {
	if len(old) == 0 {
		return "initial"
	}
	var changed []string
	for _, key := range []string{"model", "cwd", "runtime_dir", "system", "native_questions", "security", "env", "custom"} {
		if old[key] != current[key] {
			changed = append(changed, key)
		}
	}
	if len(changed) == 0 {
		return "none"
	}
	return strings.Join(changed, ",")
}

// Keep only private digests. Log names of changed transport metadata keys,
// never their values. Added and removed keys both change the fingerprint.
func codexCustomFingerprintParts(config map[string]any) map[string][32]byte {
	parts := map[string][32]byte{}
	for key, value := range config {
		switch key {
		case "model", "cwd", "runtime_dir", "system", "native_questions", "security", "env":
			continue
		}
		raw, _ := json.Marshal(value)
		parts[key] = sha256.Sum256(raw)
	}
	return parts
}
func codexCustomFingerprintChanges(old, current map[string][32]byte) string {
	if old == nil {
		return "initial"
	}
	names := map[string]bool{}
	for key, value := range current {
		if prior, ok := old[key]; !ok || prior != value {
			names[codexDiagnosticKey(key)] = true
		}
	}
	for key := range old {
		if _, ok := current[key]; !ok {
			names[codexDiagnosticKey(key)] = true
		}
	}
	keys := make([]string, 0, len(names))
	for key := range names {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "none"
	}
	return strings.Join(keys, ",")
}
func codexDiagnosticKey(key string) string {
	if len(key) == 0 || len(key) > 80 {
		return "other"
	}
	for _, r := range key {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
			return "other"
		}
	}
	return key
}

var codexCapacityRetryWait = codexCapacityRetryDelay

func codexCapacityRetryDelay(attempt int) time.Duration {
	delay := 2 * time.Second * time.Duration(1<<attempt)
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}

func codexAppErrorInfo(raw json.RawMessage) string {
	var code string
	if json.Unmarshal(raw, &code) == nil {
		if code == "serverOverloaded" || code == codexErrorInfoServerOverloaded {
			return codexErrorInfoServerOverloaded
		}
		return code
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil {
		if _, ok := object["serverOverloaded"]; ok {
			return codexErrorInfoServerOverloaded
		}
	}
	return ""
}
