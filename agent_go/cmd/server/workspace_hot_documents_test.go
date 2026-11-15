package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// PLAT-856-572: reuse unchanged bytes, never a stale access decision. Exercise
// the actual workspace handlers and store writes, including preserved-mtime
// external edits, deletion/creation and a write racing a cache fill.
func TestHotWorkspaceBytesReuseWithoutStalePermissions(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	const file = "config/user-product-access.json"
	const alpha = `{"owner":{"workflow_ids":["alpha"]}}`
	const bravo = `{"owner":{"workflow_ids":["bravo"]}}`
	f.write(t, file, alpha)
	invalidateHotDocuments(file, false)
	original := workspaceHTTPClient
	client := *original
	base := original.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	var gets atomic.Int32
	client.Transport = inspectionRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/documents/"+file {
			gets.Add(1)
		}
		return base.RoundTrip(r)
	})
	workspaceHTTPClient = &client
	t.Cleanup(func() { workspaceHTTPClient = original; invalidateHotDocuments(file, false) })
	claims := &UserClaims{UserID: "owner", Username: "owner"}
	for range 100 {
		if !userAllowedWorkflowID(claims, "alpha") {
			t.Fatal("allowed case refused")
		}
	}
	if gets.Load() != 1 {
		t.Fatalf("100 access decisions caused %d document GETs, want one", gets.Load())
	}
	ctx := context.WithValue(t.Context(), UserContextKey, claims)
	if err := writeFileToWorkspace(ctx, file, bravo); err != nil {
		t.Fatal(err)
	}
	if userAllowedWorkflowID(claims, "alpha") || !userAllowedWorkflowID(claims, "bravo") {
		t.Fatal("store write did not immediately revoke old access")
	}
	if gets.Load() != 2 {
		t.Fatalf("post-write GETs=%d, want two total", gets.Load())
	}
	physical := filepath.Join(f.docs, filepath.FromSlash(file))
	before, err := os.Stat(physical)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err = os.WriteFile(physical, []byte(alpha), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(physical, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	if !userAllowedWorkflowID(claims, "alpha") || userAllowedWorkflowID(claims, "bravo") {
		t.Fatal("same-size, restored-mtime external edit reused old permissions")
	}
	if gets.Load() != 3 {
		t.Fatalf("external edit GETs=%d", gets.Load())
	}
	if err = deleteWorkspaceFile(ctx, file); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		_, exists, e := readFileFromWorkspace(ctx, file)
		if e != nil || exists {
			t.Fatalf("deleted file: exists=%t err=%v", exists, e)
		}
	}
	if gets.Load() != 4 {
		t.Fatalf("negative read was not reusable: GETs=%d", gets.Load())
	}
	f.write(t, file, bravo)
	if userAllowedWorkflowID(claims, "alpha") {
		t.Fatal("creation beneath a cached missing parent did not revoke access")
	}
	invalidateHotDocuments(file, false)
	var loads int
	load := func() (string, bool, error) { loads++; invalidateHotDocuments(file, false); return bravo, true, nil }
	for range 2 {
		if _, _, err = readHotDocument(ctx, file, load); err != nil {
			t.Fatal(err)
		}
	}
	if loads != 2 {
		t.Fatal("a racing write's invalidation was lost to a cache fill")
	}
	t.Log("100 access decisions => 1 GET; store writes/external preserved-mtime edits/deletion/creation observed immediately")
}
