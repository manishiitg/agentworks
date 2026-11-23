package testcontracts

import (
	"fmt"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// ToolReceiptCase is the P0 contract for a real coding-CLI tool turn. It is
// intentionally strict: merely emitting a start/end event is not useful to a
// consumer if identity, arguments, output, correlation, or the final answer
// disappeared between the CLI and the normalized stream.
type ToolReceiptCase struct {
	Provider          string
	Chunks            []llmtypes.StreamChunk
	FinalAnswer       string
	ArgumentSentinel  string
	ResultSentinel    string
	ExpectedToolNames []string
}

// AssertToolReceiptContract verifies the complete consumer-visible receipt
// produced by a real tool call. Provider E2Es should use this shared assertion
// instead of weaker event-count checks.
func AssertToolReceiptContract(t *testing.T, c ToolReceiptCase) {
	t.Helper()
	if err := ValidateToolReceiptContract(c); err != nil {
		t.Fatal(err)
	}
}

// ValidateToolReceiptContract contains the actual contract so its rejection
// behavior can itself be unit-tested. This prevents a future edit from making
// the shared P0 assertion shallow while every provider test still appears green.
func ValidateToolReceiptContract(c ToolReceiptCase) error {
	provider := strings.TrimSpace(c.Provider)
	if provider == "" {
		provider = "coding CLI"
	}
	if strings.TrimSpace(c.ArgumentSentinel) == "" || strings.TrimSpace(c.ResultSentinel) == "" {
		return fmt.Errorf("%s P0 contract is invalid: argument and result sentinels are required", provider)
	}

	starts := make(map[string]llmtypes.StreamChunk)
	ends := make(map[string]llmtypes.StreamChunk)
	for _, chunk := range c.Chunks {
		switch chunk.Type {
		case llmtypes.StreamChunkTypeToolCallStart:
			if err := validateCompleteToolIdentity(provider, "start", chunk); err != nil {
				return err
			}
			if _, exists := starts[chunk.ToolCallID]; exists {
				return fmt.Errorf("%s emitted duplicate tool start for call %q", provider, chunk.ToolCallID)
			}
			starts[chunk.ToolCallID] = chunk
		case llmtypes.StreamChunkTypeToolCallEnd:
			if err := validateCompleteToolIdentity(provider, "end", chunk); err != nil {
				return err
			}
			if strings.TrimSpace(chunk.ToolResult) == "" {
				return fmt.Errorf("%s tool end %q lost its output", provider, chunk.ToolCallID)
			}
			if chunk.ToolDuration <= 0 {
				return fmt.Errorf("%s tool end %q has non-positive duration %s", provider, chunk.ToolCallID, chunk.ToolDuration)
			}
			if _, exists := ends[chunk.ToolCallID]; exists {
				return fmt.Errorf("%s emitted duplicate tool end for call %q", provider, chunk.ToolCallID)
			}
			ends[chunk.ToolCallID] = chunk
		}
	}
	if len(starts) == 0 || len(ends) == 0 {
		return fmt.Errorf("%s emitted an incomplete tool lifecycle: starts=%d ends=%d", provider, len(starts), len(ends))
	}

	matchedSentinel := false
	for id, start := range starts {
		end, ok := ends[id]
		if !ok {
			return fmt.Errorf("%s tool start %q has no matching end", provider, id)
		}
		if start.ToolName != end.ToolName {
			return fmt.Errorf("%s tool identity changed for %q: start=%q end=%q", provider, id, start.ToolName, end.ToolName)
		}
		if start.ToolArgs != end.ToolArgs {
			return fmt.Errorf("%s tool arguments changed for %q: start=%q end=%q", provider, id, start.ToolArgs, end.ToolArgs)
		}
		if !allowedToolName(start.ToolName, c.ExpectedToolNames) {
			continue
		}
		if strings.Contains(start.ToolArgs, c.ArgumentSentinel) && strings.Contains(end.ToolResult, c.ResultSentinel) {
			matchedSentinel = true
		}
	}
	for id := range ends {
		if _, ok := starts[id]; !ok {
			return fmt.Errorf("%s tool end %q has no matching start", provider, id)
		}
	}
	if !matchedSentinel {
		return fmt.Errorf("%s emitted no matched tool receipt carrying argument %q and result %q", provider, c.ArgumentSentinel, c.ResultSentinel)
	}
	if strings.Count(c.FinalAnswer, c.ResultSentinel) != 1 {
		return fmt.Errorf("%s final answer must contain result %q exactly once; got %q", provider, c.ResultSentinel, c.FinalAnswer)
	}
	return nil
}

func validateCompleteToolIdentity(provider, phase string, chunk llmtypes.StreamChunk) error {
	if strings.TrimSpace(chunk.ToolCallID) == "" {
		return fmt.Errorf("%s tool %s lost its call ID", provider, phase)
	}
	name := strings.TrimSpace(chunk.ToolName)
	if name == "" || strings.EqualFold(name, "unknown") {
		return fmt.Errorf("%s tool %s %q has unusable name %q", provider, phase, chunk.ToolCallID, chunk.ToolName)
	}
	if strings.TrimSpace(chunk.ToolArgs) == "" {
		return fmt.Errorf("%s tool %s %q lost its arguments", provider, phase, chunk.ToolCallID)
	}
	return nil
}

func allowedToolName(name string, expected []string) bool {
	if len(expected) == 0 {
		return true
	}
	for _, candidate := range expected {
		if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}
