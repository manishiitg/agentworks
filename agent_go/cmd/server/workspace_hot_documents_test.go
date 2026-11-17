package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
)

// PLAT-856-572: reuse unchanged bytes, never a stale access decision. Exercise
// the actual workspace handlers and store writes, including preserved-mtime
// external edits, deletion/creation and a write racing a cache fill.
func TestHotWorkspaceBytesReuseWithoutStalePermissions(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t, func(router *gin.Engine) {
		// The shared command fixture has GET/PUT/POST but not DELETE. Exercise the
		// real deletion route; a router-level 404 is not proof the file was deleted.
		router.DELETE("/api/documents/*filepath", workspacehandlers.DeleteDocument)
	})
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
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
	// #2989: server-side project scans exceed 64 metadata files. Another
	// project's mutation must not flush these unchanged byte snapshots.
	for i := range 100 {
		other := fmt.Sprintf("Crew/project-%d/product.json", i)
		f.write(t, other, `{}`)
		if _, _, err := readFileFromWorkspace(t.Context(), other); err != nil {
			t.Fatal(err)
		}
	}
	other := "Crew/project-0/product.json"
	if err := writeFileToWorkspace(t.Context(), other, `{"title":"changed"}`); err != nil {
		t.Fatal(err)
	}
	if !userAllowedWorkflowID(claims, "alpha") || gets.Load() != 1 {
		t.Fatal("project scan or unrelated mutation evicted unchanged access bytes")
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
	if gets.Load() != 5 {
		t.Fatalf("missing files must bypass the byte cache: GETs=%d", gets.Load())
	}
	f.write(t, file, bravo)
	if userAllowedWorkflowID(claims, "alpha") {
		t.Fatal("creation did not revoke access")
	}
	invalidateHotDocuments(file, false)
	var loads int
	load := func() (string, bool, error) { loads++; invalidateHotDocuments(file, false); return bravo, true, nil }
	for range 2 {
		if _, _, err = readFileFromWorkspaceHotDocument(ctx, file, load); err != nil {
			t.Fatal(err)
		}
	}
	if loads != 2 {
		t.Fatal("a racing write's invalidation was lost to a cache fill")
	}
	t.Log("100 access decisions => 1 GET; store writes/external preserved-mtime edits/deletion/creation observed immediately")
}
