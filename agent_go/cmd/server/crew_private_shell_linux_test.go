//go:build linux

package server

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wh "github.com/manishiitg/coding-agent-loop/workspace/handlers"
)

// PLAT-856-383: the MCP execution path must create and use a new top-level
// directory in one actual kernel-sandboxed command, without platform writes.
func TestCrewMCPNativeRootCreationKeepsPlatformStateProtected(t *testing.T) {
	if os.Getenv("AGENTWORKS_LANDLOCK_RUNNER") == "" {
		t.Skip("requires the built Linux Landlock runner")
	}
	// Hosted storage lives outside the launcher's writable private /tmp.
	// Keep this fixture there too, so a temporary-tree grant cannot mask policy errors.
	root := os.Getenv("AGENTWORKS_LINUX_TEST_ROOT")
	if root == "" || !filepath.IsAbs(root) || root == "/tmp" || strings.HasPrefix(root, "/tmp/") {
		t.Fatal("AGENTWORKS_LINUX_TEST_ROOT must name a writable directory outside /tmp")
	}
	t.Setenv("TMPDIR", root)
	f, upstream := newExternalCrewCommandFixture(t)
	t.Setenv("AUTH_SECRET", "crew-execution-test-signing-secret")
	t.Setenv("LOCAL_MODE", "false")
	t.Setenv("AGENTWORKS_BROWSER_STAGING_NAMESPACE", t.TempDir())
	t.Setenv(browserconfig.ProfileRootEnv, t.TempDir())
	socketRoot := browserconfig.ManagedSocketRoot()
	t.Cleanup(func() { _ = os.RemoveAll(socketRoot) })
	if _, _, err := crewroot.MigratePrivateState(f.docs); err != nil {
		t.Fatal(err)
	}
	roots, _, err := crewroot.Lookup(f.docs, externalCommandCrewRoot)
	if err != nil {
		t.Fatal(err)
	}
	// Both files exist: refusal must be from access policy, never a missing file.
	if err := os.MkdirAll(filepath.Join(roots.Private, "db"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roots.Private, "db", "db.sqlite"), []byte("private database"), 0600); err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(upstream)
	router := gin.New()
	router.POST("/api/execute", wh.ExecuteShellCommand)
	router.NoRoute(gin.WrapH(httputil.NewSingleHostReverseProxy(parsed)))
	workspace := httptest.NewServer(router)
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	mcp := serveExternalMCP(t, f.api, crewWriter("owner"))
	client := dialExternalMCP(t, ctx, mcp.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, client)
	command := fmt.Sprintf("mkdir folder-check && printf 'folder creation works' > folder-check/hello.txt && cat folder-check/hello.txt && if printf changed > %q; then exit 41; fi && if cat %q; then exit 42; fi && printf boundary-ok > folder-check/proof.txt", filepath.Join(roots.Private, "product.json"), filepath.Join(roots.Private, "db", "db.sqlite"))
	result := callRemoteTool(t, ctx, client, "call_tool", map[string]any{"name": "terminal", "arguments": map[string]any{"crew_id": "team", "action": "execute", "command": command}})
	requireRemoteSuccess(t, result, "terminal.execute")
	raw, err := os.ReadFile(filepath.Join(roots.Files, "folder-check", "hello.txt"))
	if err != nil || string(raw) != "folder creation works" {
		t.Fatalf("new root file: %q %v; MCP=%+v", raw, err, result)
	}
	raw, err = os.ReadFile(filepath.Join(roots.Private, "product.json"))
	if err != nil || strings.Contains(string(raw), "changed") {
		t.Fatal("platform record changed", err)
	}
	proof, err := os.ReadFile(filepath.Join(roots.Files, "folder-check", "proof.txt"))
	if err != nil || string(proof) != "boundary-ok" {
		t.Fatalf("MCP command did not finish all allowed/refused checks: %s %v", marshalContent(t, result), err)
	}
	if _, err := os.Stat(filepath.Join(roots.Files, ".sandbox-cache")); !os.IsNotExist(err) {
		t.Fatalf("tool home recreated in working folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(roots.Private, ".sandbox-cache", "npm")); err != nil {
		t.Fatalf("private tool cache missing: %v", err)
	}
	t.Logf("MCP result: %s", marshalContent(t, result))
}
