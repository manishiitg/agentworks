package server

import (
	"reflect"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

func TestConfiguredCDPPortsForAutoRemainCandidates(t *testing.T) {
	ports := configuredCDPPortsForMode("auto", nil, nil)
	if !reflect.DeepEqual(ports, []int{9222}) {
		t.Fatalf("default auto candidates = %v, want [9222]", ports)
	}
	ports = configuredCDPPortsForMode("auto", nil, []int{9222, 9333})
	if !reflect.DeepEqual(ports, []int{9222, 9333}) {
		t.Fatalf("configured auto candidates = %v", ports)
	}
	if ports := configuredCDPPortsForMode("headless", nil, []int{9222}); len(ports) != 0 {
		t.Fatalf("headless must not retain CDP candidates: %v", ports)
	}
}

func TestHostDownloadsBrowserModeResolvesRestoredAutoSessionToCDP(t *testing.T) {
	req := QueryRequest{BrowserMode: "auto", CdpPorts: []int{9222}}
	if got := hostDownloadsBrowserMode(req); got != "cdp" {
		t.Fatalf("host Downloads mode = %q, want cdp", got)
	}
	if got := hostDownloadsBrowserMode(QueryRequest{BrowserMode: "headless"}); got != "headless" {
		t.Fatalf("headless host Downloads mode = %q, want headless", got)
	}
}

func TestGetCdpPortsPreservesPrimaryAndDeduplicates(t *testing.T) {
	primary := 9333
	ports := getCdpPorts(QueryRequest{BrowserMode: "cdp", CdpPort: &primary, CdpPorts: []int{9222, 9333, -1, 9444}})
	want := []int{9333, 9222, 9444}
	if !reflect.DeepEqual(ports, want) {
		t.Fatalf("getCdpPorts() = %v, want %v", ports, want)
	}
}

func TestGetCdpPortsIgnoresStalePortsOutsideCDPModes(t *testing.T) {
	for _, mode := range []string{"none", "headless"} {
		t.Run(mode, func(t *testing.T) {
			primary := 9222
			ports := getCdpPorts(QueryRequest{BrowserMode: mode, CdpPort: &primary, CdpPorts: []int{9333}})
			if len(ports) != 0 {
				t.Fatalf("getCdpPorts() = %v for mode %q, want no CDP authorization", ports, mode)
			}
		})
	}
}

func TestBuildChatBrowserConfigUsesBrowserModeCDPWithoutEnableBrowserAccess(t *testing.T) {
	req := QueryRequest{BrowserMode: "cdp"}

	cfg := buildChatBrowserConfig(req)

	if !cfg.HasAgentBrowser {
		t.Fatalf("expected CDP browser_mode to enable agent_browser")
	}
	if cfg.Mode != "cdp" {
		t.Fatalf("mode = %q, want cdp", cfg.Mode)
	}
	if cfg.CdpPort != 9222 {
		t.Fatalf("cdp port = %d, want default 9222", cfg.CdpPort)
	}
}

// When this deployment's CDP is disabled, "auto" mode's only possible answer
// is headless -- getCdpPorts already returns none and
// validateRequestedCDPPorts already rejects an explicit cdp request. Resolving
// straight to headless here drops the "call agent_browser status first" auto
// preamble and the phantom port-9222 candidate that would otherwise be added
// on every single turn, for a check that can never succeed. Confirmed live on
// SparkQuill: every turn's prompt claimed an "authorized CDP endpoint" at
// :9222 that was never reachable on this headless-only server.
func TestBuildChatBrowserConfigResolvesAutoToHeadlessWhenCDPDisabled(t *testing.T) {
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	req := QueryRequest{BrowserMode: "auto"}

	cfg := buildChatBrowserConfig(req)

	if cfg.Mode != "headless" {
		t.Fatalf("mode = %q, want headless", cfg.Mode)
	}
	if cfg.CdpPort != 0 || len(cfg.CdpPorts) != 0 {
		t.Fatalf("expected no CDP candidates when CDP is disabled, got port=%d ports=%v", cfg.CdpPort, cfg.CdpPorts)
	}
	if !cfg.HasAgentBrowser {
		t.Fatalf("expected agent_browser to remain enabled in headless mode")
	}
}

func TestApplyMultiAgentCapabilitiesToRequestOverridesRequestCapabilities(t *testing.T) {
	globalSecrets := []string{"GLOBAL_TOKEN"}
	req := QueryRequest{
		Servers:               []string{"old-server"},
		EnabledServers:        []string{"old-enabled"},
		SelectedTools:         []string{"old:tool"},
		SelectedSkills:        []string{"old-skill"},
		BrowserMode:           "none",
		SelectedGlobalSecrets: &[]string{"OLD_GLOBAL"},
	}

	applyMultiAgentCapabilitiesToRequest(&req, WorkflowCapabilities{
		SelectedServers:           []string{"filesystem", "agent-browser"},
		SelectedTools:             []string{"filesystem:read_file"},
		SelectedSkills:            []string{"agent-browser"},
		SelectedGlobalSecretNames: &globalSecrets,
		BrowserMode:               "CDP",
		UseCodeExecutionMode:      true,
		LLMConfig: &workflowtypes.PresetLLMConfig{
			SchemaVersion: workflowtypes.LLMConfigSchemaVersion,
			Mode:          workflowtypes.LLMConfigModeExplicit,
			BuilderLLM: &workflowtypes.AgentLLMConfig{
				Provider: "openai",
				ModelID:  "gpt-test",
			},
		},
	})

	if len(req.Servers) != 0 {
		t.Fatalf("legacy servers = %v, want cleared", req.Servers)
	}
	if got := req.EnabledServers; len(got) != 2 || got[0] != "filesystem" || got[1] != "agent-browser" {
		t.Fatalf("enabled servers = %v, want saved selection", got)
	}
	if got := req.SelectedTools; len(got) != 1 || got[0] != "filesystem:read_file" {
		t.Fatalf("selected tools = %v, want saved selection", got)
	}
	if got := req.SelectedSkills; len(got) != 1 || got[0] != "agent-browser" {
		t.Fatalf("selected skills = %v, want saved selection", got)
	}
	if req.BrowserMode != "cdp" {
		t.Fatalf("browser mode = %q, want cdp", req.BrowserMode)
	}
	if req.EnableBrowserAccess == nil || !*req.EnableBrowserAccess {
		t.Fatalf("EnableBrowserAccess = %v, want true for cdp", req.EnableBrowserAccess)
	}
	if !req.UseCodeExecutionMode {
		t.Fatalf("UseCodeExecutionMode = false, want true")
	}
	if req.SelectedGlobalSecrets == nil || len(*req.SelectedGlobalSecrets) != 1 || (*req.SelectedGlobalSecrets)[0] != "GLOBAL_TOKEN" {
		t.Fatalf("selected global secrets = %v, want saved selection", req.SelectedGlobalSecrets)
	}
	if req.LLMConfig == nil || req.LLMConfig.Primary.Provider != "openai" || req.LLMConfig.Primary.ModelID != "gpt-test" {
		t.Fatalf("llm config = %+v, want saved phase llm", req.LLMConfig)
	}

}

func TestApplyMultiAgentCapabilitiesMigratesLegacyNoneToAuto(t *testing.T) {
	enabled := true
	req := QueryRequest{
		BrowserMode:          "cdp",
		EnableBrowserAccess:  &enabled,
		UseCodeExecutionMode: true,
	}

	applyMultiAgentCapabilitiesToRequest(&req, WorkflowCapabilities{BrowserMode: "none"})

	if req.BrowserMode != "auto" {
		t.Fatalf("browser mode = %q, want auto", req.BrowserMode)
	}
	if req.EnableBrowserAccess == nil || !*req.EnableBrowserAccess {
		t.Fatalf("EnableBrowserAccess = %v, want true for legacy none", req.EnableBrowserAccess)
	}
	if req.UseCodeExecutionMode {
		t.Fatalf("UseCodeExecutionMode = true, want saved false")
	}
}

func TestApplyMultiAgentCapabilitiesToRequestEnablesBrowserForAuto(t *testing.T) {
	req := QueryRequest{}
	applyMultiAgentCapabilitiesToRequest(&req, WorkflowCapabilities{BrowserMode: "auto", CDPPorts: []int{9222, 9333}})

	if req.BrowserMode != "auto" {
		t.Fatalf("browser mode = %q, want auto", req.BrowserMode)
	}
	if req.EnableBrowserAccess == nil || !*req.EnableBrowserAccess {
		t.Fatalf("EnableBrowserAccess = %v, want true for auto", req.EnableBrowserAccess)
	}
	if !reflect.DeepEqual(req.CdpPorts, []int{9222, 9333}) {
		t.Fatalf("CdpPorts = %v, want saved multi-profile ports", req.CdpPorts)
	}
}
