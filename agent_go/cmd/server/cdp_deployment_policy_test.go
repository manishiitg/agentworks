package server

import (
	"strings"
	"testing"
)

func TestDisabledDeploymentRejectsCDPRequestsAndClearsCandidatePorts(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	port := 9222
	if err := validateRequestedCDPPorts(QueryRequest{BrowserMode: "cdp", CdpPort: &port}); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled CDP request error, got %v", err)
	}
	if ports := getCdpPorts(QueryRequest{BrowserMode: "headless", resolvedCDPPorts: []int{9222}}); len(ports) != 0 {
		t.Fatalf("disabled deployment exposed CDP ports: %v", ports)
	}

	caps := WorkflowCapabilities{BrowserMode: "headless"}
	if err := enforceDeploymentBrowserCapability(&caps); err != nil {
		t.Fatal(err)
	}
	caps.BrowserMode = "cdp"
	if err := enforceDeploymentBrowserCapability(&caps); err == nil {
		t.Fatal("explicit CDP workflow config must be rejected")
	}
}
