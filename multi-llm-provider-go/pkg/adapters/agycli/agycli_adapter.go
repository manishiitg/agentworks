package agycli

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// AgyCLIAdapter drives the Antigravity CLI (`agy`). Turns route by lane:
// persistent interactive runs inside the TUI sidecar's conversation
// (Builder chats); everything else uses the JSON exec lane (`agy -p
// --output-format json`, resumed with `--conversation`). Live input and
// control keys target the sidecar, which now carries the turn itself.
type AgyCLIAdapter struct {
	apiKey  string
	modelID string
	logger  interfaces.Logger
}

// NewAgyCLIAdapter builds an Antigravity adapter. Auth resolves in-CLI:
// stored Google login by default, GEMINI_API_KEY env when modelProvider is
// "gemini". apiKey is accepted for signature symmetry and never exported.
func NewAgyCLIAdapter(apiKey string, modelID string, logger interfaces.Logger) *AgyCLIAdapter {
	if modelID == "" {
		modelID = DefaultModelID
	}
	return &AgyCLIAdapter{apiKey: apiKey, modelID: modelID, logger: logger}
}

// GetModelID returns the configured model id.
func (a *AgyCLIAdapter) GetModelID() string { return a.modelID }

// GetModelMetadata returns metadata for an agy model id.
func (a *AgyCLIAdapter) GetModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	return GetAgyModelMetadata(modelID)
}

// GenerateContent routes one agy turn by lane: persistent interactive +
// an owner session id runs the turn inside the TUI sidecar's own
// conversation (Builder chats: paste, await, extract, meter from the
// conversation .db); everything else stays on the headless JSON exec lane
// (steps/workflows). Both lanes fold system+human identically and share
// the mount manager, so the tool surface is the same either way. The
// sidecar needs a pre-trusted cwd (trust gates fail loudly, muse parity).
func (a *AgyCLIAdapter) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	opts := &llmtypes.CallOptions{}
	for _, opt := range options {
		opt(opts)
	}
	if agyStringMetadata(opts, MetadataKeyPersistentInteractive) != "true" || !AgyACPSelected() {
		for _, input := range llmtypes.ClaimNativeImageInputsForSession(ctx, agyStringMetadata(opts, MetadataKeyInteractiveSessionID)) {
			if input.Skipped != nil {
				input.Skipped("text_only_transport")
			}
		}
	}
	if agyStringMetadata(opts, MetadataKeyPersistentInteractive) == "true" {
		// Chats run on `agy acp` by default; tmux stays selectable (AGY_CLI_TRANSPORT=tmux).
		if AgyACPSelected() {
			return a.generateContentACP(ctx, messages, opts)
		}
		return a.generateContentInteractive(ctx, messages, opts)
	}
	return a.generateContentExec(ctx, messages, opts)
}

// generateContentInteractive runs one turn in the owner's sidecar TUI:
// same prompt fold as exec, reply extracted from the pane, usage summed
// from the conversation .db steps the turn appended. Schema-mode is exec-only.
// Interactive effort must match the selected model's baked-in effort.
func (a *AgyCLIAdapter) generateContentInteractive(ctx context.Context, messages []llmtypes.MessageContent, opts *llmtypes.CallOptions) (*llmtypes.ContentResponse, error) {
	if err := llmtypes.ValidateCLISecurityLaunch(opts, llmtypes.ConfinedModes(opts)...); err != nil {
		return nil, err
	}
	owner := agyStringMetadata(opts, MetadataKeyInteractiveSessionID)
	if owner == "" {
		return nil, fmt.Errorf("agy persistent interactive turn needs an owner session id (WithInteractiveSessionID)")
	}
	workdir := agyStringMetadata(opts, MetadataKeyWorkingDir)
	if workdir == "" {
		var err error
		workdir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve agy working directory: %w", err)
		}
	}
	if schemaJSON, err := agyExecSchemaJSON(opts); err != nil {
		return nil, err
	} else if strings.TrimSpace(schemaJSON) != "" {
		return nil, fmt.Errorf("agy schema-mode turns are exec-only; persistent interactive does not support --json-schema")
	}
	model := strings.TrimSpace(a.modelID)
	if opts != nil && strings.TrimSpace(opts.Model) != "" {
		model = strings.TrimSpace(opts.Model)
	}
	if model == "" {
		model = DefaultModelID
	}
	if effort, err := agyExecEffort(opts); err != nil {
		return nil, err
	} else if effort != "" {
		if !strings.HasSuffix(model, "-"+effort) {
			log.Printf("[AGY_CONFIG] action=refused lane=interactive reason=effort_not_in_booted_model level=%s model=%s", effort, model)
			return nil, fmt.Errorf("agy interactive effort %q does not match the booted model %q; choose a model with that effort", effort, model)
		}
		log.Printf("[AGY_CONFIG] action=applied lane=interactive level=%s model=%s", effort, model)
	}
	launchOnly := llmtypes.CodingProviderLaunchOnlyFromOptions(opts)
	prompt := ""
	if !launchOnly {
		var err error
		prompt, err = agyBuildExecPrompt(messages, llmtypes.CodingProviderLaunchSystemPromptFromOptions(opts))
		if err != nil {
			return nil, err
		}
	}
	mcpJSON := agyStringMetadata(opts, MetadataKeyMCPConfig)
	resumeConversation := agyStringMetadata(opts, MetadataKeyResumeSessionID)
	toolMode, err := agyToolModeForLaunch(opts)
	if err != nil {
		return nil, err
	}
	session, err := ensureAgyInteractiveSessionForTurn(ctx, owner, workdir, model, mcpJSON, resumeConversation, toolMode, opts)
	if err != nil {
		return nil, err
	}
	defer session.turnLeases.Add(-1)
	if launchOnly {
		// Warmup is followed by Session live input, which reads structured
		// progress rather than running this adapter's terminal stream. Publish
		// the ready pane now so the host can register and attach the main view.
		if err := emitAgyTerminalSnapshot(ctx, session.tmuxSessionName, opts.StreamChan); err != nil {
			return nil, fmt.Errorf("publish agy launch terminal: %w", err)
		}
		gi := &llmtypes.GenerationInfo{}
		llmtypes.AttachCodingProviderSessionHandle(gi, llmtypes.CodingProviderSessionHandle{
			Provider:        "agy-cli",
			Transport:       llmtypes.CodingProviderTransportTmux,
			NativeSessionID: session.getConversationID(),
			TmuxSession:     session.tmuxSessionName,
			WorkingDir:      workdir,
			Model:           model,
			Status:          llmtypes.CodingProviderSessionStatusIdle,
		})
		return &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{GenerationInfo: gi}}}, nil
	}
	stopTerminal := streamAgyTerminal(ctx, session.tmuxSessionName, opts.StreamChan)
	defer stopTerminal()
	reply, usage, toolCalls, compactions, modelCalls, err := runAgyInteractiveTurn(ctx, owner, prompt)
	if err != nil {
		if ctx.Err() != nil {
			// A cancellation can land before the sidecar reaches its busy
			// wait, or while Escape is still being handled. Retiring the
			// process prevents the next message for this owner from queuing
			// behind the abandoned turn.
			CloseAgyCLIInteractiveSessionForOwner(owner, "turn canceled")
		}
		return nil, err
	}
	if opts != nil && opts.StreamChan != nil {
		// Post-hoc tool events: the sidecar never streams live, so each
		// real invocation from the .db goes out as a Start,End pair in
		// completion order, ahead of the final content chunk. Context
		// compactions recorded during the turn go first, the same way.
		for _, compaction := range compactions {
			opts.StreamChan <- llmtypes.ContextCompactionChunk(compaction)
		}
		for _, call := range toolCalls {
			opts.StreamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallStart, ToolName: call.Name, ToolArgs: call.Args, ToolCallID: call.CallID}
			opts.StreamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallEnd, ToolName: call.Name, ToolCallID: call.CallID, ToolResult: call.ErrorText}
		}
		opts.StreamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: reply}
	}
	gi := &llmtypes.GenerationInfo{}
	llmtypes.AttachCodingProviderSessionHandle(gi, llmtypes.CodingProviderSessionHandle{
		Provider:        "agy-cli",
		Transport:       llmtypes.CodingProviderTransportTmux,
		NativeSessionID: session.getConversationID(),
		TmuxSession:     session.tmuxSessionName,
		WorkingDir:      workdir,
		Model:           model,
	})
	agyAttachCost(gi, model, usage)
	gi.Additional["agy_intake_source"] = "sqlite_user_step"
	gi.Additional["agy_completion_source"] = "sqlite_assistant_settled"
	if modelCalls > 0 {
		gi.Additional["llm_call_count"] = modelCalls
		gi.Additional["llm_call_count_source"] = "native_model_generations"
	}
	return &llmtypes.ContentResponse{
		Choices: []*llmtypes.ContentChoice{{
			Content:        reply,
			StopReason:     "completed",
			GenerationInfo: gi,
		}},
		Usage: &usage,
	}, nil
}
