package mcpagent

import (
	"context"
	"strings"
	"testing"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/nativeshell"
)

const testDefaultPreamble = "DEFAULT PREAMBLE FOR TEST"

func TestAppendBridgeRoutingInstructionsDefaultUnchanged(t *testing.T) {
	a := &Agent{}
	a.appendBridgeRoutingInstructions(testDefaultPreamble)

	got := a.instructions()
	if !strings.Contains(got, testDefaultPreamble) {
		t.Fatalf("expected default preamble in system prompt, got: %s", got)
	}
	if !strings.Contains(got, "IMPORTANT — bridge tool routing") {
		t.Fatalf("expected default bridgeRoutingExplicitInstructions text in system prompt, got: %s", got)
	}
	for _, want := range []string{
		`curl --fail-with-body -sS --json '<payload>' -H "$MCP_AUTH" "$MCP_CUSTOM/<tool>"`,
		"MCP_AUTH is already the complete `Authorization: Bearer ...` header",
		"--json already selects POST and Content-Type",
		"Keep curl unpiped to preserve its nonzero HTTP-failure status",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected compact MCP bridge rule %q in system prompt, got: %s", want, got)
		}
	}
}

func TestAppendBridgeRoutingInstructionsNamesDirectPlatformReadImage(t *testing.T) {
	a := &Agent{additionalBridgeTools: []string{"read_image"}}
	a.appendBridgeRoutingInstructions(testDefaultPreamble)

	got := a.instructions()
	for _, want := range []string{
		"read_image",
		"platform image analysis",
		"Only declared runtime tools are direct calls",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("direct read_image routing prompt missing %q: %s", want, got)
		}
	}
}

// Native MCP declares typed bridge tools and has no generic mcp() proxy.
// Custom HTTP tools must continue to route through the bridge shell and curl.
func TestAppendBridgeRoutingInstructionsNoLongerTeachesMcpWrapperSyntax(t *testing.T) {
	a := &Agent{}
	a.appendBridgeRoutingInstructions(testDefaultPreamble)

	got := a.instructions()
	if strings.Contains(got, "mcp({") {
		t.Fatalf("expected no mcp() wrapper syntax taught in system prompt (native MCP has no proxy), got: %s", got)
	}
	for _, want := range []string{
		"Call HTTP tools through execute_shell_command",
		"never a direct tool call by their bare name",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected custom-tools-via-curl-only guardrail %q in system prompt, got: %s", want, got)
		}
	}
}

func TestAppendBridgeRoutingInstructionsCustomOverride(t *testing.T) {
	custom := "MY CUSTOM ROUTING TEXT"
	a := &Agent{bridgeRoutingInstructionsOverride: &custom}
	a.appendBridgeRoutingInstructions(testDefaultPreamble)

	got := a.instructions()
	if !strings.Contains(got, custom) {
		t.Fatalf("expected custom override text in system prompt, got: %s", got)
	}
	if strings.Contains(got, testDefaultPreamble) {
		t.Fatalf("default preamble should NOT appear when overridden, got: %s", got)
	}
	if strings.Contains(got, "IMPORTANT — bridge tool routing") {
		t.Fatalf("default bridgeRoutingExplicitInstructions text should NOT appear when overridden, got: %s", got)
	}
}

func TestAppendBridgeRoutingInstructionsEmptyOverrideSuppresses(t *testing.T) {
	empty := ""
	a := &Agent{bridgeRoutingInstructionsOverride: &empty}
	a.appendBridgeRoutingInstructions(testDefaultPreamble)

	got := a.instructions()
	if got != "" {
		t.Fatalf("expected empty system prompt when override is \"\" (suppressed), got: %s", got)
	}
}

func TestWithBridgeRoutingInstructionsOptionSetsOverride(t *testing.T) {
	a := &Agent{}
	opt := withBridgeRoutingInstructions("custom text")
	opt(a)

	if a.bridgeRoutingInstructionsOverride == nil {
		t.Fatal("expected bridgeRoutingInstructionsOverride to be set")
	}
	if *a.bridgeRoutingInstructionsOverride != "custom text" {
		t.Fatalf("expected override value %q, got %q", "custom text", *a.bridgeRoutingInstructionsOverride)
	}
}

// The routing preamble describes the mode the chat actually got. A "full"
// request that is not confined is mcp_only (never native tools), and Pi stays
// bridge-only in every mode.
func TestCodingAgentProviderRoutingPreambleMatchesConfiguredToolMode(t *testing.T) {
	for _, provider := range []llmproviders.Provider{llmproviders.ProviderClaudeCode, llmproviders.ProviderMuseCLI, llmproviders.ProviderCodexCLI, llmproviders.ProviderCursorCLI, llmproviders.ProviderAgyCLI} {
		full := &Agent{codingAgentToolsMode: codingAgentToolsFull, provider: provider, cliSecurityPolicy: confinedTestPolicy()}
		if prompt := full.codingAgentProviderRoutingPreamble(); strings.Contains(prompt, "disabled for this session") || !strings.Contains(prompt, "Protected files") {
			t.Fatalf("%s full preamble must describe enabled native tools and protected files: %s", provider, prompt)
		}
		unconfined := &Agent{codingAgentToolsMode: codingAgentToolsFull, provider: provider}
		if prompt := unconfined.codingAgentProviderRoutingPreamble(); strings.Contains(prompt, "Protected files") {
			t.Fatalf("%s: an unconfined full request must read as mcp_only: %s", provider, prompt)
		}
	}
	piFull := &Agent{codingAgentToolsMode: codingAgentToolsFull, provider: llmproviders.ProviderPiCLI, cliSecurityPolicy: confinedTestPolicy()}
	if got := piFull.codingAgentProviderRoutingPreamble(); !strings.Contains(got, "tools are disabled") {
		t.Fatalf("pi preamble must stay bridge-only: %s", got)
	}
	mcpOnly := &Agent{codingAgentToolsMode: codingAgentToolsMCPOnly}
	if got := mcpOnly.codingAgentProviderRoutingPreamble(); !strings.Contains(got, "Provider-native filesystem, shell, edit, and browser tools are disabled") {
		t.Fatalf("mcp-only routing preamble does not describe disabled native tools: %s", got)
	}
}

func TestCodingAgentProviderRoutingPromptDoesNotNameExcludedBridgeTools(t *testing.T) {
	a := &Agent{
		provider:             llmproviders.ProviderClaudeCode,
		codingAgentToolsMode: codingAgentToolsFull,
		cliSecurityPolicy:    confinedTestPolicy(),
		bridgeToolAdmit: func(name string) bool {
			return name != "execute_shell_command" && name != "diff_patch_workspace_file"
		},
	}
	a.appendBridgeRoutingInstructions(a.codingAgentProviderRoutingPreamble())
	got := a.instructions()
	for _, excluded := range []string{"execute_shell_command", "diff_patch_workspace_file"} {
		if strings.Contains(got, excluded) {
			t.Fatalf("routing prompt advertised excluded tool %q: %s", excluded, got)
		}
	}
	if !strings.Contains(got, "get_api_spec") {
		t.Fatalf("routing prompt lost the always-available discovery tool: %s", got)
	}
}

// The direct list is not the whole platform catalog, but a tool can actually
// be absent from the current chat. Discovery must not become an endless search.
func TestBridgeRoutingBlockDistinguishesDirectToolsFromUnavailableTools(t *testing.T) {
	a := &Agent{}
	a.appendBridgeRoutingInstructions(testDefaultPreamble)
	got := a.instructions()
	want := "A tool absent from your direct tool list may still be available through search_tools."
	if !strings.Contains(got, want) {
		t.Fatalf("the bridge routing block does not say %q:\n%s", want, got)
	}
	if !strings.Contains(got, "explain that it is unavailable in this chat; do not keep searching") {
		t.Fatal("discovery must stop when the current inventory has no such tool")
	}
	if !strings.Contains(got, "get_api_spec(tool_name=\"search_tools\") returns its session-scoped virtual route and schema") {
		t.Fatal("runtime discovery lost its supported HTTP route guidance")
	}
}

// A coding CLI's own shell has no platform credentials; only the bridge shell does. Agents that reach
// for bash first (Muse in Full CLI mode) must be told, in the inline instructions and in the skill.
func TestRuntimeHTTPInstructionsSayNativeShellHasNoCredentials(t *testing.T) {
	for name, text := range map[string]string{
		"inline routing":     bridgeRoutingExplicitInstructions(nil),
		"runtime-http skill": runtimeHTTPInstructions,
	} {
		for _, want := range []string{"bridge shell", "no MCP_AUTH or MCP_CUSTOM", "missing or invalid Authorization header"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not contain %q", name, want)
			}
		}
	}
}

// A native bridge tool the agent really exposes (a guest's send_message) is named a
// direct tool; a name that is not registered is not claimed. Without this a session
// with no shell was told to find send_message through search_tools and HTTP.
func TestRoutingPromptNamesEveryExposedNativeBridgeTool(t *testing.T) {
	a := &Agent{additionalBridgeTools: []string{"send_message", "not_registered_anywhere"}}
	if err := a.registerCustomTool("send_message", "Send a message", map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}, func(ctx context.Context, args map[string]interface{}) (string, error) { return "", nil }, "agent_messaging"); err != nil {
		t.Fatal(err)
	}
	a.appendBridgeRoutingInstructions(testDefaultPreamble)
	got := a.instructions()
	if !strings.Contains(got, "agent_browser, send_message. Use the exact identifiers") {
		t.Fatalf("an exposed native bridge tool must be listed as a direct tool: %s", got)
	}
	if strings.Contains(got, "not_registered_anywhere") {
		t.Fatalf("a tool the agent does not have must not be claimed: %s", got)
	}
}

// The adapters keep non-shell native tools in Full mode but remove command
// execution by default (PLAT-491). The outbound prompt must describe that same
// switch, including every spelling accepted by the adapter's shared helper.
func TestFullCLIRoutingPromptFollowsNativeShellSetting(t *testing.T) {
	for _, provider := range []llmproviders.Provider{
		llmproviders.ProviderClaudeCode, llmproviders.ProviderCodexCLI,
		llmproviders.ProviderCursorCLI, llmproviders.ProviderMuseCLI, llmproviders.ProviderAgyCLI,
	} {
		t.Run(string(provider), func(t *testing.T) {
			a := &Agent{provider: provider, codingAgentToolsMode: codingAgentToolsFull, cliSecurityPolicy: confinedTestPolicy()}
			a.appendBridgeRoutingInstructions(a.codingAgentProviderRoutingPreamble())
			for _, setting := range []string{"", "off", "on", "1", "true", "yes"} {
				t.Run("shell="+setting, func(t *testing.T) {
					t.Setenv(nativeshell.EnvVar, setting)
					got := a.outgoingSystemPrompt()
					if strings.Contains(got, "Your native shell is disabled") == nativeshell.Enabled() {
						t.Fatalf("shell=%q disagrees with outbound prompt: %s", setting, got)
					}
					if !strings.Contains(got, "Protected files") || !strings.Contains(got, "execute_shell_command") {
						t.Fatalf("Full CLI lost its boundary or bridge shell: %s", got)
					}
					if !nativeshell.Enabled() {
						for _, stale := range []string{"Bash, Read", "own shell (in Codex", "own Shell, Read", "native file, shell", "including file reads, search, edits, shell"} {
							if strings.Contains(got, stale) {
								t.Fatalf("disabled native shell advertised by %s: %s", stale, got)
							}
						}
					}
				})
			}
		})
	}
}

// A local verification turn stopped without trying a granted managed write,
// because Codex treated its native read-only sandbox as a bridge denial.
func TestCLIRoutingDistinguishesNativeSandboxFromGrantedBridgeWrites(t *testing.T) {
	for _, provider := range []llmproviders.Provider{
		llmproviders.ProviderClaudeCode, llmproviders.ProviderCodexCLI,
		llmproviders.ProviderCursorCLI, llmproviders.ProviderMuseCLI,
		llmproviders.ProviderAgyCLI, llmproviders.ProviderPiCLI,
	} {
		t.Run(string(provider), func(t *testing.T) {
			for _, mode := range []string{codingAgentToolsMCPOnly, codingAgentToolsFull} {
				t.Run(mode, func(t *testing.T) {
					for _, setting := range []string{"", "on"} {
						t.Run("native-shell="+setting, func(t *testing.T) {
							t.Setenv(nativeshell.EnvVar, setting)
							a := &Agent{provider: provider, codingAgentToolsMode: mode, cliSecurityPolicy: confinedTestPolicy()}
							a.appendBridgeRoutingInstructions(a.codingAgentProviderRoutingPreamble())
							clarification := "do not infer a bridge write denial from the native sandbox"
							wants := []string{"sandbox_mode: read-only, apply to native tools", "bridge tools enforce their own grants", clarification}
							if mode == codingAgentToolsFull && provider != llmproviders.ProviderPiCLI {
								clarification = "Native writes follow the granted folders"
								wants = []string{clarification, "bridge tools independently enforce their own grants"}
								if got := a.outgoingSystemPrompt(); strings.Contains(got, "sandbox_mode: read-only") {
									t.Fatalf("Full CLI described as native read-only: %s", got)
								}
							}
							for _, want := range wants {
								if got := a.outgoingSystemPrompt(); !strings.Contains(got, want) {
									t.Fatalf("routing missing %q: %s", want, got)
								}
							}

							// A restricted turn must not claim a managed write route.
							ctx := context.WithValue(context.Background(), turnPolicyContextKey{}, normalizedToolPolicy{allowed: map[string]bool{"search_tools": true, "get_api_spec": true}})
							if got := a.outgoingSystemPromptForContext(ctx); strings.Contains(got, clarification) || strings.Contains(got, "execute_shell_command") || strings.Contains(got, "diff_patch_workspace_file") {
								t.Fatalf("restricted turn advertises managed writes: %s", got)
							}
							if got := a.outgoingSystemPrompt(); !strings.Contains(got, clarification) {
								t.Fatalf("restricted turn removed subsequent guidance: %s", got)
							}

							// MCP-only profiles retain the patch route without a shell;
							// Full CLI uses native file tools and omits the bridge patch.
							a.bridgeToolAdmit = func(name string) bool { return name != "execute_shell_command" }
							wantPatch := mode == codingAgentToolsMCPOnly || provider == llmproviders.ProviderPiCLI
							if got := a.outgoingSystemPrompt(); strings.Contains(got, clarification) != wantPatch || strings.Contains(got, "diff_patch_workspace_file") != wantPatch || strings.Contains(got, "execute_shell_command") {
								t.Fatalf("shell-excluded profile has incorrect write guidance (patch=%t): %s", wantPatch, got)
							}
							a.bridgeToolAdmit = func(name string) bool { return name != "execute_shell_command" && name != "diff_patch_workspace_file" }
							if got := a.outgoingSystemPrompt(); strings.Contains(got, clarification) {
								t.Fatalf("profile without write routes advertises them: %s", got)
							}
						})
					}
				})
			}
		})
	}
}
