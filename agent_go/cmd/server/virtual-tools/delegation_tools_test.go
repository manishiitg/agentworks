package virtualtools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildCLIToolEnvironmentPromptUsesProviderSpecificBridgeToolNames(t *testing.T) {
	tests := []struct {
		provider string
		want     string
		forbid   string
	}{
		{
			provider: "claude-code",
			want:     "mcp__api-bridge__execute_shell_command",
			forbid:   "mcp_api-bridge_execute_shell_command",
		},
		{
			provider: "codex-cli",
			want:     "mcp_api-bridge_execute_shell_command",
			forbid:   "mcp__api-bridge__execute_shell_command",
		},
		{
			provider: "codex-cli",
			want:     "mcp_api-bridge_execute_shell_command",
			forbid:   "mcp__api-bridge__execute_shell_command",
		},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := BuildCLIToolEnvironmentPrompt(tt.provider)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("prompt missing provider bridge tool %q:\n%s", tt.want, got)
			}
			if strings.Contains(got, tt.forbid) {
				t.Fatalf("prompt contains wrong provider bridge tool %q:\n%s", tt.forbid, got)
			}
		})
	}
}

func TestBuildCLIToolEnvironmentPromptIncludesLLMConfigCurlRouting(t *testing.T) {
	got := BuildCLIToolEnvironmentPrompt("claude-code")
	for _, want := range []string{
		"LLM config tools",
		"list_published_llms",
		"$MCP_CUSTOM/list_provider_models",
		"Do **NOT** read or edit `config/` files for LLM/provider configuration",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q:\n%s", want, got)
		}
	}
}

func TestHandleDelegatePrefersAsyncBackgroundDelegate(t *testing.T) {
	tests := []struct {
		name      string
		wrapValue func(BackgroundDelegateFunc) interface{}
	}{
		{
			name: "named background delegate func",
			wrapValue: func(fn BackgroundDelegateFunc) interface{} {
				return fn
			},
		},
		{
			name: "plain compatible function",
			wrapValue: func(fn BackgroundDelegateFunc) interface{} {
				return func(ctx context.Context, name, instruction string) (string, error) {
					return fn(ctx, name, instruction)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			started := make(chan struct{}, 1)
			executeCalled := make(chan struct{}, 1)

			bgDelegate := BackgroundDelegateFunc(func(_ context.Context, name, instruction string) (string, error) {
				if name != "bg-contract-check" {
					t.Fatalf("unexpected background agent name: %q", name)
				}
				if instruction != "sleep briefly" {
					t.Fatalf("unexpected instruction: %q", instruction)
				}
				started <- struct{}{}
				return "bg-agent-123", nil
			})

			ctx := context.WithValue(context.Background(), BackgroundDelegateKey, tt.wrapValue(bgDelegate))
			ctx = context.WithValue(ctx, ExecuteDelegatedTaskKey, ExecuteDelegatedTaskFunc(func(context.Context, string) (string, error) {
				executeCalled <- struct{}{}
				time.Sleep(time.Second)
				return "blocking result", nil
			}))

			result, err := handleDelegate(ctx, map[string]interface{}{
				"name":            "bg-contract-check",
				"instruction":     "sleep briefly",
				"reasoning_level": "low",
			})
			if err != nil {
				t.Fatalf("handleDelegate returned error: %v", err)
			}

			select {
			case <-started:
			default:
				t.Fatalf("background delegate was not invoked")
			}
			select {
			case <-executeCalled:
				t.Fatalf("blocking delegated task executor was called despite async delegate being available")
			default:
			}

			var parsed struct {
				Async   bool   `json:"async"`
				AgentID string `json:"agent_id"`
				Status  string `json:"status"`
			}
			if err := json.Unmarshal([]byte(result), &parsed); err != nil {
				t.Fatalf("result is not JSON: %v\n%s", err, result)
			}
			if !parsed.Async || parsed.AgentID != "bg-agent-123" || parsed.Status != "running" {
				t.Fatalf("unexpected async delegate result: %+v", parsed)
			}
		})
	}
}

// TestHandleDelegateBuildsChildSpec verifies that delegate(...) translates its
// tool args into the SubAgentSpec the sub-agent creation path consumes:
// additive skills, server restriction, tier, and the
// incremented depth. If this propagation breaks, sub-agents silently get the
// defaults with no diagnostic — this test catches it at the boundary.
func TestHandleDelegateBuildsChildSpec(t *testing.T) {
	var captured SubAgentSpec

	// The async background-delegate path captures the ctx the handler builds.
	bgDelegate := BackgroundDelegateFunc(func(ctx context.Context, name, instruction string) (string, error) {
		captured = SubAgentSpecFromContext(ctx)
		return "agent-xyz", nil
	})

	parentSpec := SubAgentSpec{Depth: 1}
	ctx := context.WithValue(context.Background(), BackgroundDelegateKey, bgDelegate)
	ctx = WithSubAgentSpec(ctx, parentSpec)

	_, err := handleDelegate(ctx, map[string]interface{}{
		"name":            "test-spec-pass",
		"instruction":     "do the thing",
		"reasoning_level": "low",
		"skills":          []interface{}{"pdf-extract", "agent-browser"},
		"servers":         []interface{}{"github"},
	})
	if err != nil {
		t.Fatalf("handleDelegate returned error: %v", err)
	}

	if len(captured.Skills) != 2 || captured.Skills[0] != "pdf-extract" || captured.Skills[1] != "agent-browser" {
		t.Errorf("expected skills [pdf-extract agent-browser], got %v", captured.Skills)
	}
	if len(captured.Servers) != 1 || captured.Servers[0] != "github" {
		t.Errorf("expected servers [github], got %v", captured.Servers)
	}
	if captured.ReasoningLevel != "low" {
		t.Errorf("expected reasoning level low, got %q", captured.ReasoningLevel)
	}
	if captured.Depth != parentSpec.Depth+1 {
		t.Errorf("expected child depth %d, got %d", parentSpec.Depth+1, captured.Depth)
	}
}

// TestHandleDelegateNoSkillsArgMeansNoAdditionalSkills verifies that the
// typed spec stays empty when a call requests no additions. Parent-skill
// inheritance is carried independently by the server launch context.
func TestHandleDelegateNoSkillsArgMeansNoAdditionalSkills(t *testing.T) {
	var captured SubAgentSpec

	bgDelegate := BackgroundDelegateFunc(func(ctx context.Context, _, _ string) (string, error) {
		captured = SubAgentSpecFromContext(ctx)
		return "agent-xyz", nil
	})

	ctx := context.WithValue(context.Background(), BackgroundDelegateKey, bgDelegate)

	_, err := handleDelegate(ctx, map[string]interface{}{
		"name":            "no-skills",
		"instruction":     "do the thing",
		"reasoning_level": "low",
		// no "skills" key
	})
	if err != nil {
		t.Fatalf("handleDelegate returned error: %v", err)
	}

	if len(captured.Skills) != 0 {
		t.Errorf("expected no skills in child spec when args has no skills; got %v", captured.Skills)
	}
}

// TestHandleDelegateEnforcesMaxDepth verifies the recursion guard reads the
// depth from the SubAgentSpec.
func TestHandleDelegateEnforcesMaxDepth(t *testing.T) {
	bgDelegate := BackgroundDelegateFunc(func(context.Context, string, string) (string, error) {
		t.Fatal("delegate must not run at max depth")
		return "", nil
	})
	ctx := context.WithValue(context.Background(), BackgroundDelegateKey, bgDelegate)
	ctx = WithSubAgentSpec(ctx, SubAgentSpec{Depth: MaxDelegationDepth})

	_, err := handleDelegate(ctx, map[string]interface{}{
		"name":            "too-deep",
		"instruction":     "do the thing",
		"reasoning_level": "low",
	})
	if err == nil || !strings.Contains(err.Error(), "maximum delegation depth") {
		t.Fatalf("expected max-depth error, got: %v", err)
	}
}

// TestSubAgentSpecContextRoundTrip locks the spec accessor contract: defaults
// when absent (root depth), exact round-trip when set, and
// WithBackgroundAgentID preserving other fields.
func TestSubAgentSpecContextRoundTrip(t *testing.T) {
	def := SubAgentSpecFromContext(context.Background())
	if def.Depth != 0 || def.ReasoningLevel != "" || def.BackgroundAgentID != "" {
		t.Errorf("unexpected default spec: %+v", def)
	}

	want := SubAgentSpec{
		Depth:          2,
		ReasoningLevel: "high",
		AgentTemplate:  "researcher",
		Servers:        []string{"github"},
		Skills:         []string{"pdf-extract"},
	}
	ctx := WithSubAgentSpec(context.Background(), want)
	got := SubAgentSpecFromContext(ctx)
	if got.Depth != want.Depth || got.ReasoningLevel != want.ReasoningLevel ||
		got.AgentTemplate != want.AgentTemplate ||
		len(got.Servers) != 1 || len(got.Skills) != 1 {
		t.Errorf("spec round-trip mismatch: got %+v, want %+v", got, want)
	}

	linked := SubAgentSpecFromContext(WithBackgroundAgentID(ctx, "bg-42"))
	if linked.BackgroundAgentID != "bg-42" {
		t.Errorf("expected background agent ID bg-42, got %q", linked.BackgroundAgentID)
	}
	if linked.ReasoningLevel != "high" || linked.Depth != 2 {
		t.Errorf("WithBackgroundAgentID must preserve other fields, got %+v", linked)
	}
}

// This prompt is bridge-only by construction: it asserts the CLI's native tools
// are disabled and that execute_shell_command replaces them. That is true for an
// mcp_only profile and false for a hybrid one, so handleQuery must not inject it
// for hybrid.
//
// The claims are asserted here so the coupling is visible from this side too: if
// someone softens this text, the caller's hybrid guard may no longer be needed,
// and if someone strengthens it, the guard matters more. Codex read these exact
// sentences, concluded it had no shell, and reported the product broken while
// holding a working shell.
func TestCLIToolEnvironmentPromptClaimsNativeToolsAreDisabled(t *testing.T) {
	for _, provider := range []string{"claude-code", "codex-cli"} {
		prompt := BuildCLIToolEnvironmentPrompt(provider)
		for _, claim := range []string{
			"Your native tools (Bash, Read, Write, etc.) are **disabled**",
			"Run any shell command (replaces Bash/Read/Write)",
		} {
			if !strings.Contains(prompt, claim) {
				t.Fatalf("%s: expected the bridge-only claim %q; if this text changed, revisit the hybrid guard in handleQuery", provider, claim)
			}
		}
	}
}

func TestDelegateRejectsUnknownReasoningTier(t *testing.T) {
	_, err := handleDelegate(context.Background(), map[string]interface{}{
		"instruction": "Do the task", "reasoning_level": "missing-tier",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid reasoning_level") {
		t.Fatalf("unknown tier must fail before inheriting another model: %v", err)
	}
}
