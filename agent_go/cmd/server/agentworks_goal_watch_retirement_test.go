package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspacereads"
)

// #2989: a cold startup spent seconds resolving nonexistent reserved Crews for
// every person and virtual Team user, each doing a server-wide ownership scan.
func TestGoalRetirementDoesNotResolveAbsentReservedCrews(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	f.api.projectActions = NewProjectActionDispatcher(f.api, f.api.agentProfiles)
	for _, user := range []string{"owner", "other", "team-fixture", "_shared"} {
		if err := os.MkdirAll(filepath.Join(f.docs, "_users", user), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, reads := workspacereads.WithRequest(t.Context())
	if err := f.api.retireAgentWorksGoalWatching(ctx); err != nil {
		t.Fatal(err)
	}
	if reads.Reads() != 0 {
		t.Fatalf("absent reserved Crews caused %d workspace reads", reads.Reads())
	}
}
