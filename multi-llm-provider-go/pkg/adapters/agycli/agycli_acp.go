package agycli

import (
	"context"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/acpcli"
)

// The ACP lane runs a chat on `agy acp` through the generic ACP connector
// (package acpcli, agent "agy"): acpcli owns the protocol; this package owns
// only agy's process setup, the launch the JSON lane uses (private home with
// the chat's MCP catalogue, tool-mode hook, sandbox), and reads agy's own
// conversation store to classify a failed turn.

// AgyTransportEnv picks agy's transport for chats: acp (default), tmux.
const AgyTransportEnv = "AGY_CLI_TRANSPORT"

const agyACPAgent = "agy"

// AgyACPSelected reports whether chats run on `agy acp`.
func AgyACPSelected() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv(AgyTransportEnv)), "tmux")
}

func init() { acpcli.SetLaunch(agyACPAgent, agyACPLaunch) }

// CloseAgyACPSession ends a chat's `agy acp` process (session/close first, so
// its conversation store has no writer).
func CloseAgyACPSession(owner string) { acpcli.CloseAgentSession(owner, agyACPAgent) }

// AgyACPSessionActive reports whether a chat runs on `agy acp` now.
func AgyACPSessionActive(owner string) bool { return acpcli.AgentSessionActive(owner, agyACPAgent) }

// AgyACPTurnSteerable reports whether a chat's running `agy acp` turn takes messages now (agy 1.5.0+, #3920).
func AgyACPTurnSteerable(owner string) bool { return acpcli.TurnSteerable(owner, agyACPAgent) }

// AgyACPSessionTooOldToSteer reports whether a chat runs on an `agy acp` older than 1.5.0, which queues a message
// sent mid-turn instead of reading it into the running turn.
func AgyACPSessionTooOldToSteer(owner string) bool {
	return AgyACPSessionActive(owner) && !acpcli.SessionSteersWithPrompt(owner, agyACPAgent)
}

// SendAgyACPInput hands a message to a chat's running `agy acp` turn as another session/prompt, which agy reads
// into the running turn. The error wraps llmtypes.ErrCodingAgentInputNotSent when no running turn took it.
func SendAgyACPInput(ctx context.Context, owner, message string) error {
	return acpcli.SteerTurn(ctx, owner, agyACPAgent, message)
}

func closeAllAgyACPSessions() { acpcli.CloseAgentSessions(agyACPAgent) }

// GetStatusLine returns only this owner's active native session context record.
func (a *AgyCLIAdapter) GetStatusLine(_ context.Context, owner string) (*llmtypes.StatusLine, error) {
	if !AgyACPSessionActive(owner) {
		return nil, nil
	}
	return acpcli.StatusLine(owner), nil
}

// generateContentACP runs one chat turn on the chat's `agy acp` session.
func (a *AgyCLIAdapter) generateContentACP(ctx context.Context, messages []llmtypes.MessageContent, opts *llmtypes.CallOptions) (*llmtypes.ContentResponse, error) {
	effort, err := agyExecEffort(opts)
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(a.modelID)
	if strings.TrimSpace(opts.Model) != "" {
		model = strings.TrimSpace(opts.Model)
	}
	if model == "" {
		model = DefaultModelID
	}
	call := *opts
	call.ReasoningEffort = effort
	metadata := llmtypes.Metadata{}
	if opts.Metadata != nil {
		metadata = *opts.Metadata
	}
	metadata.Custom = make(map[string]interface{}, 16)
	if opts.Metadata != nil {
		for k, v := range opts.Metadata.Custom {
			metadata.Custom[k] = v
		}
	}
	for from, to := range map[string]string{
		MetadataKeyWorkingDir:           acpcli.MetadataKeyWorkingDir,
		MetadataKeyInteractiveSessionID: acpcli.MetadataKeyInteractiveSessionID,
		MetadataKeyResumeSessionID:      acpcli.MetadataKeyResumeSessionID,
		MetadataKeyMCPConfig:            acpcli.MetadataKeyMCPConfig,
		MetadataKeyNativeToolsMode:      acpcli.MetadataKeyNativeToolsMode,
	} {
		metadata.Custom[to] = agyStringMetadata(opts, from)
	}
	call.Metadata = &metadata
	return acpcli.Generate(ctx, agyACPAgent, model, messages, &call)
}

// agyACPLaunch is agy's process setup for one chat: its own private home
// (MCP catalogue, settings, tool-mode hook), the sandbox, and the same
// permission rule as the JSON lane.
func agyACPLaunch(req acpcli.LaunchRequest) (*acpcli.LaunchSpec, error) {
	opts := req.Opts
	args := []string{"acp", "--disable-telemetry"}
	var servers []agyMCPServer
	if strings.TrimSpace(req.MCPConfig) != "" {
		var err error
		if servers, err = agyParseMCPServers(req.MCPConfig); err != nil {
			return nil, err
		}
	}
	// A mounted bridge or Full CLI runs without per-tool prompts; the
	// tool-mode hook still decides what may run.
	if strings.TrimSpace(req.MCPConfig) != "" || agyFullNativeToolsMode(req.ToolsMode) {
		args = append(args, "--dangerously-skip-permissions")
	}
	privateHome, releaseHome, err := agyIsolatedHomeForCall(servers, req.WorkDir, opts)
	if err != nil {
		return nil, err
	}
	releaseHook, err := agyHoldToolModeHookForCall(req.WorkDir, privateHome, req.ToolsMode, opts, agyBridgeHostPorts(req.MCPConfig)...)
	if err != nil {
		releaseHome()
		return nil, err
	}
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "HOME=") {
			env = append(env, entry)
		}
	}
	env = agyPinHome(agyAccountEnv(llmtypes.MergeCodingAgentSecretEnvironment(append(env, "HOME="+privateHome, "NO_OPEN_BROWSER=1"), opts), opts), privateHome)
	agyLogLaunch(env)
	return &acpcli.LaunchSpec{
		Args: args,
		Env:  env,
		Confine: func(cmd *exec.Cmd) (func(), error) {
			return agyLandlockCmd(opts, cmd, req.WorkDir, privateHome)
		},
		Release:     func() { releaseHook(); releaseHome() },
		AutoApprove: true, // agy's tool-mode hook decides what may run, as on its other lanes
		// agy asks the person its own questions as permission requests with these ids (T3 Code reads them the same way).
		QuestionIDPrefix: "interaction_",
		NoSessionMCP:     true, // the MCP catalogue is in agy's private home
		Outcome:          agyACPOutcome{home: agyTranscriptHome(opts)},
	}, nil
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
