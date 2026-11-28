package acpcli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/instructions"
)

// Adapter runs chat turns on an ACP agent. The model id names the agent.
type Adapter struct {
	modelID string
	logger  interfaces.Logger
}

// NewAdapter builds an acp-cli adapter for "<agentID>[/<model>]".
func NewAdapter(modelID string, logger interfaces.Logger) (*Adapter, error) {
	agentID, _ := SplitModelID(modelID)
	if _, ok := LookupAgent(agentID); !ok {
		return nil, fmt.Errorf("unknown ACP agent %q in model %q (want one of %s)", agentID, modelID, strings.Join(agentIDs(), ", "))
	}
	return &Adapter{modelID: strings.TrimSpace(modelID), logger: logger}, nil
}

func agentIDs() []string {
	var ids []string
	for _, agent := range Agents() {
		ids = append(ids, agent.ID)
	}
	return ids
}

// GetModelID returns the configured "<agentID>[/<model>]".
func (a *Adapter) GetModelID() string { return a.modelID }

// GetModelMetadata describes an agent model; prices are the agent's own.
func (a *Adapter) GetModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	if strings.TrimSpace(modelID) == "" {
		modelID = a.modelID
	}
	agentID, model := SplitModelID(modelID)
	agent, ok := LookupAgent(agentID)
	if !ok {
		return nil, fmt.Errorf("unknown ACP agent %q", agentID)
	}
	name := agent.Label
	if model != "" {
		name += " " + model
		for _, m := range AgentModels(agentID) {
			if m.ID == model && m.Name != "" {
				name = agent.Label + " " + m.Name
			}
		}
	}
	return &llmtypes.ModelMetadata{Provider: ProviderID, ModelID: modelID, ModelName: name, ModelSelectionMode: "dynamic", SupportsToolCalls: true}, nil
}

// GenerateContent runs one turn on the chat's agent process, launching it
// (or relaunching it when the launch changed) as needed.
func (a *Adapter) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	opts := &llmtypes.CallOptions{}
	for _, opt := range options {
		opt(opts)
	}
	modelID := a.modelID
	if strings.TrimSpace(opts.Model) != "" {
		modelID = strings.TrimSpace(opts.Model)
	}
	agentID, model := SplitModelID(modelID)
	agent, ok := LookupAgent(agentID)
	if !ok {
		return nil, fmt.Errorf("unknown ACP agent %q", agentID)
	}
	return generate(ctx, agent, model, messages, opts)
}

// Generate runs one turn for a provider with its own id that runs a
// registered agent (Agent.Provider); model is that provider's model id. opts carries this package's
// metadata keys (WithWorkingDir, WithInteractiveSessionID, ...).
func Generate(ctx context.Context, agentID, model string, messages []llmtypes.MessageContent, opts *llmtypes.CallOptions) (*llmtypes.ContentResponse, error) {
	agent, ok := lookupAny(agentID)
	if !ok {
		return nil, fmt.Errorf("unknown ACP agent %q", agentID)
	}
	if opts == nil {
		opts = &llmtypes.CallOptions{}
	}
	return generate(ctx, agent, strings.TrimSpace(model), messages, opts)
}

func generate(ctx context.Context, agent Agent, model string, messages []llmtypes.MessageContent, opts *llmtypes.CallOptions) (*llmtypes.ContentResponse, error) {
	if err := llmtypes.ValidateCLISecurityLaunch(opts, llmtypes.ConfinedModes(opts)...); err != nil {
		return nil, err
	}
	owner := stringMetadata(opts, MetadataKeyInteractiveSessionID)
	if owner == "" {
		return nil, fmt.Errorf("ACP turn needs an owner session id (WithInteractiveSessionID)")
	}
	chatModel, thought := model, strings.ToLower(strings.TrimSpace(opts.ReasoningEffort))
	model, thought = agent.splitModel(model, thought)
	workdir := stringMetadata(opts, MetadataKeyWorkingDir)
	if workdir == "" {
		var err error
		if workdir, err = os.Getwd(); err != nil {
			return nil, fmt.Errorf("resolve ACP working directory: %w", err)
		}
	}
	mode, err := toolMode(opts)
	if err != nil {
		return nil, err
	}
	system, prompt := foldMessages(messages, opts)
	mcpJSON := stringMetadata(opts, MetadataKeyMCPConfig)
	resume := stringMetadata(opts, MetadataKeyResumeSessionID)
	instructionsEmpty := strings.TrimSpace(system) == ""
	// Instruction changes are delivered in the turn message via instructions.Tracker
	// instead of restarting the ACP process and wiping the prompt cache (#4560).
	config := map[string]any{"agent": agent.ID, "working_dir": workdir, "mcp_config": mcpJSON, "native_tools_mode": mode, "instructions_empty": instructionsEmpty}
	fingerprint := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%t", agent.ID, workdir, mcpJSON, mode, instructionsEmpty)
	parts := llmtypes.AgentProcessDigests(config)

	sessions.Lock()
	s := sessions.m[owner]
	sessions.Unlock()
	launchReason := "first"
	if s != nil {
		select {
		case <-s.rpc.done:
			CloseSession(owner)
			s, launchReason = nil, "process_exited"
		default:
			if s.fingerprint != fingerprint || (resume != "" && resume != s.currentSessionID()) {
				launchReason = "other_conversation"
				if s.fingerprint != fingerprint {
					launchReason = "launch_changed:" + llmtypes.AgentProcessChanged(s.parts, parts)
				}
				CloseSession(owner)
				s = nil
			} else {
				s.rpc.process.Reused(nil, "launch_unchanged")
			}
		}
	}
	if s == nil {
		if s, err = launch(ctx, agent, opts, owner, workdir, mode, mcpJSON, resume, system); err != nil {
			return nil, err
		}
		s.rpc.process.Launched(nil, launchReason)
		s.fingerprint, s.parts = fingerprint, parts
		if agent.SessionMeta != nil || (resume != "" && s.currentSessionID() == resume) {
			s.instructions.Started(system)
		} else {
			s.instructions.Started("")
		}
		sessions.Lock()
		sessions.m[owner] = s
		sessions.Unlock()
	}
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	s.mu.Lock()
	s.chatModel = chatModel
	s.mu.Unlock()
	if err := s.selectModel(ctx, model); err != nil {
		return nil, err
	}
	s.applyThought(ctx, thought)
	if llmtypes.CodingProviderLaunchOnlyFromOptions(opts) {
		return s.response("", nil), nil
	}
	if strings.TrimSpace(prompt) == "" && !hasPendingImages(ctx, owner, messages) {
		return nil, fmt.Errorf("%s prompt is empty", agent.Label)
	}
	if llmtypes.NativePlanModeForSession(ctx, owner) {
		restore, err := s.planTurn(ctx)
		if err != nil {
			return nil, err
		}
		defer restore()
	}
	// A slash command (agy's /compact) must reach the agent first in the prompt;
	// the instructions then wait for the next ordinary prompt.
	command := strings.HasPrefix(strings.TrimSpace(prompt), "/")
	var deliveredInstructions func()
	if !command {
		var delivery instructions.Delivery
		delivery, deliveredInstructions = s.instructions.Next(system)
		prompt = instructions.InMessage(delivery, prompt)
	}
	emit := func(chunk llmtypes.StreamChunk) {
		if opts.StreamChan != nil {
			select {
			case opts.StreamChan <- chunk:
			case <-ctx.Done():
			}
		}
	}
	response, err := s.turn(ctx, prompt, emit, turnImages{owner: owner, messages: messages})
	if err == nil && deliveredInstructions != nil {
		deliveredInstructions()
	}
	return response, err
}

func launch(ctx context.Context, agent Agent, opts *llmtypes.CallOptions, owner, workdir, mode, mcpJSON, resume, system string) (*session, error) {
	servers, err := parseMCPServers(owner, mcpJSON)
	if err != nil {
		return nil, err
	}
	full := mode == "full"
	var spec *LaunchSpec
	if agent.Launch != nil {
		if spec, err = agent.Launch(LaunchRequest{Opts: opts, Owner: owner, WorkDir: workdir, ToolsMode: mode, MCPConfig: mcpJSON}); err != nil {
			return nil, err
		}
	}
	rpc, env, err := startAgent(agent, opts, owner, workdir, full, servers, spec)
	if err != nil {
		return nil, err
	}
	rpc.process = llmtypes.NewAgentProcess(agent.providerID(), owner)
	// Same rule as the other coding agents: a mounted bridge (legacy unset
	// mode) or Full tools runs without per-tool prompts; mcp_only asks.
	auto := full || (mode == "" && len(servers) > 0)
	s := &session{agent: agent, rpc: rpc, env: env, workdir: workdir, autoApprove: auto, fullTools: full}
	if spec != nil {
		s.autoApprove = s.autoApprove || spec.AutoApprove
		s.questionPrefix = spec.QuestionIDPrefix
		s.outcome = spec.Outcome
		if spec.NoSessionMCP {
			servers = []mcpServer{}
		}
	}
	go s.pump()
	if err := s.handshake(ctx, resume, servers, system); err != nil {
		if line := rpc.stderrLine(); line != "" {
			err = fmt.Errorf("%w (last agent error: %s)", err, line)
		}
		s.closeNow()
		return nil, err
	}
	return s, nil
}

// foldMessages returns the chat's instructions and the pending human text.
func foldMessages(messages []llmtypes.MessageContent, opts *llmtypes.CallOptions) (string, string) {
	var msgSystems []string
	seen := map[string]bool{}
	for _, m := range messages {
		if m.Role == llmtypes.ChatMessageTypeSystem {
			if text := strings.TrimSpace(messageText(m)); text != "" && !seen[text] {
				seen[text] = true
				msgSystems = append(msgSystems, text)
			}
		}
	}
	// CodingProviderLaunchSystemPromptFromOptions is normally a copy of the
	// system message already in messages; avoid doubling the prompt (#4560).
	var systems []string
	if launchSystem := strings.TrimSpace(llmtypes.CodingProviderLaunchSystemPromptFromOptions(opts)); launchSystem != "" {
		joinedMsgs := strings.Join(msgSystems, "\n\n")
		if !seen[launchSystem] && launchSystem != joinedMsgs {
			systems = append(systems, launchSystem)
		}
	}
	systems = append(systems, msgSystems...)
	var pending []string
	for _, m := range llmtypes.PendingHumanMessages(messages) {
		pending = append(pending, messageText(m))
	}
	return strings.Join(systems, "\n\n"), strings.Join(pending, "\n\n")
}

func messageText(m llmtypes.MessageContent) string {
	var parts []string
	for _, part := range m.Parts {
		switch v := part.(type) {
		case llmtypes.TextContent:
			parts = append(parts, v.Text)
		case *llmtypes.TextContent:
			if v != nil {
				parts = append(parts, v.Text)
			}
		}
	}
	return strings.Join(parts, "")
}

// GetStatusLine returns the chat's latest context usage from its agent.
func (a *Adapter) GetStatusLine(_ context.Context, owner string) (*llmtypes.StatusLine, error) {
	return StatusLine(owner), nil
}

// StatusLine returns a copy of the chat's latest context usage, or nil.
func StatusLine(owner string) *llmtypes.StatusLine {
	sessions.Lock()
	s := sessions.m[owner]
	sessions.Unlock()
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status == nil {
		return nil
	}
	result := *s.status
	result.Metadata = make(map[string]interface{}, len(s.status.Metadata))
	for k, v := range s.status.Metadata {
		result.Metadata[k] = v
	}
	return &result
}

func (s *session) currentSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}
