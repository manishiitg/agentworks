package mcpagent

import (
	"slices"
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/agycli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
)

// For every CLI and both modes, the prompt the CLI gets must match the tools
// it is really launched with: full says its own tools are on (and the launch
// enables them); mcp_only says they are off (and the launch disables them).
func TestCodingAgentModesContract(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")
	type launch func(a *Agent) ([]llmtypes.CallOption, error)
	cases := []struct {
		provider llm.Provider
		launch   launch
		full     func(t *testing.T, got map[string]interface{})
		mcpOnly  func(t *testing.T, got map[string]interface{})
	}{
		{llm.ProviderClaudeCode, func(a *Agent) ([]llmtypes.CallOption, error) {
			return a.appendClaudeCodeIntegrationOptions(nil, LLMModel{})
		},
			func(t *testing.T, got map[string]interface{}) {
				tools, _ := got[claudecode.MetadataKeyTools].(string)
				for _, want := range []string{"Bash", "Write", "Edit", "Read", "Agent"} {
					if !slices.Contains(strings.Split(tools, ","), want) {
						t.Errorf("full Claude lacks %s: %s", want, tools)
					}
				}
				if got[claudecode.MetadataKeyDangerouslySkipPermissions] != true {
					t.Error("full Claude must not stop on permission prompts")
				}
			},
			func(t *testing.T, got map[string]interface{}) {
				if got[claudecode.MetadataKeyTools] != "WebSearch" {
					t.Errorf("mcp_only Claude tools = %v", got[claudecode.MetadataKeyTools])
				}
			}},
		{llm.ProviderCodexCLI, func(a *Agent) ([]llmtypes.CallOption, error) {
			return a.appendCodexCLIIntegrationOptions(nil, LLMModel{})
		},
			func(t *testing.T, got map[string]interface{}) {
				if _, off := got[codexcli.MetadataKeyDisableShellTool]; off {
					t.Error("full Codex lost its shell")
				}
				if got[codexcli.MetadataKeySandbox] != "workspace-write" {
					t.Errorf("full Codex sandbox = %v", got[codexcli.MetadataKeySandbox])
				}
			},
			func(t *testing.T, got map[string]interface{}) {
				if _, off := got[codexcli.MetadataKeyDisableShellTool]; !off {
					t.Error("mcp_only Codex kept its shell")
				}
				if got[codexcli.MetadataKeySandbox] != "read-only" {
					t.Errorf("mcp_only Codex sandbox = %v", got[codexcli.MetadataKeySandbox])
				}
			}},
		{llm.ProviderCursorCLI, func(a *Agent) ([]llmtypes.CallOption, error) { return a.appendCursorCLIIntegrationOptions(nil) },
			func(t *testing.T, got map[string]interface{}) {
				if got[cursorcli.MetadataKeyFullNativeTools] != true || got[cursorcli.MetadataKeyDenyBuiltinTools] != true {
					t.Errorf("full Cursor must use the full-mode hooks: %#v", got)
				}
			},
			func(t *testing.T, got map[string]interface{}) {
				if got[cursorcli.MetadataKeyDenyBuiltinTools] != true || got[cursorcli.MetadataKeyFullNativeTools] == true {
					t.Errorf("mcp_only Cursor must deny its builtins: %#v", got)
				}
			}},
		{llm.ProviderMuseCLI, func(a *Agent) ([]llmtypes.CallOption, error) { return a.appendMuseCLIIntegrationOptions(nil) },
			func(t *testing.T, got map[string]interface{}) {
				if _, set := got[musecli.MetadataKeyMuseToolAllowlist]; set {
					t.Error("full Muse must have no allowlist (no hook, no --disable flags)")
				}
			},
			func(t *testing.T, got map[string]interface{}) {
				if list, _ := got[musecli.MetadataKeyMuseToolAllowlist].([]string); !slices.Equal(list, []string{"web_search"}) {
					t.Errorf("mcp_only Muse allowlist = %v", list)
				}
			}},
		{llm.ProviderAgyCLI, func(a *Agent) ([]llmtypes.CallOption, error) { return a.appendAgyCLIIntegrationOptions(nil) },
			func(t *testing.T, got map[string]interface{}) {
				if got[agycli.MetadataKeyNativeToolsMode] != codingAgentToolsFull {
					t.Errorf("full Agy mode = %v", got[agycli.MetadataKeyNativeToolsMode])
				}
			},
			func(t *testing.T, got map[string]interface{}) {
				if got[agycli.MetadataKeyNativeToolsMode] != codingAgentToolsMCPOnly {
					t.Errorf("mcp_only Agy mode = %v", got[agycli.MetadataKeyNativeToolsMode])
				}
			}},
	}
	for _, tc := range cases {
		for _, mode := range []string{codingAgentToolsFull, codingAgentToolsMCPOnly} {
			t.Run(string(tc.provider)+"/"+mode, func(t *testing.T) {
				a := bridgeTestAgent()
				a.provider = tc.provider
				a.codingAgentToolsMode = mode
				a.cliSecurityPolicy = confinedTestPolicy()
				opts, err := tc.launch(a)
				if err != nil {
					t.Fatal(err)
				}
				got := metadataFromCallOptions(opts)
				prompt := a.codingAgentProviderRoutingPreamble()
				if mode == codingAgentToolsMCPOnly {
					tc.mcpOnly(t, got)
					if strings.Contains(prompt, "Protected files") || strings.Contains(prompt, "are enabled") && tc.provider != llm.ProviderMuseCLI {
						t.Errorf("mcp_only prompt claims native tools: %s", prompt)
					}
					return
				}
				tc.full(t, got)
				if !strings.Contains(prompt, "enabled") || !strings.Contains(prompt, "Protected files") || strings.Contains(prompt, "disabled for this session") {
					t.Errorf("full prompt does not describe the native tools it has: %s", prompt)
				}
			})
		}
	}
}

// Full mode edits natively; the bridge edit tool is offered only when native
// tools are off (and to Pi, which never has them).
func TestBridgeEditToolOnlyWithoutNativeTools(t *testing.T) {
	for _, tc := range []struct {
		provider llm.Provider
		mode     string
		want     bool
	}{
		{llm.ProviderClaudeCode, codingAgentToolsFull, false},
		{llm.ProviderCodexCLI, codingAgentToolsFull, false},
		{llm.ProviderCursorCLI, codingAgentToolsFull, false},
		{llm.ProviderMuseCLI, codingAgentToolsFull, false},
		{llm.ProviderClaudeCode, codingAgentToolsMCPOnly, true},
		{llm.ProviderPiCLI, codingAgentToolsFull, true},
	} {
		a := &Agent{provider: tc.provider, codingAgentToolsMode: tc.mode, cliSecurityPolicy: confinedTestPolicy()}
		if got := a.admitsCoreBridgeTool("diff_patch_workspace_file"); got != tc.want {
			t.Errorf("%s/%s: diff_patch admitted=%v, want %v", tc.provider, tc.mode, got, tc.want)
		}
		if !a.admitsCoreBridgeTool("execute_shell_command") {
			t.Errorf("%s/%s: the bridge shell must stay (HTTP tool routes run through it)", tc.provider, tc.mode)
		}
		if prompt := bridgeRoutingInstructions(a.admitsCoreBridgeTool, false); strings.Contains(prompt, "diff_patch_workspace_file") != tc.want {
			t.Errorf("%s/%s: routing prompt disagrees with the bridge: %s", tc.provider, tc.mode, prompt)
		}
	}
}
