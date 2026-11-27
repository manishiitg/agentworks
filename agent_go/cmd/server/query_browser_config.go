package server

import (
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	browserinstructions "github.com/manishiitg/coding-agent-loop/agent_go/pkg/instructions"
)

const maxCDPPortsPerRun = 4

// getCdpPorts returns the ordered, unique CDP ports explicitly authorized for
// this run. Multiple ports are reserved for separate Chrome profiles/login
// identities inside one workflow; ordinary concurrent workflows share one.
func getCdpPorts(req QueryRequest) []int {
	if !browser.CDPEnabled() {
		return nil
	}
	mode := strings.ToLower(strings.TrimSpace(req.BrowserMode))
	if mode == "none" || mode == "extension" || mode == "headless" {
		return nil
	}
	candidates := make([]int, 0, 1+len(req.resolvedCDPPorts))
	if req.CdpPort != nil {
		candidates = append(candidates, *req.CdpPort)
	}
	candidates = append(candidates, req.resolvedCDPPorts...)
	seen := make(map[int]bool, len(candidates))
	ports := make([]int, 0, len(candidates))
	for _, port := range candidates {
		if port < 1 || port > 65535 || seen[port] {
			continue
		}
		seen[port] = true
		ports = append(ports, port)
		if len(ports) == maxCDPPortsPerRun {
			break
		}
	}
	if len(ports) == 0 && strings.EqualFold(strings.TrimSpace(req.BrowserMode), "cdp") {
		return []int{defaultCDPPort}
	}
	return ports
}

func validateRequestedCDPPorts(req QueryRequest) error {
	if !browser.CDPEnabled() {
		if strings.EqualFold(strings.TrimSpace(req.BrowserMode), "cdp") || req.CdpPort != nil || len(req.resolvedCDPPorts) > 0 {
			return fmt.Errorf("CDP is disabled for this server deployment; choose the extension or in-app browser without CDP ports")
		}
		return nil
	}
	candidates := append([]int{}, req.resolvedCDPPorts...)
	if req.CdpPort != nil {
		candidates = append([]int{*req.CdpPort}, candidates...)
	}
	seen := make(map[int]bool, len(candidates))
	for _, port := range candidates {
		if port < 1 || port > 65535 {
			return fmt.Errorf("CDP port %d must be between 1 and 65535", port)
		}
		seen[port] = true
	}
	if len(seen) > maxCDPPortsPerRun {
		return fmt.Errorf("a run may authorize at most %d CDP ports", maxCDPPortsPerRun)
	}
	return nil
}

// getCdpPort returns the primary CDP port from a QueryRequest, or 0 if unset.
func getCdpPort(req QueryRequest) int {
	if ports := getCdpPorts(req); len(ports) > 0 {
		return ports[0]
	}
	return 0
}

// getBrowserMode resolves optional browser intent using installation policy.
func getBrowserMode(req QueryRequest) string {
	mode := strings.ToLower(strings.TrimSpace(req.BrowserMode))
	if mode != "" {
		if browser.ValidateMode(mode) == nil {
			return mode
		}
		return "none"
	}
	if req.EnableBrowserAccess != nil && !*req.EnableBrowserAccess {
		return "none"
	}
	return browser.DefaultMode()
}

// buildChatBrowserConfig resolves the browser configuration from a QueryRequest
// into the standardized BrowserConfig used by BuildBrowserInstructions.
func buildChatBrowserConfig(req QueryRequest) browserinstructions.BrowserConfig {
	mode := getBrowserMode(req)
	if mode == "auto" && !browser.CDPEnabled() {
		// CDP can never resolve on this deployment (getCdpPorts already
		// returns none, and validateRequestedCDPPorts already rejects an
		// explicit cdp request) -- so "auto" mode's whole point, deciding
		// between CDP and headless, has only one possible answer here.
		// Skipping straight to headless drops the "call agent_browser status
		// first, follow effective_mode" preamble and the phantom port-9222
		// candidate that GetAutoBrowserInstructions/BuildBrowserRuntimeInstructions
		// would otherwise always add, on every single turn, for a check that
		// can never succeed.
		mode = "headless"
	}
	ports := getCdpPorts(req)
	if mode == "auto" {
		ports = configuredCDPPortsForMode(mode, req.CdpPort, req.resolvedCDPPorts)
	}
	primary := 0
	if len(ports) > 0 {
		primary = ports[0]
	}
	cfg := browserinstructions.BrowserConfig{
		CdpPort:  primary,
		CdpPorts: ports,
		Mode:     mode,
	}
	hasBrowserAccess := req.EnableBrowserAccess != nil && *req.EnableBrowserAccess
	if hasBrowserAccess || mode == "auto" || mode == "headless" || mode == "cdp" {
		cfg.HasAgentBrowser = true
	}
	return cfg
}

func cdpPromptEndpoints(ports []int, primary int) (string, string) {
	ordered := append([]int{}, ports...)
	if primary > 0 {
		ordered = append([]int{primary}, ordered...)
	}
	endpoints := browser.ConfiguredCDPEndpoints(ordered)
	if len(endpoints) == 0 {
		endpoints = []string{browser.ConfiguredCDPEndpoint(defaultCDPPort)}
	}
	guidance := "This run authorizes CDP endpoint `" + endpoints[0] + "`."
	if len(endpoints) > 1 {
		guidance = "This run explicitly authorizes independent Chrome profiles at `" + strings.Join(endpoints, "`, `") + "`. Choose the endpoint matching the intended login/account on every call; keep separate labeled tabs per profile. Multiple ports are for specialized multi-login testing, not ordinary workflow concurrency."
	}
	return endpoints[0], guidance
}
