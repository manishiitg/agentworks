package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// PLAT-746-972: a newly created split-folder twin lost its manifests when
// WriteSync changed the settings root AFTER creation. Use real HTTP/disk storage.
func TestSplitRelayCreationRemainsAdmitted(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	// PLAT-856-572: sync admission used to discover projects and expand every
	// runtime action on each relay round. Count real workspace GETs, not mocks.
	previousClient := workspaceHTTPClient
	client := *previousClient
	transport := previousClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	var readsMu sync.Mutex
	reads := map[string]int{}
	client.Transport = inspectionRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/documents") {
			readsMu.Lock()
			reads[r.URL.Path]++
			readsMu.Unlock()
		}
		return transport.RoundTrip(r)
	})
	workspaceHTTPClient = &client
	t.Cleanup(func() { workspaceHTTPClient = previousClient })
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
		readsMu.Lock()
		reads = map[string]int{}
		readsMu.Unlock()
		roots, err := f.api.crewSyncRoots(ctx, claims, created.ProjectID)
		if err != nil {
			t.Fatalf("new twin cannot be opened: %v", err)
		}
		readsMu.Lock()
		gotReads := reads
		reads = map[string]int{}
		readsMu.Unlock()
		metadataReads := 0
		for route, count := range gotReads {
			if strings.HasSuffix(route, "/product.json") {
				metadataReads += count
				continue
			}
			if strings.HasSuffix(route, "/user-product-access.json") && count <= 1 {
				continue
			}
			t.Fatalf("sync resolved more than roots and fresh access: %s count=%d", route, count)
		}
		if metadataReads != 1 {
			t.Fatalf("identity reads=%d, want one: %v", metadataReads, gotReads)
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
		// A read failure for the correct owner is NOT an account change and
		// must never trigger the Mac's create-another-copy repair loop.
		if err := os.Remove(filepath.Join(roots.SettingsDir(), "product.json")); err != nil {
			t.Fatal(err)
		}
		if err := f.api.crewRelayAdmit(ctx, claims, crewRelayCrew{Project: created.ProjectID, Pair: request.PairID}); err == nil || strings.Contains(err.Error(), "not the owner") {
			t.Fatalf("unavailable settings misreported as account switch: %v", err)
		}

		// #2796: a temporary settings failure must not mint a second project
		// and Team/home for the same owner's original Mac Crew.
		if duplicate, err := f.api.crewSync(ctx, claims, "new", request); err == nil || duplicate.ProjectID != "" {
			t.Fatal("unreadable existing twin was replaced by a duplicate")
		}

	}
}
