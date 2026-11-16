package agycli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/procshutdown"
)

// The ACP lane runs a chat on `agy acp` (Agent Client Protocol on stdio, built
// into agy): one process per chat, the launch the JSON lane uses (private home
// with the chat's MCP catalogue, tool-mode hook, sandbox), and the conversation
// kept in the same store under the same id. Replies, thinking and tool calls
// stream as protocol events; nothing is read off a screen.

// AgyTransportEnv picks agy's transport for chats: acp (default), tmux.
const AgyTransportEnv = "AGY_CLI_TRANSPORT"

// AgyACPSelected reports whether chats run on `agy acp`.
func AgyACPSelected() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv(AgyTransportEnv)), "tmux")
}

type agyACPConfigOption struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Type     string `json:"type"`
}
type agyACPConfigResult struct {
	SessionID     string               `json:"sessionId"`
	ConfigOptions []agyACPConfigOption `json:"configOptions"`
}

type agyACPSession struct {
	imagesAllowed  bool
	transcriptHome string
	status         *llmtypes.StatusLine
	configOptions  []agyACPConfigOption
	turnMu         sync.Mutex
	mu             sync.Mutex
	rpc            *agyACP
	sessionID      string
	workdir        string
	model          string
	fingerprint    string
	parts          map[string][32]byte
	listener       chan agyACPMessage
	release        func()
}

var agyACPSessions = struct {
	sync.Mutex
	m map[string]*agyACPSession
}{m: map[string]*agyACPSession{}}

// CloseAgyACPSession ends a chat's `agy acp` process (session/close first, so
// its conversation store has no writer).
func CloseAgyACPSession(owner string) {
	agyACPSessions.Lock()
	s := agyACPSessions.m[owner]
	delete(agyACPSessions.m, owner)
	agyACPSessions.Unlock()
	if s == nil || s.rpc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = s.rpc.call(ctx, "session/close", map[string]any{"sessionId": s.sessionID}, nil)
	cancel()
	s.rpc.close()
	if s.release != nil {
		s.release()
	}
}

// AgyACPSessionActive reports whether a chat runs on `agy acp` now.
func AgyACPSessionActive(owner string) bool {
	agyACPSessions.Lock()
	defer agyACPSessions.Unlock()
	return agyACPSessions.m[owner] != nil
}

// agyACPModel splits agy's model id ("gemini-3.8-flash-high",
// "gemini-4-hydrogen-xhigh") into the ACP model option and thinking level.
func agyACPModel(model, effort string) (string, string) {
	for _, level := range agyReasoningEffortLevels {
		if strings.HasSuffix(model, "-"+level) {
			if effort == "" {
				effort = level
			}
			return strings.TrimSuffix(model, "-"+level), effort
		}
	}
	return model, effort
}

func (a *AgyCLIAdapter) launchAgyACP(ctx context.Context, opts *llmtypes.CallOptions, workdir, system, resume string) (*agyACPSession, error) {
	toolMode, err := agyToolModeForLaunch(opts)
	if err != nil {
		return nil, err
	}
	args := []string{"acp", "--disable-telemetry"}
	mcpJSON := agyStringMetadata(opts, MetadataKeyMCPConfig)
	var servers []agyMCPServer
	if strings.TrimSpace(mcpJSON) != "" {
		if servers, err = agyParseMCPServers(mcpJSON); err != nil {
			return nil, err
		}
	}
	// The same permission rule as the JSON lane: a mounted bridge or Full CLI
	// runs without per-tool prompts; the tool-mode hook still applies.
	if strings.TrimSpace(mcpJSON) != "" || agyFullNativeToolsMode(toolMode) {
		args = append(args, "--dangerously-skip-permissions")
	}
	privateHome, releaseHome, err := agyIsolatedHomeForCall(servers, workdir, opts)
	if err != nil {
		return nil, err
	}
	releaseHook, err := agyHoldToolModeHookForCall(workdir, privateHome, toolMode, opts, agyBridgeHostPorts(mcpJSON)...)
	if err != nil {
		releaseHome()
		return nil, err
	}
	release := func() { releaseHook(); releaseHome() }
	life, stop := context.WithCancel(context.Background())
	cmd := exec.CommandContext(life, "agy", args...)
	cmd.Dir = workdir
	procshutdown.SetGroup(cmd)
	procshutdown.KillGroupOnCancel(cmd)
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "HOME=") {
			env = append(env, entry)
		}
	}
	cmd.Env = agyPinHome(llmtypes.MergeCodingAgentSecretEnvironment(append(env, "HOME="+privateHome, "NO_OPEN_BROWSER=1"), opts), privateHome)
	agyLogLaunch(cmd.Env)
	cleanupSandbox, err := agyLandlockCmd(opts, cmd, workdir, privateHome)
	if err != nil {
		stop()
		release()
		return nil, err
	}
	// Bounded diagnostics are exposed only on failure, with payloads omitted.
	stderr := &clisandbox.StderrTail{}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		stop()
		cleanupSandbox()
		release()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stop()
		_ = stdin.Close()
		cleanupSandbox()
		release()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		stop()
		_ = stdin.Close()
		cleanupSandbox()
		release()
		if _, lookErr := exec.LookPath("agy"); lookErr != nil {
			return nil, fmt.Errorf("agy CLI not in PATH: %w", lookErr)
		}
		return nil, fmt.Errorf("start agy acp: %w", err)
	}
	r := &agyACP{stderr: stderr, owner: agyStringMetadata(opts, MetadataKeyInteractiveSessionID), cmd: cmd, stdin: stdin, pending: map[string]chan agyACPMessage{}, events: make(chan agyACPMessage, 4096), done: make(chan struct{}), exited: make(chan struct{}), stop: stop}
	r.process = llmtypes.NewAgentProcess("agy-cli", r.owner)
	go r.read(stdout)
	go func() {
		err := cmd.Wait()
		if err != nil {
			log.Printf("[CLI_EXIT] session=%s provider=agy-cli transport=acp action=failed exit_code=%d reason=process_exited last_error=%q", r.owner, cmd.ProcessState.ExitCode(), r.stderrLine())
		}
		exitReason := "process_exited"
		if life.Err() != nil {
			exitReason = "stopped"
		}
		r.process.Exited(nil, exitReason)
		r.finish(fmt.Errorf("agy acp exited: %v", err))
		cleanupSandbox()
		close(r.exited)
	}()
	s := &agyACPSession{rpc: r, workdir: workdir, transcriptHome: agyTranscriptHome(opts), release: release}
	go s.pump()
	initCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	imagesAllowed, initErr := initializeAgyImageCapability(initCtx, r)
	if initErr != nil {
		s.closeNow()
		return nil, initErr
	}
	s.imagesAllowed = imagesAllowed
	// The service's Gemini key, when the chat's account gives one, signs in;
	// otherwise agy uses its stored login.
	if agyEnvHas(cmd.Env, "GEMINI_API_KEY") || agyEnvHas(cmd.Env, "GOOGLE_API_KEY") {
		if err := r.call(initCtx, "authenticate", map[string]any{"methodId": "gemini-api-key"}, nil); err != nil {
			s.closeNow()
			return nil, err
		}
	}
	params := map[string]any{"cwd": workdir, "mcpServers": []any{}}
	if strings.TrimSpace(system) != "" {
		params["_meta"] = map[string]any{"antigravity": map[string]any{"systemPrompt": map[string]any{"append": system}}}
	}
	var opened agyACPConfigResult
	if resume != "" {
		// The chat's own conversation, by its exact id; never a fresh one.
		params["sessionId"] = resume
		err = r.call(initCtx, "session/load", params, &opened)
		opened.SessionID = resume
	} else {
		err = r.call(initCtx, "session/new", params, &opened)
	}
	if err != nil {
		s.closeNow()
		return nil, err
	}
	if opened.SessionID == "" {
		s.closeNow()
		return nil, fmt.Errorf("agy ACP did not return a session id")
	}
	s.mu.Lock()
	s.sessionID = opened.SessionID
	s.mu.Unlock()
	s.rememberConfigOptions(opened.ConfigOptions)
	return s, nil
}

func agyEnvHas(env []string, name string) bool {
	value := ""
	for _, entry := range env {
		if key, v, ok := strings.Cut(entry, "="); ok && key == name {
			value = v
		}
	}
	return strings.TrimSpace(value) != ""
}

func (s *agyACPSession) closeNow() {
	s.rpc.close()
	if s.release != nil {
		s.release()
	}
}

// pump reads every event for the life of the process: a running turn gets
// them; between turns, requests are answered and the rest dropped.
func (s *agyACPSession) pump() {
	for {
		select {
		case <-s.rpc.done:
			return
		case msg := <-s.rpc.events:
			s.mu.Lock()
			currentSession := s.sessionID
			s.mu.Unlock()
			if msg.Method == "session/update" {
				var notification struct {
					SessionID string `json:"sessionId"`
					Update    struct {
						Kind          string               `json:"sessionUpdate"`
						ConfigOptions []agyACPConfigOption `json:"configOptions"`
					} `json:"update"`
				}
				if json.Unmarshal(msg.Params, &notification) == nil && notification.SessionID == currentSession && notification.Update.Kind == "config_option_update" {
					s.rememberConfigOptions(notification.Update.ConfigOptions)
				}
			}
			s.mu.Lock()
			listener := s.listener
			s.mu.Unlock()
			if listener != nil {
				select {
				case listener <- msg:
				case <-s.rpc.done:
					return
				}
				continue
			}
			if len(msg.ID) > 0 && msg.Method != "" {
				s.answer(msg)
			}
		}
	}
}

// answer replies to what agy asks of the client, as today's agy lanes do: a
// permission request is allowed (the tool-mode hook decides what may run; a
// Full CLI launch has no prompts at all), anything else is refused.
func (s *agyACPSession) answer(msg agyACPMessage) {
	if msg.Method != "session/request_permission" {
		_ = s.rpc.write(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32601, "message": "not supported"}})
		return
	}
	var p struct {
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	_ = json.Unmarshal(msg.Params, &p)
	choice := ""
	for _, kind := range []string{"allow_once", "allow_always"} {
		for _, option := range p.Options {
			if choice == "" && option.Kind == kind {
				choice = option.OptionID
			}
		}
	}
	if choice == "" {
		_ = s.rpc.reply(msg.ID, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}})
		return
	}
	_ = s.rpc.reply(msg.ID, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": choice}})
}

func (s *agyACPSession) listen() <-chan agyACPMessage {
	ch := make(chan agyACPMessage, 4096)
	s.mu.Lock()
	s.listener = ch
	s.mu.Unlock()
	return ch
}

func (s *agyACPSession) stopListening() {
	s.mu.Lock()
	s.listener = nil
	s.mu.Unlock()
}

// generateContentACP runs one chat turn on the chat's `agy acp` session.
func (a *AgyCLIAdapter) generateContentACP(ctx context.Context, messages []llmtypes.MessageContent, opts *llmtypes.CallOptions) (*llmtypes.ContentResponse, error) {
	if err := llmtypes.ValidateCLISecurityLaunch(opts, llmtypes.ConfinedModes(opts)...); err != nil {
		return nil, err
	}
	emit := func(chunk llmtypes.StreamChunk) {
		if opts.StreamChan != nil {
			select {
			case opts.StreamChan <- chunk:
			case <-ctx.Done():
			}
		}
	}
	owner := agyStringMetadata(opts, MetadataKeyInteractiveSessionID)
	if owner == "" {
		return nil, fmt.Errorf("agy ACP turn needs an owner session id (WithInteractiveSessionID)")
	}
	workdir := agyStringMetadata(opts, MetadataKeyWorkingDir)
	if workdir == "" {
		var err error
		if workdir, err = os.Getwd(); err != nil {
			return nil, fmt.Errorf("resolve agy working directory: %w", err)
		}
	}
	model := strings.TrimSpace(a.modelID)
	if strings.TrimSpace(opts.Model) != "" {
		model = strings.TrimSpace(opts.Model)
	}
	if model == "" {
		model = DefaultModelID
	}
	effort, err := agyExecEffort(opts)
	if err != nil {
		return nil, err
	}
	modelOption, thought := agyACPModel(model, effort)
	system := strings.TrimSpace(llmtypes.CodingProviderLaunchSystemPromptFromOptions(opts))
	var systems []string
	if system != "" {
		systems = append(systems, system)
	}
	human := ""
	for _, m := range messages {
		var parts []string
		for _, part := range m.Parts {
			if text, ok := part.(llmtypes.TextContent); ok {
				parts = append(parts, text.Text)
			}
		}
		switch m.Role {
		case llmtypes.ChatMessageTypeSystem:
			if t := strings.TrimSpace(strings.Join(parts, "")); t != "" {
				systems = append(systems, t)
			}
		}
	}
	var pending []string
	for _, message := range llmtypes.PendingHumanMessages(messages) {
		var parts []string
		for _, part := range message.Parts {
			switch v := part.(type) {
			case llmtypes.TextContent:
				parts = append(parts, v.Text)
			case *llmtypes.TextContent:
				if v != nil {
					parts = append(parts, v.Text)
				}
			}
		}
		pending = append(pending, strings.Join(parts, ""))
	}
	human = strings.Join(pending, "\n\n")
	system = strings.Join(systems, "\n\n")
	resume := agyStringMetadata(opts, MetadataKeyResumeSessionID)
	fingerprint := strings.Join([]string{workdir, agyStringMetadata(opts, MetadataKeyMCPConfig), agyStringMetadata(opts, MetadataKeyNativeToolsMode), system}, "\x00")
	parts := llmtypes.AgentProcessDigests(map[string]any{"working_dir": workdir, "mcp_config": agyStringMetadata(opts, MetadataKeyMCPConfig), "native_tools_mode": agyStringMetadata(opts, MetadataKeyNativeToolsMode), "instructions": system})

	agyACPSessions.Lock()
	s := agyACPSessions.m[owner]
	agyACPSessions.Unlock()
	launchReason := "first"
	if s != nil {
		select {
		case <-s.rpc.done:
			CloseAgyACPSession(owner)
			s = nil
			launchReason = "process_exited"
		default:
			if s.fingerprint != fingerprint || (resume != "" && resume != s.sessionID) {
				launchReason = "other_conversation"
				if s.fingerprint != fingerprint {
					launchReason = "launch_changed:" + llmtypes.AgentProcessChanged(s.parts, parts)
				}
				CloseAgyACPSession(owner)
				s = nil
			} else {
				s.rpc.process.Reused(nil, "launch_unchanged")
			}
		}
	}
	if s == nil {
		if s, err = a.launchAgyACP(ctx, opts, workdir, system, resume); err != nil {
			return nil, err
		}
		s.rpc.process.Launched(nil, launchReason)
		s.fingerprint = fingerprint
		s.parts = parts
		agyACPSessions.Lock()
		agyACPSessions.m[owner] = s
		agyACPSessions.Unlock()
	}
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	if err := s.applySelection(ctx, model, modelOption, thought); err != nil {
		return nil, err
	}
	if llmtypes.CodingProviderLaunchOnlyFromOptions(opts) {
		return agyACPResponse(s, "", nil), nil
	}
	if strings.TrimSpace(human) == "" && !agyHasPendingImages(ctx, owner, messages) {
		return nil, fmt.Errorf("agy prompt is empty")
	}
	return a.consumeAgyACPTurn(ctx, s, human, emit, agyImageTurn{owner: owner, messages: messages})
}

type agyACPTool struct {
	name, args, result string
	started            time.Time
	open, ended        bool
}

func (a *AgyCLIAdapter) consumeAgyACPTurn(ctx context.Context, s *agyACPSession, prompt string, emit func(llmtypes.StreamChunk), images ...agyImageTurn) (response *llmtypes.ContentResponse, turnErr error) {
	defer func() {
		if turnErr != nil && !errors.Is(turnErr, context.Canceled) {
			log.Printf("[AGY_ACP] session=%s action=failed reason=turn_failed last_error=%q", s.rpc.owner, s.rpc.stderrLine())
		}
	}()
	meta := map[string]any{"agy_session_id": s.sessionID, "agy_stream_source": "acp"}
	events := s.listen()
	defer s.stopListening()
	var result struct {
		StopReason string `json:"stopReason"`
		Usage      *struct {
			Input       int `json:"inputTokens"`
			Output      int `json:"outputTokens"`
			CachedRead  int `json:"cachedReadTokens"`
			CachedWrite int `json:"cachedWriteTokens"`
			Thought     int `json:"thoughtTokens"`
			Total       int `json:"totalTokens"`
		} `json:"usage"`
	}
	imageTurn := agyImageTurn{owner: s.sessionID}
	if len(images) > 0 {
		imageTurn = images[0]
	}
	input := []any{}
	if prompt != "" {
		input = append(input, map[string]any{"type": "text", "text": prompt})
	}
	params, cleanup, prepErr := agyParamsWithImages(ctx, imageTurn.owner, map[string]any{"sessionId": s.sessionID, "prompt": input}, imageTurn.messages, s.imagesAllowed)
	if prepErr != nil {
		return nil, prepErr
	}
	defer cleanup()
	outcome := beginAgyACPOutcome(s.transcriptHome, s.sessionID)
	complete := make(chan error, 1)
	go func() {
		// The turn's own lifetime, not ctx: on Stop it is cancelled and waits for agy's answer.
		complete <- s.rpc.call(context.Background(), "session/prompt", params, &result)
	}()
	if image, ok := params.(*agyImageParams); ok {
		if writeErr := <-image.wrote; writeErr != nil {
			return nil, writeErr
		}
	}

	emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypePromptAccepted, Metadata: meta})
	var text strings.Builder
	tools := map[string]*agyACPTool{}
	handle := func(msg agyACPMessage) {
		if len(msg.ID) > 0 && msg.Method != "" {
			s.answer(msg)
			return
		}
		if msg.Method != "session/update" {
			return
		}
		var envelope struct {
			SessionID string `json:"sessionId"`
			Update    struct {
				Kind       string          `json:"sessionUpdate"`
				Content    json.RawMessage `json:"content"`
				ToolCallID string          `json:"toolCallId"`
				Title      string          `json:"title"`
				Status     string          `json:"status"`
				RawInput   json.RawMessage `json:"rawInput"`
				Used       *int            `json:"used"`
				Size       *int            `json:"size"`
				Entries    json.RawMessage `json:"entries"`
				RawOutput  json.RawMessage `json:"rawOutput"`
			} `json:"update"`
		}
		if json.Unmarshal(msg.Params, &envelope) != nil || envelope.SessionID != s.sessionID {
			return
		}
		u := envelope.Update
		switch u.Kind {
		case "agent_message_chunk", "agent_thought_chunk":
			var content struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(u.Content, &content) != nil || content.Type != "text" || content.Text == "" {
				return
			}
			kind := llmtypes.StreamChunkTypeContent
			if u.Kind == "agent_thought_chunk" {
				kind = llmtypes.StreamChunkTypeReasoning
			} else {
				text.WriteString(content.Text)
			}
			emit(llmtypes.StreamChunk{Type: kind, Content: content.Text, Metadata: map[string]any{"agy_session_id": s.sessionID, "agy_stream_source": "acp", llmtypes.ContentDeltaMetadataKey: true}})
		case "usage_update":
			if u.Used != nil && *u.Used >= 0 {
				status := &llmtypes.StatusLine{Provider: "agy-cli", Model: s.model, Metadata: map[string]interface{}{"agy_session_id": s.sessionID}}
				size := 0
				if u.Size != nil && *u.Size > 0 {
					size = *u.Size
				}
				status.SetContextUsage(*u.Used, size)
				if *u.Used == 0 {
					status.Metadata[llmtypes.ContextUsedTokensMetaKey] = 0
					if size > 0 {
						status.Metadata[llmtypes.ContextWindowTokensMetaKey] = size
					}
				}
				s.mu.Lock()
				s.status = status
				s.mu.Unlock()
				emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeStatusLine, StatusLine: status, Metadata: meta})
				log.Printf("[AGY_CONTEXT] session=%s used=%d size=%d", s.sessionID, *u.Used, size)
			}
		case "plan":
			raw, _ := json.Marshal(map[string]json.RawMessage{"entries": u.Entries})
			if plan, ok := llmtypes.ParseCodingAgentPlan(raw, "entries"); ok {
				plan.Provider, plan.PlanID = "agy-cli", s.sessionID
				emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeCodingAgentPlan, CodingAgentPlan: plan, Metadata: meta})
			}
		case "tool_call", "tool_call_update":
			t := tools[u.ToolCallID]
			if t == nil {
				t = &agyACPTool{started: time.Now()}
				tools[u.ToolCallID] = t
			}
			if u.Title != "" {
				t.name = u.Title
			}
			if len(u.RawInput) > 0 && string(u.RawInput) != "{}" && string(u.RawInput) != "null" {
				t.args = string(u.RawInput)
			}
			if !t.open {
				t.open = true
				if t.args == "" {
					t.args = "{}"
				}
				emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallStart, ToolCallID: u.ToolCallID, ToolName: t.name, ToolArgs: t.args, Metadata: meta})
			}
			if len(u.RawOutput) > 0 && string(u.RawOutput) != "null" {
				t.result = string(u.RawOutput)
			} else if len(u.Content) > 0 && string(u.Content) != "null" {
				t.result = agyACPContentText(u.Content)
			}
			if !t.ended && (u.Status == "completed" || u.Status == "failed") {
				t.ended = true
				emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallEnd, ToolCallID: u.ToolCallID, ToolName: t.name, ToolArgs: t.args, ToolResult: t.result, ToolDuration: time.Since(t.started), Metadata: meta})
			}
		}
	}
	cancelled := false
	for {
		select {
		case msg := <-events:
			handle(msg)
		case err := <-complete:
			for len(events) > 0 {
				handle(<-events)
			}
			if cancelled || result.StopReason == "cancelled" {
				return nil, context.Canceled
			}
			if err != nil {
				return nil, agyACPRPCFailure(s.model, s.sessionID, err)
			}
			if failure := outcome.failure(s.model, result.StopReason); failure != nil {
				return nil, failure
			}
			var usage *llmtypes.Usage
			if u := result.Usage; u != nil {
				cached := u.CachedRead
				reasoning := u.Thought
				usage = &llmtypes.Usage{InputTokens: u.Input, OutputTokens: u.Output, TotalTokens: u.Total, CacheTokens: &cached, ReasoningTokens: &reasoning}
			}
			if strings.TrimSpace(text.String()) == "" && len(tools) == 0 {
				return nil, fmt.Errorf("agy returned no answer (stop reason: %s)", result.StopReason)
			}
			return agyACPResponse(s, text.String(), usage), nil
		case <-ctx.Done():
			if !cancelled {
				// agy's cancel waits for the prompt to come back as cancelled.
				cancelled = true
				_ = s.rpc.notify("session/cancel", map[string]any{"sessionId": s.sessionID})
				go func() {
					select {
					case <-time.After(30 * time.Second):
						s.rpc.finish(errors.New("agy did not stop within 30s"))
					case <-s.rpc.done:
					}
				}()
			}
			ctx = context.Background()
		case <-s.rpc.done:
			return nil, s.rpc.failure()
		}
	}
}

func agyACPContentText(raw json.RawMessage) string {
	var blocks []struct {
		Type    string `json:"type"`
		Content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var out []string
		for _, b := range blocks {
			if b.Content.Text != "" {
				out = append(out, b.Content.Text)
			}
		}
		if len(out) > 0 {
			return strings.Join(out, "\n")
		}
	}
	return string(raw)
}

func agyACPResponse(s *agyACPSession, text string, usage *llmtypes.Usage) *llmtypes.ContentResponse {
	gi := &llmtypes.GenerationInfo{Additional: map[string]interface{}{"provider": "agy-cli", "agy_cli_mode": "acp", "agy_session_id": s.sessionID}}
	if usage != nil {
		prompt := usage.InputTokens
		gi.PromptTokens, gi.CompletionTokens, gi.TotalTokens = &prompt, &usage.OutputTokens, &usage.TotalTokens
		gi.CachedContentTokens = usage.CacheTokens
		gi.ReasoningTokens = usage.ReasoningTokens
	}
	s.mu.Lock()
	if s.status != nil {
		for _, key := range []string{llmtypes.ContextUsedTokensMetaKey, llmtypes.ContextWindowTokensMetaKey} {
			if value, ok := s.status.Metadata[key]; ok {
				gi.Additional[key] = value
			}
		}
	}
	s.mu.Unlock()
	llmtypes.AttachCodingProviderSessionHandle(gi, llmtypes.CodingProviderSessionHandle{
		Provider: "agy-cli", Transport: llmtypes.CodingProviderTransportACP, NativeSessionID: s.sessionID,
		WorkingDir: s.workdir, Model: s.model, Status: llmtypes.CodingProviderSessionStatusIdle,
	})
	return &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{Content: text, StopReason: "stop", GenerationInfo: gi}}, Usage: usage}
}

func closeAllAgyACPSessions() {
	agyACPSessions.Lock()
	owners := make([]string, 0, len(agyACPSessions.m))
	for owner := range agyACPSessions.m {
		owners = append(owners, owner)
	}
	agyACPSessions.Unlock()
	for _, owner := range owners {
		CloseAgyACPSession(owner)
	}
}

// agyLogLaunch records what an agy launch gets, names only: its HOME, whether a Gemini key is in its environment, and
// the mode of the settings that select key mode (a slot's agy kept asking for a Google login, PLAT-872-86).
func agyLogLaunch(env []string) {
	key, home := false, ""
	for _, entry := range env {
		if strings.HasPrefix(entry, "HOME=") {
			home = strings.TrimPrefix(entry, "HOME=")
		}
		if strings.HasPrefix(entry, "GEMINI_API_KEY=") && len(entry) > len("GEMINI_API_KEY=") {
			key = true
		}
	}
	settings := "missing"
	if info, err := os.Stat(agySettingsPath(home)); err == nil {
		settings = info.Mode().String()
	}
	log.Printf("[AGY_LAUNCH] home=%s gemini_key=%t settings=%s", home, key, settings)
}

// agyPinHome makes HOME the run's own agy home again after the account merge: a confined CLI's sandbox home replaces
// HOME there, so agy ran in the plain sandbox home without its per-run settings (modelProvider gemini, MCP catalogue,
// hooks) and asked for a Google login ("Authentication required", PLAT-872-86).
func agyPinHome(env []string, home string) []string {
	if home == "" {
		return env
	}
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, "HOME=") {
			out = append(out, entry)
		}
	}
	return append(out, "HOME="+home)
}

func (s *agyACPSession) rememberConfigOptions(options []agyACPConfigOption) {
	if options == nil {
		return
	}
	s.mu.Lock()
	s.configOptions = append([]agyACPConfigOption{}, options...)
	s.mu.Unlock()
}

// Called under turnMu: model selection is retained, but thinking must be
// reasserted on every turn, including unchanged-model turns and resumes.
func (s *agyACPSession) applySelection(ctx context.Context, model, modelOption, thought string) error {
	if s.model != model {
		var result agyACPConfigResult
		if err := s.rpc.call(ctx, "session/set_config_option", map[string]any{"sessionId": s.sessionID, "configId": "model", "value": modelOption}, &result); err != nil {
			return agyACPRPCFailure(model, s.sessionID, err)
		}
		s.rememberConfigOptions(result.ConfigOptions)
		s.model = model
	}
	if thought == "" {
		return nil
	}
	s.mu.Lock()
	id := ""
	if s.configOptions == nil {
		id = "thought_level"
	}
	for _, option := range s.configOptions {
		if option.Category == "thought_level" && option.Type == "select" {
			id = option.ID
			break
		}
	}
	s.mu.Unlock()
	if id == "" {
		log.Printf("[AGY_CONFIG] action=refused lane=acp reason=model_has_no_thought_level thought_level_unavailable=true level=%s model=%s", thought, model)
		return nil
	}
	var result agyACPConfigResult
	if err := s.rpc.call(ctx, "session/set_config_option", map[string]any{"sessionId": s.sessionID, "configId": id, "value": thought}, &result); err != nil {
		// ACP errors may echo inputs: log only the failure category and numeric code.
		code := 0
		var rpcError *agyACPError
		if errors.As(err, &rpcError) {
			code = rpcError.Code
		}
		log.Printf("[AGY_CONFIG] action=refused lane=acp thought_level_set_failed=true code=%d level=%s model=%s", code, thought, model)
	} else {
		s.rememberConfigOptions(result.ConfigOptions)
		log.Printf("[AGY_CONFIG] action=applied lane=acp level=%s model=%s", thought, model)
	}
	return nil
}
