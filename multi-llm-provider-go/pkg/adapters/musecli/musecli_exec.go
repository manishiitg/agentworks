package musecli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// EnvMuseCLIExecProvider overrides the muse startup provider for one exec
// lane run (default "meta"). Tests set it to "echo": deterministic, no
// auth, no API spend.
const EnvMuseCLIExecProvider = "MUSE_CLI_EXEC_PROVIDER"

const defaultMuseExecProvider = "meta"

// museWireEvent is one JSONL line from `muse exec --json` (MSP wire schema,
// `muse schema`). Shapes verified live against muse 1.1.1.
type museWireEvent struct {
	PayloadType string          `json:"payload_type"`
	Stream      museWireStream  `json:"stream"`
	CausationID string          `json:"causation_id"`
	Payload     museWirePayload `json:"payload"`
}

type museWireStream struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type museWirePayload struct {
	Kind      string `json:"kind"`
	CommandID string `json:"command_id"`
	Text      string `json:"text"`
	Terminal  string `json:"terminal"`
	Reason    string `json:"reason"`
}

func (a *MuseCLIAdapter) museExecProvider() string {
	if p := strings.TrimSpace(os.Getenv(EnvMuseCLIExecProvider)); p != "" {
		return p
	}
	return defaultMuseExecProvider
}

// museExecEffort validates the caller's reasoning-effort knob against the
// levels `muse exec` accepts (see --reasoning-effort). Empty means "omit the
// flag, take the CLI default". An unknown non-empty value is a caller bug
// and fails fast rather than silently running at the wrong effort — this is
// what the tier ladder (xhigh/high/medium) drives.
func museExecEffort(opts *llmtypes.CallOptions) (string, error) {
	if opts == nil {
		return "", nil
	}
	effort := strings.ToLower(strings.TrimSpace(opts.ReasoningEffort))
	if effort == "" {
		return "", nil
	}
	for _, level := range museReasoningEffortLevels {
		if effort == level {
			return effort, nil
		}
	}
	return "", fmt.Errorf("muse-cli exec lane: unknown reasoning effort %q (want one of %s)", opts.ReasoningEffort, strings.Join(museReasoningEffortLevels, "|"))
}

// museBuildExecPrompt folds messages into one exec prompt: system texts
// become a header (exec has no system-prompt flag), the last human text is
// the prompt. Non-text parts are rejected: pass image paths as text.
func museBuildExecPrompt(messages []llmtypes.MessageContent, resume bool) (string, error) {
	system, human, err := museSplitPrompt(messages)
	if err != nil {
		return "", err
	}
	if !resume {
		human = museFreshHistoryPrompt(messages, human)
	}
	return museInlinePrompt(system, human), nil
}

// museSplitPrompt separates system-role texts from the human turn text so
// the tmux lane can project the system prompt to AGENTS.md (file-only mode)
// instead of typing it inline. The exec lane keeps concatenating via
// museBuildExecPrompt above.
func museSplitPrompt(messages []llmtypes.MessageContent) (system []string, human string, err error) {
	for _, m := range messages {
		var parts []string
		for _, part := range m.Parts {
			switch c := part.(type) {
			case llmtypes.TextContent:
				parts = append(parts, c.Text)
			case *llmtypes.TextContent:
				if c != nil {
					parts = append(parts, c.Text)
				}
			default:
				return nil, "", fmt.Errorf("muse-cli exec lane supports text only; got %T (pass image paths as text)", part)
			}
		}
		text := strings.Join(parts, "")
		switch m.Role {
		case llmtypes.ChatMessageTypeSystem:
			if strings.TrimSpace(text) != "" {
				system = append(system, text)
			}
		case llmtypes.ChatMessageTypeHuman:
			human = text
		}
	}
	if strings.TrimSpace(human) == "" {
		return nil, "", fmt.Errorf("muse-cli exec lane needs a human prompt, got none")
	}
	return system, human, nil
}

func museStderrTail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 4096 {
		// Muse prints the decisive parse/configuration error before a long help
		// page. Preserve both ends so the useful cause is not truncated away.
		s = s[:1024] + "\n... stderr truncated ...\n" + s[len(s)-3072:]
	}
	return s
}

// generateContentExec runs one headless turn via `muse exec --json` and
// returns the terminal text (falling back to joined deltas). run.output.delta
// chunks are forwarded to opts.StreamChan when set; the channel stays
// caller-owned and is never closed here. Token counts come from the session
// sidecar (transcript reader), not the wire, and feed the shadow cost
// estimate via museAttachTurnCost.
func (a *MuseCLIAdapter) generateContentExec(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	opts := &llmtypes.CallOptions{}
	for _, opt := range options {
		opt(opts)
	}
	prompt, err := museBuildExecPrompt(messages, museResumeSessionIDFromOptions(opts) != "")
	if err != nil {
		return nil, err
	}
	prompt = llmtypes.CLIPrivateSkillInstructions(opts, museSkillsSubdir) + prompt
	// exec resumes with --session-id: the old history may claim there is no bridge.
	prompt = museWithResumeNote(prompt, museResumeSessionIDFromOptions(opts) != "", museMCPConfigFromOptions(opts))
	model := strings.TrimSpace(a.modelID)
	if strings.TrimSpace(opts.Model) != "" {
		model = strings.TrimSpace(opts.Model)
	}
	provider := a.museExecProvider()
	argv := []string{"exec", "--json", "--provider", provider, "--trust-workspace"}
	if a.apiKey != "" {
		argv = append(argv, "--api-key-stdin")
	}
	// --model only for concrete non-placeholder models; the echo test
	// provider takes no model flag.
	if model != "" && model != DefaultModelID && provider != "echo" {
		argv = append(argv, "--model", model)
	}
	if resumeID := strings.TrimSpace(museResumeSessionIDFromOptions(opts)); resumeID != "" {
		argv = append(argv, "--session-id", resumeID)
	}
	if effort, err := museExecEffort(opts); err != nil {
		return nil, err
	} else if effort != "" {
		argv = append(argv, "--reasoning-effort", effort)
	}
	// MCP servers reach muse through the user-level settings.json (there is
	// no --mcp-config flag). Merge for the duration of this run only.
	// Unconditional: museApplyMCPConfig also forces tui.voice_enabled off
	// (settings.json is the only control muse exposes for it), applied to
	// every run so it can't be left on by whichever lane last restored it.
	mcpJSON := strings.TrimSpace(museMCPConfigFromOptions(opts))
	toolAllowlist, toolAllowlistSet := museToolAllowlistFromOptions(opts)
	if !toolAllowlistSet {
		toolAllowlist = nil
	}
	ctx = museWithAccount(ctx, opts, a.apiKey)
	workdir := strings.TrimSpace(museWorkingDirFromOptions(opts))
	if workdir == "" {
		workdir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve Muse working directory: %w", err)
		}
	}
	handoff := sessiontransfer.MuseHandoffWorkspace(museAccountDataHome(ctx), museResumeSessionIDFromOptions(opts), workdir)
	if handoff {
		argv = append(argv, "--allow-workspace-switch", "--workspace", workdir)
		system, human, splitErr := museSplitPrompt(messages)
		if splitErr != nil {
			return nil, splitErr
		}
		prompt = nativeHandoffInstructions(museWithResumeNote(human, true, mcpJSON), llmtypes.CLIPrivateSkillInstructions(opts, museSkillsSubdir)+strings.Join(system, "\n\n"))
	}
	configHome, cleanupConfig, err := museAccountConfig(ctx, mcpJSON, toolAllowlist)
	if err != nil {
		return nil, err
	}
	defer cleanupConfig()
	if mcpJSON != "" {
		// MCP-server tools gate on approval while built-in shell tools do
		// not: a mounted run with approvals on stalls forever waiting for a
		// human (proven live 2026-09-10). Mounting a bridge is itself the
		// explicit request for tool-capable execution, so this run only gets
		// --disable-approval; unmounted runs keep the CLI default.
		argv = append(argv, "--disable-approval")
	}
	argv = append(argv, museToolArgv(toolAllowlistSet)...)
	argv = append(argv, museBaseURLArgv(opts)...)
	argv = append(argv, prompt)

	launch := musePreludeArgv(opts, append([]string{"muse"}, argv...))
	cmd := exec.CommandContext(ctx, launch[0], launch[1:]...)
	cmd.Env = llmtypes.MergeCodingAgentSecretEnvironment(museEnvironmentWithConfigHome(configHome), opts)
	// The per-turn MCP overlay takes precedence over the persistent account config.
	cmd.Env = append(cmd.Env, "XDG_CONFIG_HOME="+configHome)
	cmd.Env = append(cmd.Env, museRuntimeEnv(opts)...)
	// The CLI treats the process cwd as the workspace root (skills, trust,
	// transcript scoping), so pin it when the caller asks. Empty keeps the
	// inherited cwd — the working_directory cert pins the explicit case.
	if skills := llmtypes.AttachedSkillsFromOptions(opts); len(skills) > 0 {
		_ = a.ProjectSkills(llmtypes.CLIArtifactDirectory(opts, workdir), skills)
	}
	cmd.Dir = workdir
	cleanupSandbox, err := museLandlockCmd(opts, cmd, workdir)
	if err != nil {
		return nil, err
	}
	defer cleanupSandbox()
	if a.apiKey != "" {
		cmd.Stdin = strings.NewReader(a.apiKey)
	} else {
		cmd.Stdin = strings.NewReader("")
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("muse exec stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		if _, lookErr := exec.LookPath("muse"); lookErr != nil {
			return nil, fmt.Errorf("muse CLI not in PATH: %w", lookErr)
		}
		return nil, fmt.Errorf("start muse exec: %w", err)
	}
	var deltas strings.Builder
	terminalText := ""
	failedReason := ""
	sessionID := ""
	commandID := ""
	unparsed := 0
	sawNativeSubagent := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var ev museWireEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			unparsed++
			continue
		}
		if sessionID == "" && ev.Stream.ID != "" {
			sessionID = ev.Stream.ID
		}
		if commandID == "" {
			if ev.Payload.CommandID != "" {
				commandID = ev.Payload.CommandID
			} else if ev.CausationID != "" {
				commandID = ev.CausationID
			}
		}
		if strings.HasPrefix(ev.PayloadType, "subagent.") {
			sawNativeSubagent = true
		}
		switch ev.PayloadType {
		case "run.output.delta":
			deltas.WriteString(ev.Payload.Text)
			if opts.StreamChan != nil {
				opts.StreamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: ev.Payload.Text}
			}
		case "run.terminal.completed":
			if ev.Payload.Text != "" {
				terminalText = ev.Payload.Text
			}
		case "run.terminal.failed":
			if ev.Payload.Reason != "" {
				failedReason = ev.Payload.Reason
			} else if ev.Payload.Text != "" {
				failedReason = ev.Payload.Text
			}
		case "tool.result":
			for _, chunk := range museToolStreamChunks(line) {
				if strings.HasPrefix(chunk.ToolName, "subagent_") {
					sawNativeSubagent = true
				}
				if opts.StreamChan != nil {
					opts.StreamChan <- *chunk
				}
			}
		case "task.lifecycle.status":
			if opts.StreamChan != nil {
				if chunk := museStatusChunk(line); chunk != nil {
					opts.StreamChan <- *chunk
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Wait()
		if quotaErr := museUsageLimitError(model, time.Now(), failedReason, terminalText, deltas.String(), stderr.String()); quotaErr != nil {
			return nil, quotaErr
		}
		return nil, fmt.Errorf("read muse exec stream: %w (stderr: %s)", err, museStderrTail(stderr.String()))
	}
	waitErr := cmd.Wait()
	if quotaErr := museUsageLimitError(model, time.Now(), failedReason, terminalText, deltas.String(), stderr.String()); quotaErr != nil {
		return nil, quotaErr
	}
	if waitErr != nil {
		if failedReason != "" {
			return nil, fmt.Errorf("muse exec failed: %s: %w (stderr: %s)", failedReason, waitErr, museStderrTail(stderr.String()))
		}
		return nil, fmt.Errorf("muse exec failed: %w (stderr: %s)", waitErr, museStderrTail(stderr.String()))
	}
	final := terminalText
	if strings.TrimSpace(final) == "" {
		final = deltas.String()
	}
	if strings.TrimSpace(final) == "" {
		return nil, fmt.Errorf("muse exec returned no text (session %s, %d unparsed lines, stderr: %s)", sessionID, unparsed, museStderrTail(stderr.String()))
	}
	turnUsage, hasTurnUsage := museUsageForTurn(sessionID, commandID)
	gi := &llmtypes.GenerationInfo{}
	if handoff && sessionID != museResumeSessionIDFromOptions(opts) {
		return nil, fmt.Errorf("Muse started a different native session after handoff; the saved conversation was preserved")
	}
	if sawNativeSubagent {
		if children := readMuseNativeSubagents(sessionID); len(children) > 0 {
			gi.Additional = map[string]interface{}{"native_subagents": children}
		}
	}
	llmtypes.AttachCodingProviderSessionHandle(gi, llmtypes.CodingProviderSessionHandle{
		Provider:        "muse-cli",
		Transport:       llmtypes.CodingProviderTransportStructured,
		NativeSessionID: sessionID,
		WorkingDir:      workdir,
		Model:           model,
	})
	resp := &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{
		Content:        final,
		StopReason:     "completed",
		GenerationInfo: gi,
	}}}
	if hasTurnUsage {
		resp.Usage = &turnUsage
		museAttachTurnCost(gi, model, &turnUsage)
	}
	return resp, nil
}
