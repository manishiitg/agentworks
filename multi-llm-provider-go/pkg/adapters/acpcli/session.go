package acpcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/procshutdown"
)

// Model is one model an agent offers.
type Model struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

type configOption struct {
	ID           string          `json:"id"`
	Category     string          `json:"category"`
	Type         string          `json:"type"`
	CurrentValue string          `json:"currentValue"`
	Options      json.RawMessage `json:"options"`
}

type sessionSetup struct {
	SessionID string `json:"sessionId"`
	Models    *struct {
		CurrentModelID  string `json:"currentModelId"`
		AvailableModels []struct {
			ModelID     string `json:"modelId"`
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"availableModels"`
	} `json:"models"`
	ConfigOptions []configOption `json:"configOptions"`
	Modes         *struct {
		CurrentModeID  string `json:"currentModeId"`
		AvailableModes []struct {
			ID string `json:"id"`
		} `json:"availableModes"`
	} `json:"modes"`
}

type agentCapabilities struct {
	LoadSession        bool `json:"loadSession"`
	PromptCapabilities struct {
		Image           bool `json:"image"`
		EmbeddedContext bool `json:"embeddedContext"`
	} `json:"promptCapabilities"`
	MCPCapabilities struct {
		HTTP bool `json:"http"`
		SSE  bool `json:"sse"`
	} `json:"mcpCapabilities"`
	SessionCapabilities struct {
		Close *json.RawMessage `json:"close"`
	} `json:"sessionCapabilities"`
}

type session struct {
	agent       Agent
	rpc         *conn
	env         []string
	workdir     string
	autoApprove bool
	// questionPrefix marks permission requests that are the agent's questions to the person.
	questionPrefix string
	fullTools      bool
	caps           agentCapabilities
	authMethods    []string
	outcome        Outcome
	// steersWithPrompt: this agent's release takes a prompt sent mid-turn as a message into the running turn.
	steersWithPrompt bool

	turnMu        sync.Mutex
	mu            sync.Mutex
	sessionID     string
	model         string // the agent's current model id
	chatModel     string // the model id this turn was asked for
	models        []Model
	setModel      bool // the agent offers session/set_model
	setCurrent    string
	setModels     []Model
	modelConfig   string // the config option that selects the model
	configCurrent string
	configModels  []Model
	options       []configOption
	modes         []string // the agent's session modes (session/set_mode), when it offers them
	mode          string   // the session's current mode
	status        *llmtypes.StatusLine
	preamble      string // instructions sent ahead of the first prompt
	fingerprint   string
	parts         map[string][32]byte
	listener      chan message
	release       func()
	// steer takes messages for the running turn (nil when none takes them); steerDone closes when that turn ends.
	steer     chan steerRequest
	steerDone chan struct{}
}

var sessions = struct {
	sync.Mutex
	m map[string]*session
}{m: map[string]*session{}}

var agentModels = struct {
	sync.Mutex
	m map[string][]Model
}{m: map[string][]Model{}}

// AgentModels returns the models the agent offered in its most recent
// session on this server (nil before the agent has run once).
func AgentModels(agentID string) []Model {
	agentModels.Lock()
	defer agentModels.Unlock()
	return append([]Model(nil), agentModels.m[strings.ToLower(agentID)]...)
}

// CloseSession ends a chat's agent process.
func CloseSession(owner string) {
	sessions.Lock()
	s := sessions.m[owner]
	delete(sessions.m, owner)
	sessions.Unlock()
	if s != nil {
		s.closeNow()
	}
}

// CloseAllSessions ends every chat's agent process (server shutdown).
func CloseAllSessions() {
	sessions.Lock()
	owners := make([]string, 0, len(sessions.m))
	for owner := range sessions.m {
		owners = append(owners, owner)
	}
	sessions.Unlock()
	for _, owner := range owners {
		CloseSession(owner)
	}
}

// SessionActive reports whether a chat has a running agent process.
func SessionActive(owner string) bool {
	sessions.Lock()
	defer sessions.Unlock()
	return sessions.m[owner] != nil
}

// AgentSessionActive reports whether a chat runs this agent now.
func AgentSessionActive(owner, agentID string) bool {
	sessions.Lock()
	defer sessions.Unlock()
	s := sessions.m[owner]
	return s != nil && s.agent.ID == agentID
}

// CloseAgentSession ends a chat's process when it runs this agent.
func CloseAgentSession(owner, agentID string) {
	if AgentSessionActive(owner, agentID) {
		CloseSession(owner)
	}
}

// CloseAgentSessions ends every chat's process that runs this agent.
func CloseAgentSessions(agentID string) {
	sessions.Lock()
	var owners []string
	for owner, s := range sessions.m {
		if s.agent.ID == agentID {
			owners = append(owners, owner)
		}
	}
	sessions.Unlock()
	for _, owner := range owners {
		CloseAgentSession(owner, agentID)
	}
}

func (s *session) closeNow() {
	// An agent that can close sessions closes this one first, so its
	// conversation store has no writer when the process stops.
	if sessionID := s.currentSessionID(); sessionID != "" && s.caps.SessionCapabilities.Close != nil {
		select {
		case <-s.rpc.done:
		default:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.rpc.call(ctx, "session/close", map[string]any{"sessionId": sessionID}, nil)
			cancel()
		}
	}
	s.rpc.close()
	if s.release != nil {
		s.release()
	}
}

// toolMode validates the chat's native tools setting. "full" needs a confined
// launch, exactly like the other coding agents.
func toolMode(opts *llmtypes.CallOptions) (string, error) {
	mode := strings.ToLower(stringMetadata(opts, MetadataKeyNativeToolsMode))
	switch mode {
	case "", "mcp_only":
	case "full":
		if opts == nil || !opts.CLISecurity.Confined() {
			return "", fmt.Errorf("ACP full native tools require a confined launch (Landlock on Linux, Seatbelt on a Mac)")
		}
	default:
		return "", fmt.Errorf("ACP native tools mode %q: want mcp_only or full", mode)
	}
	return mode, nil
}

// startAgent spawns the agent's ACP server; tests replace it with in-process pipes.
var startAgent = startAgentProcess

func startAgentProcess(agent Agent, opts *llmtypes.CallOptions, owner, workdir string, fullTools bool, servers []mcpServer, spec *LaunchSpec) (*conn, []string, error) {
	life, stop := context.WithCancel(context.Background())
	args := agent.launchArgs(fullTools)
	if spec != nil && spec.Args != nil {
		args = spec.Args
	}
	cmd := exec.CommandContext(life, agent.Binary, args...)
	cmd.Dir = workdir
	procshutdown.SetGroup(cmd)
	procshutdown.KillGroupOnCancel(cmd)
	cmd.Env = llmtypes.MergeCodingAgentSecretEnvironment(os.Environ(), opts)
	sandbox := func() {}
	release := func() {}
	if spec != nil {
		if spec.Env != nil {
			cmd.Env = spec.Env
		}
		if spec.Release != nil {
			release = spec.Release
		}
	}
	cleanup := func() { sandbox(); release() }
	if spec != nil && spec.Confine != nil {
		var err error
		if sandbox, err = spec.Confine(cmd); err != nil {
			stop()
			release()
			return nil, nil, err
		}
	} else if opts != nil && opts.CLISecurity.Confined() {
		var err error
		if sandbox, err = clisandbox.LandlockCmd(opts.CLISecurity, cmd, workdir, mcpReadPaths(servers), nil); err != nil {
			stop()
			release()
			return nil, nil, fmt.Errorf("confine %s: %w", agent.Label, err)
		}
	}
	stderr := &clisandbox.StderrTail{}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		stop()
		cleanup()
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stop()
		cleanup()
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		stop()
		cleanup()
		if _, lookErr := exec.LookPath(agent.Binary); lookErr != nil {
			hint := ""
			if agent.InstallCommand != "" {
				hint = "; install it with `" + agent.InstallCommand + "`"
			}
			return nil, nil, fmt.Errorf("%s CLI (%s) is not installed%s", agent.Label, agent.Binary, hint)
		}
		return nil, nil, fmt.Errorf("start %s: %w", agent.Label, err)
	}
	c := newConn(owner, agent.ID, stdin)
	c.cmd, c.stderr, c.stop = cmd, stderr, stop
	go c.read(stdout)
	go func() {
		err := cmd.Wait()
		reason := "process_exited"
		if life.Err() != nil {
			reason = "stopped"
		} else if err != nil {
			log.Printf("[CLI_EXIT] session=%s provider=%s agent=%s transport=acp action=failed exit_code=%d last_error=%q", owner, agent.providerID(), agent.ID, cmd.ProcessState.ExitCode(), stderrLine(stderr.String(), cmd.Env))
		}
		if c.process != nil {
			c.process.Exited(nil, reason)
		}
		c.finish(fmt.Errorf("%s exited: %v", agent.Label, err))
		cleanup()
		close(c.exited)
	}()
	return c, cmd.Env, nil
}

// handshake runs initialize, authentication and session setup on a started
// agent: session/load when resuming an agent that can load sessions,
// otherwise session/new.
func (s *session) handshake(ctx context.Context, resume string, servers []mcpServer, system string) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	init := map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":          map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal":    false,
			"elicitation": map[string]any{"form": map[string]any{}, "url": map[string]any{}},
		},
		"clientInfo": map[string]any{"name": "multi-llm-provider-go", "title": "ACP client", "version": "1"},
	}
	if len(s.agent.InitMeta) > 0 {
		init["_meta"] = s.agent.InitMeta
	}
	var initialized struct {
		ProtocolVersion   int               `json:"protocolVersion"`
		AgentCapabilities agentCapabilities `json:"agentCapabilities"`
		AuthMethods       []struct {
			ID string `json:"id"`
		} `json:"authMethods"`
		AgentInfo struct {
			Version string `json:"version"`
		} `json:"agentInfo"`
	}
	if err := s.rpc.call(ctx, "initialize", init, &initialized); err != nil {
		return s.setupError(err)
	}
	s.caps = initialized.AgentCapabilities
	s.steersWithPrompt = s.agent.SteersWithPrompt != nil && s.agent.SteersWithPrompt(initialized.AgentInfo.Version)
	if s.steersWithPrompt {
		log.Printf("[ACP_CLI] session=%s agent=%s version=%s steers_with_prompt=true", s.rpc.owner, s.agent.ID, initialized.AgentInfo.Version)
	}
	for _, method := range initialized.AuthMethods {
		s.authMethods = append(s.authMethods, method.ID)
	}
	if err := s.authenticate(ctx); err != nil {
		return err
	}
	params := map[string]any{"cwd": s.workdir, "mcpServers": servers}
	if s.agent.SessionMeta != nil && strings.TrimSpace(system) != "" {
		params["_meta"] = s.agent.SessionMeta(system)
	}
	var opened sessionSetup
	switch {
	case resume != "" && s.caps.LoadSession:
		params["sessionId"] = resume
		if err := s.rpc.call(ctx, "session/load", params, &opened); err != nil {
			if mapped := s.setupError(err); llmtypes.IsCodingAgentAuthRequiredError(mapped) || s.agent.StrictResume {
				// A strict agent resumes the chat's own conversation by its
				// exact id, never a fresh one.
				return mapped
			}
			// A conversation the agent no longer has must not block the chat.
			log.Printf("[ACP_CLI] session=%s agent=%s action=new_session reason=load_failed", s.rpc.owner, s.agent.ID)
			delete(params, "sessionId")
			opened = sessionSetup{}
			resume = ""
		} else {
			opened.SessionID = resume
		}
	case resume != "":
		log.Printf("[ACP_CLI] session=%s agent=%s action=new_session reason=load_unsupported", s.rpc.owner, s.agent.ID)
		resume = ""
	}
	if resume == "" {
		if err := s.rpc.call(ctx, "session/new", params, &opened); err != nil {
			return s.setupError(err)
		}
	}
	if opened.SessionID == "" {
		return fmt.Errorf("%s did not return a session id", s.agent.Label)
	}
	s.mu.Lock()
	s.sessionID = opened.SessionID
	s.mu.Unlock()
	s.rememberSetup(opened)
	log.Printf("[ACP_CLI] session=%s agent=%s native_session=%s action=opened resumed=%t mcp_servers=%d load_session=%t models=%d", s.rpc.owner, s.agent.ID, opened.SessionID, resume != "", len(servers), s.caps.LoadSession, len(s.models))
	return s.applyFullToolsConfig(ctx)
}

func (s *session) authenticate(ctx context.Context) error {
	method := s.agent.StoredAuthMethod
	for _, name := range s.agent.APIKeyEnv {
		if envValue(s.env, name) != "" && s.agent.APIKeyAuthMethod != "" {
			method = s.agent.APIKeyAuthMethod
			break
		}
	}
	if method == "" {
		return nil
	}
	if len(s.authMethods) > 0 && !containsString(s.authMethods, method) {
		log.Printf("[ACP_CLI] session=%s agent=%s action=skipped_auth reason=method_not_offered method=%s", s.rpc.owner, s.agent.ID, method)
		return nil
	}
	if err := s.rpc.call(ctx, "authenticate", map[string]any{"methodId": method}, nil); err != nil {
		return s.signInError(err)
	}
	return nil
}

var authRequiredText = regexp.MustCompile(`(?i)auth|sign[ -]?in|log[ -]?in|credential`)

// setupError turns an agent's authentication refusal into a clear sign-in
// error; anything else is returned as is.
func (s *session) setupError(err error) error {
	var rpc *RPCError
	if errors.As(err, &rpc) && (rpc.Code == -32000 || authRequiredText.MatchString(rpc.Message)) {
		return s.signInError(err)
	}
	return err
}

func (s *session) signInError(err error) error {
	code := 0
	var rpc *RPCError
	if errors.As(err, &rpc) {
		code = rpc.Code
	}
	log.Printf("[ACP_CLI] session=%s agent=%s action=failed reason=auth_required code=%d", s.rpc.owner, s.agent.ID, code)
	return fmt.Errorf("Sign in to %s first: %w", s.agent.Label, &llmtypes.CodingAgentAuthRequiredError{Provider: s.agent.Label, LoginCommand: s.agent.LoginCommand})
}

func (s *session) rememberSetup(setup sessionSetup) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if setup.ConfigOptions != nil {
		s.options = append([]configOption(nil), setup.ConfigOptions...)
	}
	if setup.Modes != nil {
		s.mode, s.modes = setup.Modes.CurrentModeID, s.modes[:0]
		for _, m := range setup.Modes.AvailableModes {
			s.modes = append(s.modes, m.ID)
		}
	}
	if setup.Models != nil {
		s.setModel = true
		s.setCurrent = setup.Models.CurrentModelID
		s.setModels = s.setModels[:0]
		for _, m := range setup.Models.AvailableModels {
			s.setModels = append(s.setModels, Model{ID: m.ModelID, Name: m.Name, Description: m.Description})
		}
	}
	for _, option := range s.options {
		if option.Category == "model" || (option.Category == "" && option.ID == "model") {
			s.modelConfig = option.ID
			s.configCurrent = option.CurrentValue
			s.configModels = selectValues(option.Options)
			break
		}
	}
	s.model, s.models = s.configCurrent, s.configModels
	if s.setModel {
		s.model, s.models = s.setCurrent, s.setModels
	}
	if len(s.models) > 0 {
		agentModels.Lock()
		agentModels.m[s.agent.ID] = append([]Model(nil), s.models...)
		agentModels.Unlock()
	}
}

// selectValues reads a select option's values, flat or grouped.
func selectValues(raw json.RawMessage) []Model {
	var entries []struct {
		Value       string          `json:"value"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Options     json.RawMessage `json:"options"`
	}
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	var out []Model
	for _, e := range entries {
		if len(e.Options) > 0 {
			out = append(out, selectValues(e.Options)...)
		} else if e.Value != "" {
			out = append(out, Model{ID: e.Value, Name: e.Name, Description: e.Description})
		}
	}
	return out
}

// selectModel switches the session to the agent's model id; "" keeps the
// agent's current model. An agent may list models for session/set_model and
// as a "model" config option (Antigravity lists "<model>-<level>" ids for
// one and base ids for the other): the list that has the id is used. Called
// under turnMu.
func (s *session) selectModel(ctx context.Context, model string) error {
	s.mu.Lock()
	current, offersSet, setModels, configID, configModels := s.model, s.setModel, s.setModels, s.modelConfig, s.configModels
	sessionID, label := s.sessionID, s.handleModel()
	s.mu.Unlock()
	if model == "" || model == current {
		return nil
	}
	if configID == "" {
		configID = s.agent.ModelConfigID
	}
	has := func(models []Model) bool {
		for _, m := range models {
			if m.ID == model {
				return true
			}
		}
		return false
	}
	viaSetModel := false
	switch {
	case has(setModels):
		viaSetModel = true
	case has(configModels):
	case len(setModels) == 0 && len(configModels) == 0:
		// Lists unknown (a loaded session may not repeat them).
		viaSetModel = offersSet
		if !viaSetModel && configID == "" {
			return fmt.Errorf("%s does not let clients choose a model; use %q for its default", s.agent.Label, s.agent.ID)
		}
	default:
		log.Printf("[CLI_MODEL_ERROR] provider=%s agent=%s session=%s kind=model_not_found", s.agent.providerID(), s.agent.ID, sessionID)
		return &llmerrors.Error{Provider: s.agent.providerID(), Model: label, Kind: llmerrors.KindModelNotFound,
			Err: fmt.Errorf("%s does not offer model %q; pick another model.", s.agent.Label, model)}
	}
	var err error
	if viaSetModel {
		err = s.rpc.call(ctx, "session/set_model", map[string]any{"sessionId": sessionID, "modelId": model}, nil)
	} else {
		var result sessionSetup
		err = s.rpc.call(ctx, "session/set_config_option", map[string]any{"sessionId": sessionID, "configId": configID, "value": model}, &result)
		if err == nil && result.ConfigOptions != nil {
			s.mu.Lock()
			s.options = result.ConfigOptions
			s.mu.Unlock()
		}
	}
	if err != nil {
		if s.outcome != nil {
			return s.outcome.RPCFailure(label, sessionID, err)
		}
		return err
	}
	s.mu.Lock()
	s.model = model
	s.mu.Unlock()
	log.Printf("[ACP_CLI] session=%s agent=%s action=model_selected via_set_model=%t", s.rpc.owner, s.agent.ID, viaSetModel)
	return nil
}

// applyThought sets the agent's thought level for this turn when it offers a
// thought_level config option. It is reasserted every turn (an unchanged
// model may still change its level); a refusal is logged, never fatal.
// Called under turnMu.
func (s *session) applyThought(ctx context.Context, level string) {
	if level == "" {
		return
	}
	s.mu.Lock()
	id := ""
	if s.options == nil {
		id = s.agent.ThoughtConfigID
	}
	for _, option := range s.options {
		if option.Category == "thought_level" && option.Type == "select" {
			id = option.ID
			break
		}
	}
	sessionID := s.sessionID
	s.mu.Unlock()
	if id == "" {
		log.Printf("[ACP_CONFIG] session=%s agent=%s action=skipped reason=no_thought_level level=%s", s.rpc.owner, s.agent.ID, level)
		return
	}
	var result sessionSetup
	if err := s.rpc.call(ctx, "session/set_config_option", map[string]any{"sessionId": sessionID, "configId": id, "value": level}, &result); err != nil {
		// Agent errors may echo inputs: log only the numeric code.
		code := 0
		var rpc *RPCError
		if errors.As(err, &rpc) {
			code = rpc.Code
		}
		log.Printf("[ACP_CONFIG] session=%s agent=%s action=refused reason=thought_level_set_failed code=%d level=%s", s.rpc.owner, s.agent.ID, code, level)
		return
	}
	if result.ConfigOptions != nil {
		s.mu.Lock()
		s.options = result.ConfigOptions
		s.mu.Unlock()
	}
	log.Printf("[ACP_CONFIG] session=%s agent=%s action=applied level=%s", s.rpc.owner, s.agent.ID, level)
}

// handleModel is the model id the chat sees (called under s.mu).
func (s *session) handleModel() string {
	if s.agent.Provider != "" && s.chatModel != "" {
		return s.chatModel
	}
	return s.agent.ID + "/" + s.model
}

func (s *session) applyFullToolsConfig(ctx context.Context) error {
	toggle := s.agent.FullToolsConfig
	if toggle == nil {
		return nil
	}
	s.mu.Lock()
	offered := false
	for _, option := range s.options {
		offered = offered || option.ID == toggle.ID
	}
	sessionID := s.sessionID
	s.mu.Unlock()
	if !offered {
		return nil
	}
	value := toggle.Off
	if s.fullTools {
		value = toggle.On
	}
	return s.rpc.call(ctx, "session/set_config_option", map[string]any{"sessionId": sessionID, "configId": toggle.ID, "value": value}, nil)
}

func envValue(env []string, name string) string {
	value := ""
	for _, entry := range env {
		if key, v, ok := strings.Cut(entry, "="); ok && key == name {
			value = v
		}
	}
	return strings.TrimSpace(value)
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

var diagnosticBase64 = regexp.MustCompile(`[A-Za-z0-9+/=]{64,}`)
var diagnosticAssignment = regexp.MustCompile(`(?i)(?:authorization|api[_-]?key|token|secret|password)\s*[:=]\s*[^,;\s]+`)

// stderrLine is the agent's last stderr line with secret-looking values
// omitted, cut to 200 characters (sanitized before the cut).
func stderrLine(tail string, environment []string) string {
	last := ""
	for _, line := range strings.Split(tail, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			last = line
		}
	}
	for _, entry := range environment {
		name, value, _ := strings.Cut(entry, "=")
		name = strings.ToUpper(name)
		if value != "" && (strings.Contains(name, "KEY") || strings.Contains(name, "TOKEN") || strings.Contains(name, "SECRET") || strings.Contains(name, "PASSWORD")) {
			last = strings.ReplaceAll(last, value, "[omitted]")
		}
	}
	last = diagnosticAssignment.ReplaceAllString(last, "[omitted]")
	last = diagnosticBase64.ReplaceAllString(last, "[omitted]")
	runes := []rune(strings.ToValidUTF8(last, ""))
	if len(runes) > 200 {
		runes = runes[:200]
	}
	return string(runes)
}

func (c *conn) stderrLine() string {
	if c.stderr == nil {
		return ""
	}
	var environment []string
	if c.cmd != nil {
		environment = c.cmd.Env
	}
	return stderrLine(c.stderr.String(), environment)
}

// planTurn runs this one turn in the agent's own plan mode (/plan or the Plan
// switch) and returns how to go back to the chat's mode after it. An agent
// without a plan mode refuses the turn rather than running it unplanned.
// Called under turnMu.
func (s *session) planTurn(ctx context.Context) (func(), error) {
	s.mu.Lock()
	// A loaded session may not repeat its modes: then set_mode itself answers.
	sessionID, current, offered, known := s.sessionID, s.mode, false, len(s.modes) > 0
	for _, m := range s.modes {
		offered = offered || m == "plan"
	}
	s.mu.Unlock()
	if !offered && known {
		return nil, llmtypes.NativePlanUnavailable(s.agent.Label)
	}
	if current == "" {
		current = "default"
	}
	if err := s.rpc.call(ctx, "session/set_mode", map[string]any{"sessionId": sessionID, "modeId": "plan"}, nil); err != nil {
		return nil, err
	}
	log.Printf("[PLAN_MODE] provider=%s session=%s action=plan_turn", s.agent.providerID(), s.rpc.owner)
	return func() {
		back, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.rpc.call(back, "session/set_mode", map[string]any{"sessionId": sessionID, "modeId": current}, nil); err != nil {
			log.Printf("[PLAN_MODE] provider=%s session=%s action=restore_failed err=%v", s.agent.providerID(), s.rpc.owner, err)
		}
	}, nil
}
