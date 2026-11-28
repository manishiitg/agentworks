package agycli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// agyExecEnvelope is the terminal result object from AGY's JSON output.
// Shape verified live against agy
// 1.2.7: status/response/usage plus denied_actions when headless mode
// auto-denies a native tool it cannot prompt for.
type agyExecEnvelope struct {
	ConversationID string          `json:"conversation_id"`
	Status         string          `json:"status"`
	Response       string          `json:"response"`
	Error          json.RawMessage `json:"error"`
	NumTurns       int             `json:"num_turns"`
	Usage          struct {
		InputTokens     int `json:"input_tokens"`
		OutputTokens    int `json:"output_tokens"`
		ThinkingTokens  int `json:"thinking_tokens"`
		CacheReadTokens int `json:"cache_read_tokens"`
		TotalTokens     int `json:"total_tokens"`
	} `json:"usage"`
	DeniedActions []struct {
		Action      string `json:"action"`
		DisplayName string `json:"display_name"`
	} `json:"denied_actions"`
}

type agyParsedExec struct {
	conversationID string
	response       string
	usage          llmtypes.Usage
}

// agyBuildExecPrompt folds messages into one print-mode prompt: the launch
// system prompt (when the caller sets one) plus system-role texts become a
// header (print mode has no system-prompt flag), the last human text is the
// turn. Non-text parts are rejected: pass image paths as text.
func agyBuildExecPrompt(messages []llmtypes.MessageContent, launchSystemPrompt string) (string, error) {
	var systems []string
	if strings.TrimSpace(launchSystemPrompt) != "" {
		systems = append(systems, strings.TrimSpace(launchSystemPrompt))
	}
	human := ""
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
				return "", fmt.Errorf("agy-cli exec lane supports text only; got %T (pass image paths as text)", part)
			}
		}
		text := strings.Join(parts, "")
		switch m.Role {
		case llmtypes.ChatMessageTypeSystem:
			if strings.TrimSpace(text) != "" {
				systems = append(systems, text)
			}
		case llmtypes.ChatMessageTypeHuman:
			human = text
		}
	}
	if strings.TrimSpace(human) == "" {
		return "", fmt.Errorf("agy-cli exec lane needs a human prompt, got none")
	}
	if len(systems) == 0 {
		return human, nil
	}
	return "System instructions:\n" + strings.Join(systems, "\n\n") + "\n\n" + human, nil
}

// agyBuildExecArgv builds the stream-json print-mode argv. The prompt is
// sent on stdin, avoiding process-list disclosure and Linux's per-argument
// size limit. The requested model is always explicit because AGY's API-key default differs
// from its signed-in default; resume pins --conversation; a schema
// string pins --json-schema. The exec lane never passes
// --dangerously-skip-permissions itself: the caller adds it only for mounted
// MCP or explicit Full CLI turns, whose selected hook supplies the tool gate.
func agyBuildExecArgv(model, resumeID, schemaJSON string) []string {
	argv := []string{"--input-format", "stream-json", "--output-format", "stream-json"}
	if model = strings.TrimSpace(model); model != "" {
		argv = append(argv, "--model", model)
	}
	if resumeID = strings.TrimSpace(resumeID); resumeID != "" {
		argv = append(argv, "--conversation", resumeID)
	}
	if schemaJSON = strings.TrimSpace(schemaJSON); schemaJSON != "" {
		argv = append(argv, "--json-schema", schemaJSON)
	}
	return argv
}

func agyStreamInput(prompt string) ([]byte, error) {
	message, err := json.Marshal(map[string]any{
		"event":   "user",
		"message": map[string]string{"content": prompt},
	})
	if err != nil {
		return nil, fmt.Errorf("encode agy stream input: %w", err)
	}
	return append(message, '\n'), nil
}

// agyParseStreamExec finds the terminal result event in AGY's NDJSON output.
// Init and step_update events are progress evidence, not final answers.
func agyParseStreamExec(data []byte) (*agyParsedExec, error) {
	var result json.RawMessage
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var event struct {
			Type   string          `json:"event"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, fmt.Errorf("agy stream event not JSON: %w (output: %s)", err, agyOutputTail(string(line)))
		}
		if event.Type == "result" {
			result = event.Result
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("agy stream returned no result event (output: %s)", agyOutputTail(string(data)))
	}
	return agyParseExecEnvelope(result)
}

// agyExecSchemaJSON marshals the caller's JSONSchema config for --json-schema.
// Empty when unset. agy takes the raw schema only: name/description/strict
// have no CLI counterpart and are ignored.
func agyExecSchemaJSON(opts *llmtypes.CallOptions) (string, error) {
	if opts == nil || opts.JSONSchema == nil || len(opts.JSONSchema.Schema) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(opts.JSONSchema.Schema)
	if err != nil {
		return "", fmt.Errorf("marshal agy --json-schema: %w", err)
	}
	return string(raw), nil
}

// agyParseExecEnvelope decodes one print-mode envelope into the final text,
// the CLI-native conversation id, and wire token usage. A non-SUCCESS
// status, an empty response, or auto-denied native actions (the headless
// default-deny posture) fail with the cause attached.
func agyParseExecEnvelope(data []byte) (*agyParsedExec, error) {
	var env agyExecEnvelope
	if err := json.Unmarshal(bytes.TrimSpace(data), &env); err != nil {
		return nil, fmt.Errorf("agy exec envelope not JSON: %w (output: %s)", err, agyOutputTail(string(data)))
	}
	if !strings.EqualFold(strings.TrimSpace(env.Status), "SUCCESS") {
		// Response may contain ordinary assistant text even on a failed turn.
		// Only an explicit error field is safe to scan for quota words anywhere.
		if quotaErr := agyQuotaFailureError("", env.Status, string(env.Error)); quotaErr != nil {
			return nil, quotaErr
		}
		return nil, fmt.Errorf("agy exec status %q (conversation %s, response: %s)", env.Status, env.ConversationID, agyOutputTail(env.Response))
	}
	if strings.TrimSpace(env.Response) == "" {
		if len(env.DeniedActions) > 0 {
			var names []string
			for _, denied := range env.DeniedActions {
				names = append(names, denied.Action)
			}
			return nil, fmt.Errorf("agy exec produced no text: headless mode auto-denied %s (add a permissions.allow rule or avoid native tools)", strings.Join(names, ", "))
		}
		return nil, fmt.Errorf("agy exec returned no text (conversation %s)", env.ConversationID)
	}
	usage := llmtypes.Usage{
		InputTokens:  env.Usage.InputTokens,
		OutputTokens: env.Usage.OutputTokens,
		TotalTokens:  env.Usage.TotalTokens,
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	if env.Usage.ThinkingTokens > 0 {
		thinking := env.Usage.ThinkingTokens
		usage.ThoughtsTokens = &thinking
	}
	if env.Usage.CacheReadTokens > 0 {
		cached := env.Usage.CacheReadTokens
		usage.CacheTokens = &cached
	}
	return &agyParsedExec{conversationID: env.ConversationID, response: env.Response, usage: usage}, nil
}

// agyExtractFinalJSONObject returns the last self-contained JSON object
// line in a schema-mode response. Verified live: with --json-schema the
// harness appends the schema-shaped final result (plus harness keys like
// toolAction/toolSummary) as the trailing line, while the model's free text
// above it is unconstrained. Falls back to the raw response when no JSON
// object line exists, so a harness shape change surfaces in e2e, not here.
func agyExtractFinalJSONObject(response string) string {
	lines := strings.Split(response, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if len(line) < 2 || !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
			continue
		}
		var probe map[string]interface{}
		if err := json.Unmarshal([]byte(line), &probe); err == nil {
			return line
		}
	}
	return response
}

func agyOutputTail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 2048 {
		s = s[:512] + "\n... truncated ...\n" + s[len(s)-1536:]
	}
	return s
}

// agyExecEffort validates the caller's reasoning-effort knob against the
// levels `agy --effort` accepts (agyReasoningEffortLevels). Empty means
// "omit the flag, take the CLI default". An unknown non-empty value fails
// fast rather than silently running at the wrong effort.
func agyExecEffort(opts *llmtypes.CallOptions) (string, error) {
	if opts == nil {
		return "", nil
	}
	effort := strings.ToLower(strings.TrimSpace(opts.ReasoningEffort))
	if effort == "" {
		return "", nil
	}
	for _, level := range agyReasoningEffortLevels {
		if effort == level {
			return effort, nil
		}
	}
	return "", fmt.Errorf("agy-cli exec lane: unknown reasoning effort %q (want one of %s)", opts.ReasoningEffort, strings.Join(agyReasoningEffortLevels, "|"))
}

func agyStringMetadata(opts *llmtypes.CallOptions, key string) string {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return ""
	}
	s, _ := opts.Metadata.Custom[key].(string)
	return strings.TrimSpace(s)
}

// E2E-only metadata keys, set by in-package live tests via raw metadata (no
// public option funcs). Production lanes must never set them:
//   - skipPermissions passes --dangerously-skip-permissions for one turn in
//     a fresh tmpdir, proving cwd reaches the model when tools are allowed.
//   - homeOverride points HOME at an empty dir, proving logged-out agy is
//     detected and failed fast instead of hanging on the OAuth prompt.
const (
	agyTestOnlyMetadataKeySkipPermissions = "agy_test_only_skip_permissions"
	agyTestOnlyMetadataKeyHomeOverride    = "agy_test_only_home_override"
)

// agyLoginRequiredMarker is stderr's first line when agy has no stored
// login. Proven live: logged-out agy prints this and hangs waiting for
// OAuth (even --print-timeout does not bound the wait), so the exec lane
// watches stderr and kills the run on sight.
const agyLoginRequiredMarker = "Authentication required"

// generateContentExec runs one headless turn via
// `agy --input-format stream-json --output-format stream-json` and returns the envelope's final
// text. Print mode is non-streaming: when opts.StreamChan is set it gets one
// content chunk with the full text; the channel stays caller-owned and is
// never closed here. Wire usage lands on the response (token_usage); the
// CLI-native conversation id is attached as the session handle for resume.
func (a *AgyCLIAdapter) generateContentExec(ctx context.Context, messages []llmtypes.MessageContent, opts *llmtypes.CallOptions) (*llmtypes.ContentResponse, error) {
	if err := llmtypes.ValidateCLISecurityLaunch(opts, llmtypes.ConfinedModes(opts)...); err != nil {
		return nil, err
	}
	prompt, err := agyBuildExecPrompt(messages, llmtypes.CodingProviderLaunchSystemPromptFromOptions(opts))
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(a.modelID)
	if opts != nil && strings.TrimSpace(opts.Model) != "" {
		model = strings.TrimSpace(opts.Model)
	}
	if model == "" {
		model = DefaultModelID
	}
	workdir := agyStringMetadata(opts, MetadataKeyWorkingDir)
	if workdir == "" {
		workdir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve agy working directory: %w", err)
		}
	}
	schemaJSON, err := agyExecSchemaJSON(opts)
	if err != nil {
		return nil, err
	}
	effort, err := agyExecEffort(opts)
	if err != nil {
		return nil, err
	}
	argv := agyBuildExecArgv(model, agyStringMetadata(opts, MetadataKeyResumeSessionID), schemaJSON)
	streamInput, err := agyStreamInput(prompt)
	if err != nil {
		return nil, err
	}
	if effort != "" {
		argv = append(argv, "--effort", effort)
		log.Printf("[AGY_CONFIG] action=applied lane=exec level=%s model=%s", effort, model)
	}
	toolMode, err := agyToolModeForLaunch(opts)
	if err != nil {
		return nil, err
	}
	if agyStringMetadata(opts, agyTestOnlyMetadataKeySkipPermissions) == "true" {
		argv = append(argv, "--dangerously-skip-permissions")
	}
	if agyFullNativeToolsMode(toolMode) && strings.TrimSpace(agyStringMetadata(opts, MetadataKeyMCPConfig)) == "" {
		argv = append(argv, "--dangerously-skip-permissions")
	}
	privateHome := ""
	if mcpJSON := agyStringMetadata(opts, MetadataKeyMCPConfig); strings.TrimSpace(mcpJSON) != "" {
		// Mounting the bridge is the explicit request for tool-capable
		// execution, so this run gets --dangerously-skip-permissions;
		// unmounted runs keep headless default-deny. Each run owns a private
		// AGY home and MCP catalogue.
		servers, err := agyParseMCPServers(mcpJSON)
		if err != nil {
			return nil, err
		}
		var releaseMounts func()
		privateHome, releaseMounts, err = agyIsolatedHomeForCall(servers, workdir, opts)
		if err != nil {
			return nil, err
		}
		defer releaseMounts()
		argv = append(argv, "--dangerously-skip-permissions")
	}
	if privateHome == "" {
		var cleanup func()
		privateHome, cleanup, err = agyIsolatedHomeForCall(nil, workdir, opts)
		if err != nil {
			return nil, err
		}
		defer cleanup()
	}

	cmd := exec.CommandContext(ctx, "agy", argv...)
	cmd.Stdin = bytes.NewReader(streamInput)
	baseEnv := os.Environ()
	if privateHome != "" {
		filtered := make([]string, 0, len(baseEnv)+1)
		for _, entry := range baseEnv {
			if !strings.HasPrefix(entry, "HOME=") {
				filtered = append(filtered, entry)
			}
		}
		baseEnv = append(filtered, "HOME="+privateHome)
	}
	// NOTE: a.apiKey (config-supplied) is deliberately NOT exported: env is
	// the only key path. A key WITHOUT the modelProvider "gemini" flip is
	// ignored by design (that is what the earlier "ignores the key" probe
	// actually proved); with the flip, the ambient GEMINI_API_KEY below
	// bills the run instead of the stored login.
	if home := agyStringMetadata(opts, agyTestOnlyMetadataKeyHomeOverride); home != "" {
		filtered := baseEnv[:0]
		for _, entry := range baseEnv {
			if !strings.HasPrefix(entry, "HOME=") {
				filtered = append(filtered, entry)
			}
		}
		baseEnv = append(filtered, "HOME="+home)
	}
	cmd.Env = agyAccountEnv(llmtypes.MergeCodingAgentSecretEnvironment(baseEnv, opts), opts)
	if privateHome != "" && agyStringMetadata(opts, agyTestOnlyMetadataKeyHomeOverride) == "" {
		cmd.Env = agyPinHome(cmd.Env, privateHome)
	}
	// The CLI treats the process cwd as the workspace root (trust,
	// conversation scoping), so pin it when the caller asks. Empty keeps
	// the inherited cwd — the working_directory cert pins the explicit case.
	cmd.Dir = workdir
	cleanupSandbox, err := agyLandlockCmd(opts, cmd, workdir, privateHome)
	if err != nil {
		return nil, err
	}
	defer cleanupSandbox()
	releaseToolHook, err := agyHoldToolModeHookForCall(workdir, privateHome, toolMode, opts, agyBridgeHostPorts(agyStringMetadata(opts, MetadataKeyMCPConfig))...)
	if err != nil {
		return nil, err
	}
	defer releaseToolHook()
	turnStartIdx := agyConversationMaxIdx(agyStringMetadata(opts, MetadataKeyResumeSessionID), agyTranscriptHome(opts))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("agy exec stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		if _, lookErr := exec.LookPath("agy"); lookErr != nil {
			return nil, fmt.Errorf("agy CLI not in PATH: %w", lookErr)
		}
		return nil, fmt.Errorf("start agy exec: %w", err)
	}
	// Logged-out agy prints the login marker and hangs forever waiting for
	// OAuth, so stderr is scanned live: on the marker the run is killed
	// and reported as login-required instead of burning the ctx timeout.
	loginRequired := make(chan struct{})
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		scanner := bufio.NewScanner(stderrPipe)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		marked := false
		for scanner.Scan() {
			line := scanner.Text()
			stderr.WriteString(line + "\n")
			if !marked && strings.Contains(strings.ToLower(line), strings.ToLower(agyLoginRequiredMarker)) {
				marked = true
				close(loginRequired)
				_ = cmd.Process.Kill()
			}
		}
	}()
	waitErr := cmd.Wait()
	<-stderrDone
	select {
	case <-loginRequired:
		return nil, fmt.Errorf("agy login required: no stored Google login for this run (authenticate once with `agy` before headless use)")
	default:
	}
	if waitErr != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if quotaErr := agyQuotaStderrError(model, stderr.String()); quotaErr != nil {
			return nil, quotaErr
		}
		// A failed result envelope is an error carrier; raw NDJSON stdout is not.
		if _, parseErr := agyParseStreamExec(stdout.Bytes()); llmerrors.KindOf(parseErr) == llmerrors.KindQuotaExhausted {
			return nil, parseErr
		}
		return nil, fmt.Errorf("agy exec failed: %w (stderr: %s)", waitErr, agyOutputTail(stderr.String()))
	}
	parsed, err := agyParseStreamExec(stdout.Bytes())
	if err != nil {
		return nil, fmt.Errorf("%w (stderr: %s)", err, agyOutputTail(stderr.String()))
	}
	// The result envelope can concatenate interim subagent-wait narration with
	// the final answer. The completed native trail identifies the last authored
	// answer, just as it does for retained turns; fixtures without a trail keep
	// the envelope fallback.
	var nativeChildren []llmtypes.NativeSubagentTranscript
	if agyFullNativeToolsMode(toolMode) {
		if record, readErr := agyReadTurnRecord(parsed.conversationID, turnStartIdx, prompt, agyTranscriptHome(opts)); readErr == nil {
			if agyTurnEndedByExecutorError(record) {
				return nil, agyExecutorTurnError(record.executorError)
			}
			if schemaJSON == "" && record.lastType == agyStepAssistant && record.lastStatus == 3 && record.finalAnswer != "" {
				parsed.response = record.finalAnswer
			}
			if opts != nil && opts.StreamChan != nil && record.userIdx >= 0 {
				for _, chunk := range agyExecRecordedToolChunks(parsed.conversationID, record.userIdx, agyTranscriptHome(opts)) {
					opts.StreamChan <- chunk
				}
			}
			if len(record.subagents) > 0 {
				nativeChildren = readAgyNativeSubagents(parsed.conversationID, agyTranscriptHome(opts))
			}
		}
	}
	if schemaJSON != "" {
		parsed.response = agyExtractFinalJSONObject(parsed.response)
	}
	if opts != nil && opts.StreamChan != nil {
		opts.StreamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: parsed.response}
	}
	gi := &llmtypes.GenerationInfo{}
	if len(nativeChildren) > 0 {
		gi.Additional = map[string]interface{}{"native_subagents": nativeChildren}
	}
	llmtypes.AttachCodingProviderSessionHandle(gi, llmtypes.CodingProviderSessionHandle{
		Provider:        "agy-cli",
		Transport:       llmtypes.CodingProviderTransportStructured,
		NativeSessionID: parsed.conversationID,
		WorkingDir:      workdir,
		Model:           model,
	})
	agyAttachCost(gi, model, parsed.usage)
	if count := len(agyUsageSteps(parsed.conversationID, turnStartIdx, agyTranscriptHome(opts))); count > 0 {
		gi.Additional["llm_call_count"] = count
		gi.Additional["llm_call_count_source"] = "native_model_generations"
	}
	return &llmtypes.ContentResponse{
		Choices: []*llmtypes.ContentChoice{{
			Content:        parsed.response,
			StopReason:     "completed",
			GenerationInfo: gi,
		}},
		Usage: &parsed.usage,
	}, nil
}

// Headless exec records native invocations in the same trail as the TUI. Emit
// only calls after this run's intake, so resuming never replays old tool rows.
func agyExecRecordedToolChunks(conversationID string, userIdx int, home string) []llmtypes.StreamChunk {
	if userIdx < 0 {
		return nil
	}
	var chunks []llmtypes.StreamChunk
	for _, call := range agyTurnToolCallsSince(conversationID, userIdx, home) {
		chunks = append(chunks,
			llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallStart, ToolName: call.Name, ToolArgs: call.Args, ToolCallID: call.CallID},
			llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallEnd, ToolName: call.Name, ToolCallID: call.CallID, ToolResult: call.ErrorText})
	}
	return chunks
}
