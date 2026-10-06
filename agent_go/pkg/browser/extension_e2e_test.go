package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
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
	stateRoot := t.TempDir()
	m, err := browserrelay.NewPersistent(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	var live atomic.Pointer[browserrelay.Manager]
	live.Store(m)
	previous := browserrelay.Default
	browserrelay.Default = m
	defer func() { live.Load().Close(); browserrelay.Default = previous }()
	root := t.TempDir()
	workspace := "Chats/Code/projects/extension-e2e"
	evidence := workspace + "/evidence"
	os.MkdirAll(filepath.Join(root, evidence), 0755)
	oldDocs := viper.GetString("docs-dir")
	viper.Set("docs-dir", root)
	defer viper.Set("docs-dir", oldDocs)
	session := "chrome-extension-e2e"
	common.BindSessionBrowserIsolationForProject(session, "_users/alice/"+workspace)
	common.SetSessionWorkingDir(session, workspace)
	common.SetSessionFolderGuard(session, []string{workspace}, []string{workspace})
	defer common.ClearSessionShellConfig(session)
	token, err := m.PairForProfile("alice", common.SandboxBrowserSession(session), workspace, "code")
	if err != nil {
		t.Fatal(err)
	}
	crewWorkspace := "Chats/Work/projects/crew-extension-e2e"
	if err := os.MkdirAll(filepath.Join(root, crewWorkspace), 0755); err != nil {
		t.Fatal(err)
	}
	crewSession := "crew-extension-e2e"
	common.BindSessionBrowserIsolationForProject(crewSession, "_users/alice/"+crewWorkspace)
	common.SetSessionWorkingDir(crewSession, crewWorkspace)
	common.SetSessionFolderGuard(crewSession, []string{crewWorkspace}, []string{crewWorkspace})
	defer common.ClearSessionShellConfig(crewSession)
	crewToken, err := m.PairForProfile("alice", common.SandboxBrowserSession(crewSession), crewWorkspace, "work")
	if err != nil || crewToken != token {
		t.Fatal("account token differs in Crew", err)
	}
	workflowWorkspace := "Workflow/extension-workflow"
	if err := os.MkdirAll(filepath.Join(root, workflowWorkspace), 0755); err != nil {
		t.Fatal(err)
	}
	workflowSession := "workflow-extension-e2e"
	common.BindSessionBrowserIsolationForWorkflow(workflowSession, workflowWorkspace)
	common.SetSessionWorkingDir(workflowSession, workflowWorkspace)
	common.SetSessionFolderGuard(workflowSession, []string{workflowWorkspace}, []string{workflowWorkspace})
	defer common.ClearSessionShellConfig(workflowSession)
	workflowToken, err := m.PairForProfile("alice", common.SandboxBrowserSession(workflowSession), workflowWorkspace, "workflow")
	if err != nil || workflowToken != token {
		t.Fatal("workflow account token", err)
	}
	for _, step := range []string{"workflow-step-one", "workflow-step-two"} {
		common.CopySessionFolderGuard(workflowSession, step)
		common.BindSessionBrowserIsolationForWorkflow(step, workflowWorkspace)
		defer common.ClearSessionShellConfig(step)
	}
	// Saturate headless capacity before running the actual extension path. Relay
	// commands must neither be rejected nor evict this occupied native slot.
	oldPerChat, oldGlobal := workspacehandlers.MaxBrowserSessionsPerChat, workspacehandlers.MaxBrowserSessionsGlobal
	workspacehandlers.MaxBrowserSessionsPerChat, workspacehandlers.MaxBrowserSessionsGlobal = 1, 1
	defer func() {
		workspacehandlers.MaxBrowserSessionsPerChat, workspacehandlers.MaxBrowserSessionsGlobal = oldPerChat, oldGlobal
	}()
	nativeEnv := map[string]string{"MCP_SESSION_ID": session}
	workspacehandlers.CheckBrowserSessionLimit("agent-browser --session extension-e2e-native open about:blank", nativeEnv)
	defer workspacehandlers.CheckBrowserSessionLimit("agent-browser --session extension-e2e-native close", nativeEnv)
	router := gin.New()
	router.POST("/api/execute", workspacehandlers.ExecuteShellCommand)
	shell := httptest.NewServer(router)
	defer shell.Close()
	executor := NewExecutor(NewClient(shell.URL), WithBrowserRuntimeConfig(NewBrowserRuntimeConfig("auto", nil)))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/browser/extension/connect", func(w http.ResponseWriter, r *http.Request) {
		live.Load().ServeExtensionAuthorizedWithNames(w, r, nil, func(_, workspace, profile string) string {
			if profile == "workflow" {
				return "Customer Onboarding"
			}
			if profile == "work" {
				return "Support Crew"
			}
			return "Code Browser Test"
		})
	})
	mux.HandleFunc("/fixture/code-status", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(live.Load().Status("alice", common.SandboxBrowserSession(session)))
	})
	mux.HandleFunc("/fixture/restart-relay", func(w http.ResponseWriter, r *http.Request) {
		next, err := browserrelay.NewPersistent(stateRoot)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		old := live.Swap(next)
		browserrelay.Default = next
		old.Close()
	})
	mux.HandleFunc("/fixture/disconnect-code", func(w http.ResponseWriter, r *http.Request) {
		live.Load().Disconnect("alice", common.SandboxBrowserSession(session))
	})
	mux.HandleFunc("/fixture/reset-code", func(w http.ResponseWriter, r *http.Request) {
		_, err := live.Load().Reset("alice", common.SandboxBrowserSession(session), workspace, "code")
		if err != nil {
			http.Error(w, err.Error(), 500)
		}
	})
	mux.HandleFunc("/fixture/connect-crew", func(w http.ResponseWriter, r *http.Request) {
		if !live.Load().RequestProjectConnection("alice", common.SandboxBrowserSession(crewSession)) {
			http.Error(w, "not paired", 409)
			return
		}
		w.WriteHeader(202)
	})
	mux.HandleFunc("/fixture/disconnect-crew", func(w http.ResponseWriter, r *http.Request) {
		live.Load().Disconnect("alice", common.SandboxBrowserSession(crewSession))
	})
	mux.HandleFunc("/fixture/crew-status", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(live.Load().Status("alice", common.SandboxBrowserSession(crewSession)))
	})
	mux.HandleFunc("/fixture/connect-workflow", func(w http.ResponseWriter, r *http.Request) {
		if !live.Load().RequestProjectConnection("alice", common.SandboxBrowserSession(workflowSession)) {
			http.Error(w, "not paired", 409)
			return
		}
		w.WriteHeader(202)
	})
	mux.HandleFunc("/fixture/workflow-status", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(live.Load().Status("alice", common.SandboxBrowserSession(workflowSession)))
	})
	mux.HandleFunc("/fixture/cdp", func(w http.ResponseWriter, r *http.Request) {
		b := live.Load().Lookup("alice", common.SandboxBrowserSession(session))
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
		activeSession := session
		if r.URL.Query().Get("project") == "crew" {
			activeSession = crewSession
		}
		rootSession := activeSession
		if r.URL.Query().Get("project") == "workflow" {
			activeSession = "workflow-step-one"
			if r.URL.Query().Get("step") == "two" {
				activeSession = "workflow-step-two"
			}
			rootSession = workflowSession
		}
		ctx := context.WithValue(r.Context(), common.WorkflowSessionIDKey, rootSession)
		ctx = context.WithValue(ctx, common.ChatSessionIDKey, activeSession)
		ctx = context.WithValue(ctx, common.UserIDKey, "alice")
		output, err := executor.HandleAgentBrowser(ctx, args)
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.Write([]byte(output))
	})
	mux.HandleFunc("/fixture/interrupted-video", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(root, evidence, "interrupted.webm"))
	})
	mux.HandleFunc("/fixture/video", func(w http.ResponseWriter, r *http.Request) {
		video := filepath.Join(root, evidence, "recording.webm")
		// Decode the video, not only its container header/size.
		if output, err := exec.Command("ffmpeg", "-v", "error", "-xerror", "-i", video, "-frames:v", "1", "-f", "null", "-").CombinedOutput(); err != nil {
			http.Error(w, "Invalid video: "+string(output), 500)
			return
		}
		http.ServeFile(w, r, video)
	})
	mux.HandleFunc("/fixture/image", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(root, evidence, "screenshot.png"))
	})
	mux.HandleFunc("/fixture", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "fixture_login", Value: "retained", Path: "/"})
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><title>Shared fixture</title><h1>Chrome bridge fixture</h1><label>Name <input id="name"></label><button id="save" onclick="document.querySelector('#result').textContent='Saved '+document.querySelector('#name').value">Save customer</button><p id="result">Ready</p>`))
	})
	mux.HandleFunc("/fixture/frames", func(w http.ResponseWriter, r *http.Request) {
		_, port, err := net.SplitHostPort(r.Host)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!doctype html><title>Cross-site fixture</title><h1>Parent loaded</h1><iframe src="http://localhost:%s/fixture/child"></iframe>`, port)
	})
	mux.HandleFunc("/fixture/child", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><title>Child fixture</title><h1 id="state">Waiting</h1><script>document.querySelector('#state').textContent='Child resumed';</script>`))
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
	driver := "test-chrome-extension-e2e.mjs"
	if os.Getenv("CHROME_EXTENSION_E2E_STANDALONE") == "1" {
		driver = "test-chrome-extension-standalone.mjs"
	}
	cmd := exec.Command("node", filepath.Join(project, "scripts", driver))
	cmd.Env = append(os.Environ(), "CHROME_EXTENSION_E2E_URL="+server.URL, "CHROME_EXTENSION_E2E_TOKEN="+token, "CHROME_EXTENSION_E2E_SCOPE="+common.SandboxBrowserSession(session), "CHROME_EXTENSION_E2E_CREW_SCOPE="+common.SandboxBrowserSession(crewSession), "CHROME_EXTENSION_E2E_WORKFLOW_SCOPE="+common.SandboxBrowserSession(workflowSession))
	output, err := cmd.CombinedOutput()
	t.Log(string(output))
	if msg := workspacehandlers.CheckBrowserSessionLimit("agent-browser --session extension-e2e-second open about:blank", nativeEnv); msg == "" {
		t.Fatal("extension evicted the native capacity slot")
	}
	if err != nil {
		t.Fatal(err)
	}
}
