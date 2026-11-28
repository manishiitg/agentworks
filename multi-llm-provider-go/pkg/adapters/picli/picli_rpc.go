package picli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/instructions"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/procshutdown"
)

type piRPCSession struct {
	turnMu                                    sync.Mutex
	mu                                        sync.Mutex
	rpc                                       *piRPC
	nativeID, sessionFile, cwd, model, turnID string
	fingerprint                               [32]byte
	parts                                     map[string][32]byte
	instructions                              instructions.Tracker
	status                                    *llmtypes.StatusLine
	idle                                      *time.Timer
	receipts                                  []*piRPCReceipt
}
type piRPCReceipt struct {
	text       string
	at         time.Time
	timestamp  int64
	observed   chan struct{}
	confirmed  bool
	claimed    bool
	occurrence int
}

var piRPCSessions = struct {
	sync.Mutex
	sessions map[string]*piRPCSession
}{sessions: map[string]*piRPCSession{}}

func piRPCSessionForOwner(owner string) *piRPCSession {
	piRPCSessions.Lock()
	defer piRPCSessions.Unlock()
	return piRPCSessions.sessions[strings.TrimSpace(owner)]
}
func closePiRPCSession(owner string) {
	piRPCSessions.Lock()
	s := piRPCSessions.sessions[owner]
	delete(piRPCSessions.sessions, owner)
	piRPCSessions.Unlock()
	if s == nil {
		return
	}
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
func closePiRPCSessions() {
	piRPCSessions.Lock()
	owners := make([]string, 0, len(piRPCSessions.sessions))
	for o := range piRPCSessions.sessions {
		owners = append(owners, o)
	}
	piRPCSessions.Unlock()
	for _, o := range owners {
		closePiRPCSession(o)
	}
}

func (p *PiCLIAdapter) launchRPC(ctx context.Context, opts *llmtypes.CallOptions, cwd, nativeID, system string) (*piRPC, string, error) {
	prefix, extraEnv, err := piCommandPrefix()
	if err != nil {
		return nil, "", err
	}
	agentDir, sessionDir, cleanupConfig, err := preparePiNativeMCPConfig(llmtypes.CLIArtifactDirectory(opts, cwd), nativeID, opts, p.customProvider)
	if err != nil {
		return nil, "", err
	}
	if cleanupConfig == nil {
		cleanupConfig = func() {}
	}
	args := append([]string{}, prefix[1:]...)
	provider, model := p.resolveStructuredProviderModel(opts)
	args = append(args, "--mode", "rpc", "--provider", provider, "--model", model, "--session-id", nativeID, "--session-dir", sessionDir, "--no-extensions", "--no-skills", "--no-context-files", "--approve")
	if piMCPConfigFromOptions(opts) != "" {
		args = append(args, "-e", piMCPExtensionFromOptions(opts))
	}
	if piBridgeOnlyToolsFromOptions(opts) {
		args = append(args, "--no-builtin-tools")
	}
	if thinking := piEffectiveThinkingLevel(provider, model, opts); thinking != "" {
		args = append(args, "--thinking", thinking)
	}
	if system != "" {
		args = append(args, "--append-system-prompt", system)
	}
	if len(llmtypes.AttachedSkillsFromOptions(opts)) > 0 {
		args = append(args, "--skill", piProjectedSkillsPath(llmtypes.CLIArtifactDirectory(opts, cwd)))
	}
	env := llmtypes.MergeCodingAgentSecretEnvironment(os.Environ(), opts)
	// A saved login belongs to the selected account environment. Never consult
	// the server's home after a scoped HOME/PI_CODING_AGENT_DIR has been supplied.
	selectedAgent := piAgentDirFromEnv(env)
	if selectedAgent == "" {
		for _, e := range env {
			if h, ok := strings.CutPrefix(e, "HOME="); ok {
				selectedAgent = filepath.Join(h, ".pi", "agent")
			}
		}
	}
	auth := filepath.Join(selectedAgent, "auth.json")
	if selectedAgent != "" && filepath.Clean(selectedAgent) != filepath.Clean(agentDir) {
		target := filepath.Join(agentDir, "auth.json")
		_ = os.Remove(target)
		if _, err := os.Stat(auth); err == nil && p.apiKey == "" {
			if err = os.Symlink(auth, target); err != nil {
				cleanupConfig()
				return nil, "", err
			}
		}
	}
	env = piOverrideEnv(env, extraEnv)
	env = piOverrideEnv(env, piNodeOptionsEnv())
	env = piOverrideEnv(env, piNativeMCPEnv(agentDir, sessionDir))
	env = piOverrideEnv(env, piAPIKeyEnv(provider, p.apiKey))
	// Pi keeps only the chosen model's provider key, whether it came from this account or the server (PLAT-872-91).
	if opts != nil && opts.CLISecurity != nil {
		opts.CLISecurity.OwnLoginEnv = nil
		for _, entry := range piAPIKeyEnv(provider, "x") {
			name, _, _ := strings.Cut(entry, "=")
			opts.CLISecurity.OwnLoginEnv = append(opts.CLISecurity.OwnLoginEnv, name)
		}
	}
	env = piOverrideEnv(env, piBridgeShellEnvFromMCPConfig(piMCPConfigFromOptions(opts)))
	life, stop := context.WithCancel(context.Background())
	cmd := exec.CommandContext(life, prefix[0], args...)
	cmd.Dir = cwd
	cmd.Env = env
	procshutdown.SetGroup(cmd)
	procshutdown.KillGroupOnCancel(cmd)
	cleanupSandbox, err := piLandlockCmd(opts, cmd, cwd, agentDir)
	if err != nil {
		stop()
		cleanupConfig()
		return nil, "", err
	}
	cleanup := func() { cleanupSandbox(); cleanupConfig() }
	stdin, err := cmd.StdinPipe()
	if err != nil {
		stop()
		cleanup()
		return nil, "", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stop()
		stdin.Close()
		cleanup()
		return nil, "", err
	}
	// Diagnostics can contain credential-bearing extension errors. Protocol
	// failures are returned on stdout; do not copy raw stderr into app logs.
	if err = cmd.Start(); err != nil {
		stop()
		stdin.Close()
		cleanup()
		return nil, "", err
	}
	r := &piRPC{cmd: cmd, nativeID: nativeID, stdin: stdin, pending: map[string]chan piRPCRecord{}, events: make(chan piRPCRecord, 4096), done: make(chan struct{}), exited: make(chan struct{}), exitInfo: make(chan struct{}), stop: stop, process: llmtypes.NewAgentProcess("pi-cli", piInteractiveSessionIDFromOptions(opts))}
	go r.read(stdout)
	go func() {
		err := cmd.Wait()
		// Pi's stderr can carry credential-bearing extension errors, so only the exit status is reported.
		exitErr := clisandbox.UnexpectedExit("Pi", err, "")
		exitReason := "process_exited"
		if life.Err() != nil {
			exitReason = "stopped"
		}
		r.process.Exited(nil, exitReason)
		r.mu.Lock()
		r.exitErr = exitErr
		r.mu.Unlock()
		close(r.exitInfo)
		r.finish(exitErr)
		cleanup()
		close(r.exited)
	}()
	initCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var state struct {
		SessionID   string `json:"sessionId"`
		SessionFile string `json:"sessionFile"`
	}
	if err = r.call(initCtx, "get_state", nil, &state); err != nil {
		r.close()
		return nil, "", err
	}
	if state.SessionID != nativeID {
		r.close()
		return nil, "", fmt.Errorf("Pi RPC returned a different native session")
	}
	return r, state.SessionFile, nil
}

func (p *PiCLIAdapter) generateContentRPC(ctx context.Context, messages []llmtypes.MessageContent, opts *llmtypes.CallOptions) (resp *llmtypes.ContentResponse, err error) {
	if opts.StreamChan != nil {
		defer close(opts.StreamChan)
	}
	turnCtx, cancel := context.WithTimeout(ctx, piMaxTurnDuration())
	defer cancel()
	emit := func(c llmtypes.StreamChunk) {
		if opts.StreamChan != nil {
			select {
			case opts.StreamChan <- c:
			case <-turnCtx.Done():
			}
		}
	}
	system, conversation := splitPiSystemPrompt(messages)
	if system == "" {
		system = llmtypes.CodingProviderLaunchSystemPromptFromOptions(opts)
	}
	prompt := buildPiPrompt(conversation)
	launchOnly := llmtypes.CodingProviderLaunchOnlyFromOptions(opts)
	idleCompact := llmtypes.CodingProviderIdleCompactFromOptions(opts)
	if strings.TrimSpace(prompt) == "" && !launchOnly && !idleCompact && !piHasPendingImages(ctx, piInteractiveSessionIDFromOptions(opts), conversation) {
		return nil, fmt.Errorf("pi-cli prompt is empty")
	}
	resume := piResumeSessionIDFromOptions(opts)
	if resume != "" && !isValidPiNativeSessionID(resume) {
		return nil, fmt.Errorf("invalid Pi native session ID")
	}
	if idleCompact && resume == "" {
		return llmtypes.IdleCompactSkipResponse("pi-cli", llmtypes.IdleCompactReasonNoSession), nil
	}
	cwd := piWorkingDirFromOptions(opts)
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	if skills := llmtypes.AttachedSkillsFromOptions(opts); len(skills) > 0 {
		if err = p.ProjectSkills(llmtypes.CLIArtifactDirectory(opts, cwd), skills); err != nil {
			return nil, err
		}
	}
	owner := piInteractiveSessionIDFromOptions(opts)
	if owner == "" {
		owner = "pi-rpc-" + piRandomHex(12)
	}
	defer func() { err = piRPCRequestFailure(p.GetModelID(), owner, err, false) }()
	persistent := piPersistentInteractiveFromOptions(opts)
	piRPCSessions.Lock()
	s := piRPCSessions.sessions[owner]
	if s == nil {
		s = &piRPCSession{}
		piRPCSessions.sessions[owner] = s
	}
	piRPCSessions.Unlock()
	if !s.turnMu.TryLock() {
		return nil, fmt.Errorf("Pi session already has a turn; use live input")
	}
	defer s.turnMu.Unlock()
	s.mu.Lock()
	if s.idle != nil {
		s.idle.Stop()
		s.idle = nil
	}
	s.mu.Unlock()
	defer func() {
		if !persistent || (err != nil && !errors.Is(err, errPiRPCAborted)) {
			closePiRPCSession(owner)
			return
		}
		s.mu.Lock()
		s.idle = time.AfterFunc(piInteractiveIdleTimeout(), func() {
			if !s.turnMu.TryLock() {
				return
			}
			defer s.turnMu.Unlock()
			piRPCSessions.Lock()
			same := piRPCSessions.sessions[owner] == s
			if same {
				delete(piRPCSessions.sessions, owner)
			}
			piRPCSessions.Unlock()
			if same {
				s.mu.Lock()
				r := s.rpc
				s.mu.Unlock()
				if r != nil {
					r.close()
				}
			}
		})
		s.mu.Unlock()
	}()
	// Instruction changes go in the turn message via instructions.Tracker instead of restarting (#4560).
	config := map[string]any{"model": p.modelID, "key": p.apiKey, "custom": p.customProvider, "cwd": cwd, "runtime_dir": opts.CLIRuntimeDir, "instructions_empty": strings.TrimSpace(system) == "", "security": opts.CLISecurity, "env": llmtypes.MergeCodingAgentSecretEnvironment(os.Environ(), opts)}
	if opts.Metadata != nil {
		for k, v := range opts.Metadata.Custom {
			if k != MetadataKeyResumeSessionID && k != llmtypes.CodingProviderLaunchOnlyMetadataKey && k != llmtypes.CodingProviderIdleCompactMetadataKey && k != llmtypes.CodingProviderLaunchSystemPromptMetadataKey && k != llmtypes.AttachedSkillsMetadataKey {
				config[k] = v
			}
		}
	}
	raw, e := json.Marshal(config)
	if e != nil {
		return nil, e
	}
	fingerprint := sha256.Sum256(raw)
	parts := llmtypes.AgentProcessDigests(config)
	s.mu.Lock()
	r, native, old, oldParts := s.rpc, s.nativeID, s.fingerprint, s.parts
	s.mu.Unlock()
	launchReason := "first"
	if r != nil {
		select {
		case <-r.done:
			r.close()
			r = nil
			launchReason = "process_exited"
		default:
		}
	}
	if r != nil && (old != fingerprint || (resume != "" && resume != native)) {
		launchReason = "other_conversation"
		if old != fingerprint {
			launchReason = "launch_changed:" + llmtypes.AgentProcessChanged(oldParts, parts)
		}
		r.close()
		r = nil
	}
	if r != nil {
		r.process.Reused(nil, "launch_unchanged")
	}
	if r == nil {
		if old != ([32]byte{}) && old != fingerprint {
			native = ""
		}
		if resume != "" {
			native = resume
		}
		if native == "" {
			native = generatePiNativeSessionID()
		}
		var file string
		r, file, err = p.launchRPC(turnCtx, opts, cwd, native, system)
		if err != nil {
			return nil, err
		}
		r.process.Launched(nil, launchReason)
		s.mu.Lock()
		s.rpc = r
		s.nativeID = native
		s.sessionFile = file
		s.cwd = cwd
		s.model = p.GetModelID()
		s.fingerprint = fingerprint
		s.parts = parts
		s.status = nil
		s.receipts = nil
		s.mu.Unlock()
		s.instructions.Started(system)
	}
	if launchOnly {
		return piRPCResponse(s, "", nil, nil), nil
	}
	if idleCompact {
		resp, compactErr := compactPiRPC(turnCtx, s, r, emit)
		if compactErr == nil {
			s.instructions.Compacted()
		}
		return resp, compactErr
	}
	delivery, deliveredInstructions := s.instructions.Next(system)
	prompt = instructions.InMessage(delivery, prompt)
	params, closeImages, prepErr := piParamsWithImages(turnCtx, owner, map[string]any{"message": prompt}, conversation)
	if prepErr != nil {
		return nil, prepErr
	}
	// Only pending human inputs are sent: Pi's saved session owns history.
	s.mu.Lock()
	s.turnID = "pi-turn-" + piRandomHex(8)
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.turnID = ""; s.mu.Unlock() }()
	turnCtx = llmtypes.ScopeCodingAgentQuestions(turnCtx, "pi-cli", owner)
	accepted := make(chan piRPCAccepted, 1)
	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		defer closeImages()
		var reply piRPCAccepted
		reply.Err = r.callPrepared(turnCtx, "prompt", params, &reply)
		finishPiImageDelivery(owner, params, reply.Err)
		accepted <- reply
	}()

	defer func() { cancel(); <-callDone }()
	resp, turnErr := p.consumeRPCTurn(turnCtx, s, r, emit, accepted)
	if turnErr == nil {
		deliveredInstructions()
	}
	return resp, turnErr
}

func piRPCMetadata(s *piRPCSession) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{"provider": "pi-cli", "pi_mode": "rpc", "pi_session_id": s.nativeID, "pi_turn_id": s.turnID, "pi_model": s.model, "pi_token_usage_source": "stream-json"}
}
func piRPCResponse(s *piRPCSession, text string, usage *piRPCUsage, messages []llmtypes.MessageContent) *llmtypes.ContentResponse {
	if usage == nil {
		usage = &piRPCUsage{}
	}
	additional := piRPCMetadata(s)
	s.mu.Lock()
	additional["pi_transcript_file"] = s.sessionFile
	handle := llmtypes.CodingProviderSessionHandle{Provider: "pi-cli", Transport: llmtypes.CodingProviderTransportRPC, NativeSessionID: s.nativeID, WorkingDir: s.cwd, Model: s.model, Status: llmtypes.CodingProviderSessionStatusIdle}
	s.mu.Unlock()
	additional["pi_completion_source"] = "agent_settled"
	additional["cache_read_input_tokens"] = usage.CacheRead
	additional["cache_creation_input_tokens"] = usage.CacheWrite
	additional["prompt_tokens_include_cache"] = false
	additional["cost_usd"] = usage.Cost.Total
	additional["input_cost_usd"] = usage.Cost.Input
	additional["output_cost_usd"] = usage.Cost.Output
	additional["cache_cost_usd"] = usage.Cost.CacheRead + usage.Cost.CacheWrite
	gi := &llmtypes.GenerationInfo{InputTokens: intPtr(usage.Input), OutputTokens: intPtr(usage.Output), TotalTokens: intPtr(usage.Total), CachedContentTokens: intPtr(usage.CacheRead), Additional: additional}
	llmtypes.AttachCodingProviderSessionHandle(gi, handle)
	llmtypes.AttachCodingProviderIntermediateMessages(gi, llmtypes.CodingProviderIntermediateMessages{Provider: "pi-cli", Transport: "rpc", Messages: messages})
	return &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{Content: text, StopReason: "stop", GenerationInfo: gi}}, Usage: &llmtypes.Usage{InputTokens: usage.Input, OutputTokens: usage.Output, TotalTokens: usage.Total, CacheTokens: intPtr(usage.CacheRead)}}
}

// GetStatusLine returns the latest native usage record for this owner.
func (p *PiCLIAdapter) GetStatusLine(ctx context.Context, owner string) (*llmtypes.StatusLine, error) {
	s := piRPCSessionForOwner(owner)
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status == nil {
		return nil, nil
	}
	copy := *s.status
	return &copy, nil
}
