package llmproviders

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type CodingAgentContinuationErrorKind string

const (
	CodingAgentContinuationErrorNonApplicable  CodingAgentContinuationErrorKind = "non_applicable"
	CodingAgentContinuationErrorNonContinuable CodingAgentContinuationErrorKind = "non_continuable"
	CodingAgentContinuationErrorStaleHandle    CodingAgentContinuationErrorKind = "stale_handle"
)

type CodingAgentContinuationError struct {
	Kind     CodingAgentContinuationErrorKind
	Provider Provider
	Reason   string
}

func (e *CodingAgentContinuationError) Error() string {
	if e == nil {
		return ""
	}
	provider := strings.TrimSpace(string(e.Provider))
	if provider == "" {
		provider = "unknown"
	}
	if strings.TrimSpace(e.Reason) == "" {
		return fmt.Sprintf("coding-agent continuation %s for provider %s", e.Kind, provider)
	}
	return fmt.Sprintf("coding-agent continuation %s for provider %s: %s", e.Kind, provider, e.Reason)
}

func IsCodingAgentContinuationError(err error, kind CodingAgentContinuationErrorKind) bool {
	var continuationErr *CodingAgentContinuationError
	return errors.As(err, &continuationErr) && continuationErr.Kind == kind
}

// ContinueCodingAgentSession continues a provider-native coding-agent session
// using only the latest user message. It owns translating the opaque provider
// handle into provider-specific resume/workdir options; callers should not
// construct Claude/Codex/Gemini resume flags themselves.
func ContinueCodingAgentSession(ctx context.Context, model llmtypes.Model, handle llmtypes.CodingProviderSessionHandle, message string, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	if model == nil {
		return nil, &CodingAgentContinuationError{Kind: CodingAgentContinuationErrorNonContinuable, Reason: "model is nil"}
	}
	provider := Provider(strings.ToLower(strings.TrimSpace(handle.Provider)))
	if provider == "" {
		return nil, &CodingAgentContinuationError{Kind: CodingAgentContinuationErrorNonContinuable, Reason: "provider handle is missing provider"}
	}
	if _, ok := GetCodingAgentProviderContract(provider, strings.TrimSpace(handle.Model)); !ok {
		return nil, &CodingAgentContinuationError{
			Kind:     CodingAgentContinuationErrorNonApplicable,
			Provider: provider,
			Reason:   "provider is not a coding-agent provider",
		}
	}
	if strings.TrimSpace(message) == "" {
		return nil, &CodingAgentContinuationError{Kind: CodingAgentContinuationErrorNonContinuable, Provider: provider, Reason: "message is empty"}
	}
	if strings.TrimSpace(handle.NativeSessionID) == "" {
		return nil, &CodingAgentContinuationError{
			Kind:     CodingAgentContinuationErrorNonContinuable,
			Provider: provider,
			Reason:   "provider handle is missing native session id",
		}
	}

	continuationOptions, err := appendCodingAgentContinuationOptions(provider, handle, options)
	if err != nil {
		return nil, err
	}
	// Prepend a System message when the caller provided one via
	// WithCodingProviderLaunchSystemPrompt. Without this, every
	// continuation turn arrives at the adapter with only a Human
	// message → split*SystemPrompt returns empty → prepare*ProjectFiles
	// skips writing the rule file (.cursor/rules/mlp-system.mdc,
	// .agents/rules/mlp-system.md, AGENTS.md, etc.) → the adapter's
	// "launch configuration changed" guard tears down the prior tmux
	// session and re-acquires with empty systemPrompt mid-conversation.
	probe := &llmtypes.CallOptions{}
	for _, opt := range continuationOptions {
		opt(probe)
	}
	messages := []llmtypes.MessageContent{}
	if systemPrompt := strings.TrimSpace(llmtypes.CodingProviderLaunchSystemPromptFromOptions(probe)); systemPrompt != "" {
		messages = append(messages, llmtypes.MessageContent{
			Role:  llmtypes.ChatMessageTypeSystem,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: systemPrompt}},
		})
	}
	messages = append(messages, llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, message))
	resp, err := model.GenerateContent(ctx, messages, continuationOptions...)
	if err == nil || !llmtypes.IsCodingAgentTmuxSessionLostError(err) {
		return resp, err
	}

	// A killed tmux pane can leave a process-local persistent-session registry
	// pointing at a dead pane for one call. Adapters clean up that stale mapping
	// when the first call fails; retry once so the continuation can start a fresh
	// tmux TUI with the provider-native resume id. Disable StreamChan on the
	// retry because the first failed GenerateContent owns closing it.
	retryOptions := append([]llmtypes.CallOption{}, continuationOptions...)
	retryOptions = append(retryOptions, llmtypes.WithStreamingChan(nil))
	resp, retryErr := model.GenerateContent(ctx, messages, retryOptions...)
	if retryErr != nil {
		return nil, fmt.Errorf("coding-agent continuation retry after tmux loss failed: first error: %w; retry error: %w", err, retryErr)
	}
	return resp, nil
}

// StartCodingAgentTransportSession starts or reacquires the provider's
// launchable coding-agent transport without sending a user prompt. The current
// launchable terminal transport is tmux; callers should gate on the persisted
// handle/contract transport instead of checking provider names.
func StartCodingAgentTransportSession(ctx context.Context, model llmtypes.Model, handle llmtypes.CodingProviderSessionHandle, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	if model == nil {
		return nil, &CodingAgentContinuationError{Kind: CodingAgentContinuationErrorNonContinuable, Reason: "model is nil"}
	}
	provider := Provider(strings.ToLower(strings.TrimSpace(handle.Provider)))
	if provider == "" {
		return nil, &CodingAgentContinuationError{Kind: CodingAgentContinuationErrorNonContinuable, Reason: "provider handle is missing provider"}
	}
	contract, ok := GetCodingAgentProviderContract(provider, strings.TrimSpace(handle.Model))
	if !ok {
		return nil, &CodingAgentContinuationError{
			Kind:     CodingAgentContinuationErrorNonApplicable,
			Provider: provider,
			Reason:   "provider is not a coding-agent provider",
		}
	}

	transport := contract.Transport
	if handleTransport := strings.TrimSpace(handle.Transport); handleTransport != "" {
		transport = CodingAgentTransport(handleTransport)
	}
	switch transport {
	case CodingAgentTransportTmux:
		return startCodingAgentTmuxTransportSession(ctx, model, provider, handle, options...)
	default:
		return nil, &CodingAgentContinuationError{
			Kind:     CodingAgentContinuationErrorNonApplicable,
			Provider: provider,
			Reason:   fmt.Sprintf("transport %q does not expose a launchable terminal session", transport),
		}
	}
}

// StartCodingAgentTmuxSession starts or reacquires a tmux-backed coding-agent
// TUI for a provider-native continuation handle without sending a user prompt.
func StartCodingAgentTmuxSession(ctx context.Context, model llmtypes.Model, handle llmtypes.CodingProviderSessionHandle, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	return StartCodingAgentTransportSession(ctx, model, handle, options...)
}

// CompactCodingAgentSession runs only the CLI's own compaction on a
// provider-native session (idle compaction, PLAT-872): no person message is
// sent. The response carries an llmtypes.IdleCompactResult; a provider or
// transport without a reliable native trigger answers reason=unsupported
// without starting anything.
func CompactCodingAgentSession(ctx context.Context, model llmtypes.Model, handle llmtypes.CodingProviderSessionHandle, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	if model == nil {
		return nil, &CodingAgentContinuationError{Kind: CodingAgentContinuationErrorNonContinuable, Reason: "model is nil"}
	}
	provider := Provider(strings.ToLower(strings.TrimSpace(handle.Provider)))
	contract, ok := GetCodingAgentProviderContract(provider, strings.TrimSpace(handle.Model))
	if !ok {
		return nil, &CodingAgentContinuationError{Kind: CodingAgentContinuationErrorNonApplicable, Provider: provider, Reason: "provider is not a coding-agent provider"}
	}
	if strings.TrimSpace(handle.NativeSessionID) == "" {
		return llmtypes.IdleCompactSkipResponse(string(provider), llmtypes.IdleCompactReasonNoSession), nil
	}
	transport := strings.TrimSpace(handle.Transport)
	if transport == "" {
		transport = string(contract.Transport)
	}
	if !IdleCompactSupported(provider, transport) {
		return llmtypes.IdleCompactSkipResponse(string(provider), llmtypes.IdleCompactReasonUnsupported), nil
	}
	compactOptions, err := appendCodingAgentContinuationOptions(provider, handle, options)
	if err != nil {
		return nil, err
	}
	compactOptions = append(compactOptions, llmtypes.WithCodingProviderIdleCompact())
	probe := &llmtypes.CallOptions{}
	for _, opt := range compactOptions {
		opt(probe)
	}
	// The system prompt keeps the launch identical to the chat's own turns, so
	// the resumed conversation's prompt prefix (and its provider cache) match.
	var messages []llmtypes.MessageContent
	if systemPrompt := strings.TrimSpace(llmtypes.CodingProviderLaunchSystemPromptFromOptions(probe)); systemPrompt != "" {
		messages = append(messages, llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: systemPrompt}}})
	}
	return model.GenerateContent(ctx, messages, compactOptions...)
}

// IdleCompactSupported reports whether a provider transport has a reliable
// native compaction trigger: Claude Code (/compact on its SDK, stream-json and
// tmux transports), Codex app-server (thread/compact/start), Pi RPC
// (compact), Muse serve (session/compact) and the Cursor SDK (summarize).
// agy over ACP (/compact). Codex tmux/exec, Cursor tmux, Muse tmux/exec and
// agy's terminal lane have none.
func IdleCompactSupported(provider Provider, transport string) bool {
	switch normalizeCodingAgentProvider(provider) {
	case ProviderClaudeCode:
		switch transport {
		case llmtypes.CodingProviderTransportSDK, llmtypes.CodingProviderTransportStructured, llmtypes.CodingProviderTransportTmux:
			return true
		}
	case ProviderCodexCLI:
		return transport == llmtypes.CodingProviderTransportAppServer
	case ProviderPiCLI:
		return transport == llmtypes.CodingProviderTransportRPC
	case ProviderMuseCLI:
		return transport == llmtypes.CodingProviderTransportMSP
	case ProviderCursorCLI:
		// Cursor SDK 1.3 agent.summarize() (agentworks#3135).
		return transport == llmtypes.CodingProviderTransportSDK
	case ProviderAgyCLI:
		// agy acp offers its own /compact command.
		return transport == llmtypes.CodingProviderTransportACP
	}
	return false
}

func startCodingAgentTmuxTransportSession(ctx context.Context, model llmtypes.Model, provider Provider, handle llmtypes.CodingProviderSessionHandle, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	if strings.TrimSpace(handle.NativeSessionID) == "" {
		return nil, &CodingAgentContinuationError{
			Kind:     CodingAgentContinuationErrorNonContinuable,
			Provider: provider,
			Reason:   "provider handle is missing native session id",
		}
	}

	launchOptions, err := appendCodingAgentContinuationOptions(provider, handle, options)
	if err != nil {
		return nil, err
	}
	launchOptions = append(launchOptions, llmtypes.WithCodingProviderLaunchOnly())
	// Build a single-system-message slice when the caller passed
	// WithCodingProviderLaunchSystemPrompt so the adapter's
	// split*SystemPrompt extracts it and prepare*ProjectFiles writes
	// the provider-specific rule file (.cursor/rules/mlp-system.mdc,
	// .agents/rules/mlp-system.md, AGENTS.md, GEMINI.md, etc.). Without
	// this, launch-only sends nil messages → empty systemPrompt → rule
	// file never written for the resumed session.
	probe := &llmtypes.CallOptions{}
	for _, opt := range launchOptions {
		opt(probe)
	}
	launchMessages := []llmtypes.MessageContent(nil)
	if systemPrompt := strings.TrimSpace(llmtypes.CodingProviderLaunchSystemPromptFromOptions(probe)); systemPrompt != "" {
		launchMessages = []llmtypes.MessageContent{
			{
				Role:  llmtypes.ChatMessageTypeSystem,
				Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: systemPrompt}},
			},
		}
	}
	return model.GenerateContent(ctx, launchMessages, launchOptions...)
}

func appendCodingAgentContinuationOptions(provider Provider, handle llmtypes.CodingProviderSessionHandle, options []llmtypes.CallOption) ([]llmtypes.CallOption, error) {
	resumeID := strings.TrimSpace(handle.NativeSessionID)
	workingDir := strings.TrimSpace(handle.WorkingDir)
	projectDirID := strings.TrimSpace(handle.ProjectDirID)

	out := append([]llmtypes.CallOption{}, options...)
	resumeOption := NativeResumeOption(provider, resumeID)
	if resumeOption == nil {
		return nil, &CodingAgentContinuationError{Kind: CodingAgentContinuationErrorNonApplicable, Provider: provider, Reason: "provider-native coding-agent continuation is not implemented"}
	}
	out = append(out, resumeOption, llmtypes.WithCodingProviderTransport(handle.Transport))
	if handle.RuntimeDir != "" {
		out = append(out, llmtypes.WithCLIRuntimeDir(handle.RuntimeDir))
	}

	if workingDir != "" {
		if option := CodingAgentWorkingDirOption(provider, workingDir); option != nil {
			out = append(out, option)
		}
	}
	// Codex's ProjectDirID option is also its cwd option; do not let an older
	// ProjectDirID override a fresher WorkingDir. Gemini's ProjectDirID is
	// separate from cwd and remains useful even when WorkingDir is present.
	if projectDirID != "" && (normalizeCodingAgentProvider(provider) != ProviderCodexCLI || workingDir == "") {
		if option := CodingAgentProjectDirIDOption(provider, projectDirID); option != nil {
			out = append(out, option)
		}
	}
	return out, nil
}
