package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// PLAT-746-972: a newly created split-folder twin lost its manifests when
// WriteSync changed the settings root AFTER creation. Use real HTTP/disk storage.
func TestSplitRelayCreationRemainsAdmitted(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]},{"id":"other","username":"other","can_create":true,"products":["work"]}]}`)
	for _, split := range []bool{false, true} {
		name := "plain"
		if split {
			name = "split"
		}
		claims := &UserClaims{UserID: "owner", Username: "owner"}
		ctx := context.WithValue(t.Context(), UserContextKey, claims)
		request := crewSyncRequest{Action: "create", Mode: "relay", SplitFiles: split, PairID: "pair-" + name, SourceID: "local-" + name, Name: "Example " + name}
		created, err := f.api.crewSync(ctx, claims, "new", request)
		if err != nil {
			t.Fatal(err)
		}
		roots, err := f.api.crewSyncRoots(ctx, claims, created.ProjectID)
		if err != nil {
			t.Fatalf("new twin cannot be opened: %v", err)
		}
		for _, filename := range []string{"product.json", "workflow.json"} {
			if _, err := os.Stat(filepath.Join(roots.SettingsDir(), filename)); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.api.crewRelayAdmit(ctx, claims, crewRelayCrew{Project: created.ProjectID, Pair: request.PairID}); err != nil {
			t.Fatalf("new twin cannot connect: %v", err)
		}
		again, err := f.api.crewSync(ctx, claims, "new", request)
		if err != nil || again.ProjectID != created.ProjectID {
			t.Fatalf("retry created another twin: %+v %v", again, err)
		}
		other := &UserClaims{UserID: "other", Username: "other"}
		if err := f.api.crewRelayAdmit(context.WithValue(t.Context(), UserContextKey, other), other, crewRelayCrew{Project: created.ProjectID, Pair: request.PairID}); err == nil {
			t.Fatal("another account connected to the owner's twin")
		}
	}
}
