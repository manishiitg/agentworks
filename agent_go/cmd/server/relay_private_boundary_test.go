package server

import (
	"context"
	"strings"
	"testing"
)

// These callbacks cross the private module boundary. A trusted product still
// receives only the selected Relay's workspace namespace, never another one.
func TestRelayReleasePortsRejectOtherWorkspaceBeforeIO(t *testing.T) {
	p := relayReleasePorts("Workflow/selected")
	ctx := context.Background()
	for _, file := range []string{"Workflow/other/relay.py", "Workflow/selected/../other/relay.py", "/private/state", relayReleaseRoot("Workflow/other") + "/v1/release.json"} {
		if _, _, err := p.Read(ctx, file); err == nil || !strings.Contains(err.Error(), "outside selected Relay workspace") {
			t.Fatal("read escaped selected namespace", file, err)
		}
		if err := p.Write(ctx, file, "unexpected"); err == nil || !strings.Contains(err.Error(), "outside selected Relay workspace") {
			t.Fatal("write escaped selected namespace", file, err)
		}
	}
	if unlock := p.LockPublication("Workflow/other"); unlock != nil {
		unlock()
		t.Fatal("product changed publication lock identity")
	}
	if _, err := p.Prepare(ctx, "Workflow/other"); err == nil {
		t.Fatal("product changed draft validation identity")
	}
}
