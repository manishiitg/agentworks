package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
)

// This runs the complete tool → real guarded workspace shell → agent-browser
// → relay → actual unpacked Chrome extension → existing signed-in tab path.
func TestChromeExtensionToolRealE2E(t *testing.T) {
	if os.Getenv("RUN_CHROME_EXTENSION_E2E") != "1" {
		t.Skip("set RUN_CHROME_EXTENSION_E2E=1 for real Chrome extension qualification")
	}
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("NATIVE_WORKSPACE", "true")
	t.Setenv("AGENTWORKS_BROWSER_STAGING_NAMESPACE", "chrome-extension-e2e")
	m := browserrelay.New()
	previous := browserrelay.Default
	browserrelay.Default = m
	defer func() { m.Close(); browserrelay.Default = previous }()
	root := t.TempDir()
	workspace := "Workflow/extension-e2e"
	evidence := workspace + "/evidence"
	os.MkdirAll(filepath.Join(root, evidence), 0755)
	oldDocs := viper.GetString("docs-dir")
	viper.Set("docs-dir", root)
	defer viper.Set("docs-dir", oldDocs)
	session := "chrome-extension-e2e"
	common.BindSessionBrowserIsolationForWorkflow(session, workspace)
	common.SetSessionWorkingDir(session, workspace)
	common.SetSessionFolderGuard(session, []string{workspace}, []string{workspace})
	defer common.ClearSessionShellConfig(session)
	token, err := m.Pair("alice", common.SandboxBrowserSession(session), "Fixture workspace")
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.POST("/api/execute", workspacehandlers.ExecuteShellCommand)
	shell := httptest.NewServer(router)
	defer shell.Close()
	executor := NewExecutor(NewClient(shell.URL), WithBrowserRuntimeConfig(NewBrowserRuntimeConfig("auto", nil)))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/browser/extension/connect", m.ServeExtension)
	mux.HandleFunc("/fixture/cdp", func(w http.ResponseWriter, r *http.Request) {
		b := m.Lookup("alice", common.SandboxBrowserSession(session))
		if b == nil {
			http.Error(w, "not paired", 409)
			return
		}
		endpoint, release, err := b.Acquire(r.Context())
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		defer release()
		json.NewEncoder(w).Encode(map[string]string{"url": endpoint, "session": b.Session()})
	})
	mux.HandleFunc("/fixture/tool", func(w http.ResponseWriter, r *http.Request) {
		var args map[string]interface{}
		json.NewDecoder(r.Body).Decode(&args)
		args["session"] = "main"
		ctx := context.WithValue(r.Context(), common.ChatSessionIDKey, session)
		ctx = context.WithValue(ctx, common.UserIDKey, "alice")
		output, err := executor.HandleAgentBrowser(ctx, args)
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.Write([]byte(output))
	})
	mux.HandleFunc("/fixture/image", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(root, evidence, "screenshot.png"))
	})
	mux.HandleFunc("/fixture", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "fixture_login", Value: "retained", Path: "/"})
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><title>Shared fixture</title><h1>Chrome bridge fixture</h1><label>Name <input id="name"></label><button id="save" onclick="document.querySelector('#result').textContent='Saved '+document.querySelector('#name').value">Save customer</button><p id="result">Ready</p>`))
	})
	mux.HandleFunc("/private", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<title>Unshared private tab</title><h1>Do not expose this tab</h1>`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	project, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", filepath.Join(project, "scripts/test-chrome-extension-e2e.mjs"))
	cmd.Env = append(os.Environ(), "CHROME_EXTENSION_E2E_URL="+server.URL, "CHROME_EXTENSION_E2E_TOKEN="+token)
	output, err := cmd.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
}
