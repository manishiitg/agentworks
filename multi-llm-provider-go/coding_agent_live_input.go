package llmproviders

import (
	"context"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"strings"
)

// SendCodingAgentLiveInput sends a user message to a currently running coding
// agent transport. Host applications should call this typed entry point instead
// of switching over provider-specific tmux implementations themselves.
func SendCodingAgentLiveInput(ctx context.Context, provider Provider, modelID, ownerSessionID, message string) error {
	return sendCodingAgentInput(ctx, provider, modelID, ownerSessionID, message, false)
}

// SendCodingAgentRetainedInput starts a new logical turn in a retained coding
// terminal. Pi needs this distinction because live steering is accepted while
// busy, whereas retained recovery must wait for the idle composer.
func SendCodingAgentRetainedInput(ctx context.Context, provider Provider, modelID, ownerSessionID, message string) error {
	return sendCodingAgentInput(ctx, provider, modelID, ownerSessionID, message, true)
}

func sendCodingAgentInput(ctx context.Context, provider Provider, modelID, ownerSessionID, message string, retainedTurn bool) error {
	normalizedProvider := Provider(strings.ToLower(strings.TrimSpace(string(provider))))
	contract, ok := GetCodingAgentProviderContract(normalizedProvider, modelID)
	if !ok {
		return &CodingAgentContinuationError{
			Kind:     CodingAgentContinuationErrorNonApplicable,
			Provider: normalizedProvider,
			Reason:   "provider is not a coding-agent provider",
		}
	}
	if !contract.SupportsLiveInput {
		return &CodingAgentContinuationError{
			Kind:     CodingAgentContinuationErrorNonContinuable,
			Provider: normalizedProvider,
			Reason:   "provider transport does not support live input",
		}
	}
	if strings.TrimSpace(ownerSessionID) == "" {
		return &CodingAgentContinuationError{
			Kind:     CodingAgentContinuationErrorNonContinuable,
			Provider: normalizedProvider,
			Reason:   "owner session id is required",
		}
	}
	if strings.TrimSpace(message) == "" {
		return &CodingAgentContinuationError{
			Kind:     CodingAgentContinuationErrorNonContinuable,
			Provider: normalizedProvider,
			Reason:   "message is empty",
		}
	}

	switch normalizedProvider {
	case ProviderClaudeCode:
		// Each chat chooses its Claude transport: send to the one running for it.
		if claudecode.ClaudeSDKSessionActive(ownerSessionID) {
			return claudecode.SendClaudeCodeSDKInput(ctx, ownerSessionID, message)
		}
		return SendClaudeCodeInput(ctx, ownerSessionID, message)
	case ProviderCursorCLI:
		if contract.Transport == CodingAgentTransportTmux {
			return cursorcli.SendCursorInteractiveInput(ctx, ownerSessionID, message)
		}
		return cursorcli.SendCursorSDKInput(ctx, ownerSessionID, message)
	case ProviderCodexCLI:
		return SendCodexCLIInteractiveInput(ctx, ownerSessionID, message)
	case ProviderPiCLI:
		if retainedTurn {
			return fmt.Errorf("Pi idle turns use the normal Run path")
		}
		return SendPiCLIInteractiveInput(ctx, ownerSessionID, message)
	case ProviderMuseCLI:
		return SendMuseCLIInteractiveInput(ctx, ownerSessionID, message)
	case ProviderAgyCLI:
		return SendAgyCLIInteractiveInput(ctx, ownerSessionID, message)
	default:
		return &CodingAgentContinuationError{
			Kind:     CodingAgentContinuationErrorNonContinuable,
			Provider: normalizedProvider,
			Reason:   "provider has no live input transport",
		}
	}
}
