package server

import (
	"testing"
	"time"
)

// PLAT-856-575: project the exact admitted attachment, never the AI program's
// own version or a row's claims. Unknown/dev builds must not demand an update.
func TestAgentHelperVersionUsesAttachmentAndServedBuild(t *testing.T) {
	servedCLIBuild.Lock()
	beforeBuild, beforeAt := servedCLIBuild.build, servedCLIBuild.at
	servedCLIBuild.build, servedCLIBuild.at = "current-build", time.Now()
	servedCLIBuild.Unlock()
	t.Cleanup(func() {
		servedCLIBuild.Lock()
		servedCLIBuild.build, servedCLIBuild.at = beforeBuild, beforeAt
		servedCLIBuild.Unlock()
	})
	for _, tc := range []struct {
		build  string
		needed bool
	}{{"", false}, {"dev", false}, {"current-build", false}, {"old-build", true}, {"bad\nvalue", false}} {
		row := map[string]any{}
		a := namedMessageAgent{ID: "version-agent", Attachment: &namedAgentAttachment{ClientBuild: tc.build}}
		addAgentPresence(row, a)
		if tc.build == "" || tc.build == "bad\nvalue" {
			if row["client_build"] != nil || row["update_needed"] != nil {
				t.Fatalf("unknown build projected: %#v", row)
			}
			continue
		}
		if row["client_build"] != tc.build || row["update_needed"] != tc.needed {
			t.Fatalf("%q: %#v", tc.build, row)
		}
	}
	row := map[string]any{}
	projectAgentHelper(row, namedMessageAgent{ID: "hosted"})
	if len(row) != 0 {
		t.Fatalf("hosted agent given a fake CLI: %#v", row)
	}
}
