package mcpagent

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestExecuteLLMInnerLaunchOnlyRequiresNativeContinuationHandle(t *testing.T) {
	agent := &Agent{
		apiKeys: &llm.ProviderAPIKeys{},
	}
	model := LLMModel{
		Provider: string(llm.ProviderClaudeCode),
		ModelID:  "claude-opus-5",
	}

	resp, err := agent.executeLLMInner(context.Background(), model, nil, nil, true)
	if err == nil || !strings.Contains(err.Error(), "native session handle is missing") {
		t.Fatalf("launch-only error = %v, want missing native session handle", err)
	}
	if resp != nil {
		t.Fatalf("launch-only response = %#v, want nil", resp)
	}
}

// The rejection test above passes just as well against a guard that rejects
// unconditionally, so on its own it cannot show the guard admits the case it is
// supposed to. This pins the positive side: with a resolvable native handle the
// launch-only path must get PAST the guard.
//
// It deliberately asserts only "not rejected for a missing handle" rather than
// success — actually launching needs a real Claude Code CLI and tmux, which a
// unit test must not require. Whatever failure follows is downstream of the
// guard, and that is exactly the boundary under test.
func TestExecuteLLMInnerLaunchOnlyProceedsWithNativeContinuationHandle(t *testing.T) {
	model := LLMModel{
		Provider: string(llm.ProviderClaudeCode),
		ModelID:  "claude-opus-5",
	}
	agent := &Agent{
		apiKeys: &llm.ProviderAPIKeys{},
		codingProviderSessionHandle: llmtypes.CodingProviderSessionHandle{
			Provider:        string(llm.ProviderClaudeCode),
			NativeSessionID: "test-native-session-id",
			Model:           model.ModelID,
		},
	}

	// Guard the premise: if the contract stops advertising native resume for this
	// provider/model, the handle would not resolve and this test would silently
	// degrade into a second copy of the rejection test.
	if _, ok := agent.codingProviderContinuationHandleForModel(llm.ProviderClaudeCode, model.ModelID); !ok {
		t.Fatalf("precondition failed: seeded handle did not resolve for %s/%s", model.Provider, model.ModelID)
	}

	var err error
	reachedPastGuard := false
	func() {
		defer func() {
			// Everything after the guard needs a fully constructed Agent and a
			// real CLI/tmux, so this bare fixture panics down there. That panic is
			// not a failure — reaching code that far IS the assertion, because the
			// guard returns before any of it. Recover so the test reports the
			// boundary rather than dying at it.
			if recovered := recover(); recovered != nil {
				reachedPastGuard = true
			}
		}()
		_, err = agent.executeLLMInner(context.Background(), model, nil, nil, true)
	}()

	if err != nil && strings.Contains(err.Error(), "native session handle is missing") {
		t.Fatalf("launch-only rejected a resolvable native handle: %v", err)
	}
	if !reachedPastGuard && err == nil {
		// No panic and no error would mean the launch genuinely succeeded, which
		// this fixture cannot do — treat it as the test having stopped exercising
		// the intended path rather than as a pass.
		t.Fatal("launch-only returned success from a fixture that cannot launch; the test no longer reaches the guard")
	}
}
