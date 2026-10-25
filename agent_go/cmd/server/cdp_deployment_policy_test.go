package server

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
)

func TestDisabledDeploymentRejectsCDPRequestsAndClearsCandidatePorts(t *testing.T) {
	t.Setenv(browser.EnvAgentBrowserCDPEnabled, "false")
	port := 9222
	if err := validateRequestedCDPPorts(QueryRequest{BrowserMode: "cdp", CdpPort: &port}); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled CDP request error, got %v", err)
	}
	if ports := getCdpPorts(QueryRequest{BrowserMode: "auto", resolvedCDPPorts: []int{9222}}); len(ports) != 0 {
		t.Fatalf("disabled deployment exposed CDP ports: %v", ports)
	}

	caps := WorkflowCapabilities{BrowserMode: "auto"}
	if err := enforceDeploymentBrowserCapability(&caps); err != nil {
		t.Fatal(err)
	}
	caps.BrowserMode = "cdp"
	if err := enforceDeploymentBrowserCapability(&caps); err == nil {
		t.Fatal("explicit CDP workflow config must be rejected")
	}
}
