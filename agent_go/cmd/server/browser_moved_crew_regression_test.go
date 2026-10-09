package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
)

// Uses the real Start handler, workspace shell handler and Chrome. The old
// Crew directory deliberately does not exist, reproducing server B.
func TestMovedCrewBrowserStartsRealChrome(t *testing.T) {
	if os.Getenv("RUN_BROWSER_TEACH_E2E") != "1" {
		t.Skip("set RUN_BROWSER_TEACH_E2E=1 for owned Chrome")
	}
	f := newMultiUserFixture(t, sharedIdentityLayout())
	root := "Crew/" + fixtureCrewFolder
	old := workspaceref.PhysicalPath(fixtureUserA, workspaceref.CrewProjectsRoot, fixtureCrewFolder)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: fixtureCrewFolder, OwnerID: fixtureUserA, Shared: true, Aliases: []string{old}}); err != nil {
		t.Fatal(err)
	}
	resetCrewLocationCaches()
	t.Setenv("AGENTWORKS_SLOTS", "off")
	t.Setenv("NATIVE_WORKSPACE", "true")
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	t.Setenv("WORKSPACE_API_TOKEN", "moved-crew-test")
	t.Setenv("AGENT_BROWSER_PROFILE_ROOT", filepath.Join(t.TempDir(), "profiles"))
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", "")
	if chrome := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"; func() bool { _, err := os.Stat(chrome); return err == nil }() {
		t.Setenv("AGENT_BROWSER_EXECUTABLE_PATH", chrome)
	}
	viper.Set("docs-dir", f.Docs)
	defer viper.Set("docs-dir", "")
	if _, err := os.Stat(filepath.Join(f.Docs, old)); !os.IsNotExist(err) {
		t.Fatal("legacy directory exists; fixture does not reproduce the bug")
	}
	session := browserSessionForWorkspace(fixtureUserA, old)
	router := gin.New()
	router.POST("/api/execute", workspacehandlers.ExecuteShellCommand)
	workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/documents/") {
			f.Mock.ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/restore-tabs") {
			json.NewEncoder(w).Encode(map[string]bool{"success": true})
			return
		}
		router.ServeHTTP(w, r)
	}))
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	client := browser.NewClient(workspace.URL)
	defer func() {
		client.ExecuteCommand(context.Background(), append(browser.HeadlessLaunchArgsForSession(session), "--session", session, "close", "--json"), workspaceBrowserExecuteOptions(fixtureUserA, root, session, 10*time.Second))
		browser.GetSessionTracker().Remove(session)
	}()
	for _, spelling := range []string{root, old} {
		req := httptest.NewRequest(http.MethodPost, "/?workspace_path="+spelling+"&profile_id=work", strings.NewReader(`{"action":"start"}`)).WithContext(f.Ctx(fixtureUserA))
		physical, err := f.API.browserWorkspaceAccess(req, spelling, "work", true)
		if err != nil || physical != root || browserSessionForWorkspace(fixtureUserA, spelling) != session {
			t.Fatalf("browser scope %s: directory=%s err=%v", spelling, physical, err)
		}
		response := httptest.NewRecorder()
		f.API.handleWorkspaceBrowser(response, req)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), session) {
			t.Fatalf("Start %s: %d %s", spelling, response.Code, response.Body.String())
		}
	}
}
