package server

import (
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
)

const defaultCDPPort = 9222

func configuredCDPPorts(primary *int, additional []int) []int {
	return getCdpPorts(QueryRequest{
		BrowserMode:      "cdp",
		CdpPort:          primary,
		resolvedCDPPorts: additional,
	})
}

func configuredCDPPortsForMode(mode string, primary *int, additional []int) []int {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "auto", "cdp":
		return configuredCDPPorts(primary, additional)
	default:
		return nil
	}
}

// hostDownloadsBrowserMode resolves the mode used by the shell's built-in
// host Downloads exception. QueryRequest keeps "auto" as user intent, while
// the filesystem helper intentionally accepts only the effective "cdp" mode.
// Resolve that boundary once so normal and restored coding-agent sessions use
// the same read-only grant.
func hostDownloadsBrowserMode(req QueryRequest) string {
	if !browser.CDPEnabled() {
		return "headless"
	}
	mode := getBrowserMode(req)
	if mode == "auto" && len(configuredCDPPortsForMode(mode, req.CdpPort, req.resolvedCDPPorts)) > 0 {
		return "cdp"
	}
	return mode
}
