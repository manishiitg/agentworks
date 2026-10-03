package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecuteCommandHostDockerFallbackDefaultsToLocalhost(t *testing.T) {
	var got ShellExecuteRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(APIResponse{
			Success: true,
			Data: ShellExecuteResponse{
				Stdout:   "ok",
				ExitCode: 0,
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.ExecuteCommand(context.Background(), []string{
		"--session", "shared-cdp-9222",
		"tab", "list",
		"--cdp", "http://host.docker.internal:9222",
		"--json",
	}, &ExecuteOptions{Timeout: time.Second})
	if err != nil {
		t.Fatalf("ExecuteCommand() error = %v", err)
	}

	if !strings.Contains(got.Command, `if [ -z "$HOST_IP" ]; then HOST_IP=localhost; fi;`) {
		t.Fatalf("command missing empty HOST_IP fallback: %s", got.Command)
	}
	if !strings.Contains(got.Command, "http://${HOST_IP}:9222") {
		t.Fatalf("command did not replace host.docker.internal with HOST_IP: %s", got.Command)
	}
	if strings.Contains(got.Command, `|| echo 'localhost') &&`) {
		t.Fatalf("command uses pipeline fallback that leaves HOST_IP empty on native macOS: %s", got.Command)
	}
}

func TestQuoteShellArgProtectsCSSIDSelectorAndFollowingUploadPaths(t *testing.T) {
	if got := quoteShellArg("#upload"); got != "'#upload'" {
		t.Fatalf("quoted CSS selector = %q, want shell-safe quoted selector", got)
	}
	if got := quoteShellArg(`folder\report.pdf`); got != `'folder\report.pdf'` {
		t.Fatalf("quoted backslash path = %q", got)
	}
}

func TestExecuteCommandKeepsStructuredStdoutWhenMacOSGetcwdWarningIsPresent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(APIResponse{
			Success: true,
			Data: ShellExecuteResponse{
				Stdout:   `{"success":false,"error":"real browser failure"}`,
				Stderr:   "shell-init: error retrieving current directory: getcwd: cannot access parent directories: Operation not permitted\n",
				ExitCode: 1,
			},
		})
	}))
	defer server.Close()

	_, err := NewClient(server.URL).ExecuteCommand(context.Background(), []string{"screenshot", "out.png"}, &ExecuteOptions{Timeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "real browser failure") {
		t.Fatalf("expected structured browser error, got %v", err)
	}
	if strings.Contains(err.Error(), "getcwd") {
		t.Fatalf("misleading getcwd warning leaked into error: %v", err)
	}
}

func TestFinalizeArtifactUsesPlainNoopCommand(t *testing.T) {
	var got ShellExecuteRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(APIResponse{Success: true, Data: ShellExecuteResponse{ExitCode: 0}})
	}))
	defer server.Close()

	transfer := &ArtifactTransfer{SourcePath: "/tmp/agentworks-browser-artifacts/a.webm", DestinationPath: "evidence/a.webm", Kind: "video"}
	err := NewClient(server.URL).FinalizeArtifact(context.Background(), transfer, &ExecuteOptions{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if got.Command != "true" {
		t.Fatalf("transfer-only command = %q, want true", got.Command)
	}
	if got.ArtifactTransfer == nil || got.ArtifactTransfer.SourcePath != transfer.SourcePath {
		t.Fatalf("artifact transfer missing from request: %#v", got.ArtifactTransfer)
	}
	if !got.ArtifactTransfer.Finalize {
		t.Fatalf("transfer-only request did not request finalization: %#v", got.ArtifactTransfer)
	}
}

func TestCDPRuntimeStartupErrorRecognized(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "auto launch", err: errString("Auto-launch failed: Invalid CDP URL: empty host"), want: true},
		{name: "invalid cdp", err: errString("Invalid CDP URL: empty host"), want: true},
		{name: "timeout", err: errString("command timed out after 30s"), want: false},
		{name: "nil", err: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCDPRuntimeStartupError(tt.err); got != tt.want {
				t.Fatalf("isCDPRuntimeStartupError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldRetryCDPTimeout(t *testing.T) {
	if !shouldRetryCDPTimeout("snapshot") {
		t.Fatalf("snapshot timeouts should retry")
	}
	if !shouldRetryCDPTimeout("get") {
		t.Fatalf("get timeouts should retry")
	}
	if !shouldRetryCDPTimeout("skills") {
		t.Fatalf("read-only skill documentation timeouts should retry")
	}
	if shouldRetryCDPTimeout("wait") {
		t.Fatalf("wait timeouts should not retry the same wait condition")
	}
	for _, command := range []string{"click", "fill", "press", "open", "upload", "download", "record", "network", "eval"} {
		if shouldRetryCDPTimeout(command) {
			t.Fatalf("potentially side-effecting %q timeout must not retry", command)
		}
	}
}

func TestBrowserDocumentationCommandDoesNotRequirePageState(t *testing.T) {
	if !isBrowserDocumentationCommand("skills") {
		t.Fatal("skills should be treated as a read-only documentation command")
	}
	for _, command := range []string{"open", "tab", "snapshot", "record", "network"} {
		if isBrowserDocumentationCommand(command) {
			t.Fatalf("%q unexpectedly treated as documentation-only", command)
		}
	}
}

func TestExtractCDPArgRejectsDuplicates(t *testing.T) {
	_, _, err := extractCDPArg([]string{"--cdp", "9222", "--cdp", "http://localhost:9222", "get", "url"})
	if err == nil || !strings.Contains(err.Error(), "only be provided once") {
		t.Fatalf("expected duplicate --cdp to be rejected, got: %v", err)
	}
}

func TestCDPExclusiveFeatureAction(t *testing.T) {
	tests := []struct {
		command string
		args    []string
		feature string
		action  string
	}{
		{command: "record", args: []string{"start", "qa.webm"}, feature: "video recording", action: "start"},
		{command: "trace", args: []string{"stop", "trace.json"}, feature: "DevTools trace", action: "stop"},
		{command: "profiler", args: []string{"restart"}, feature: "DevTools profile", action: "restart"},
		{command: "network", args: []string{"har", "start"}, feature: "HAR capture", action: "start"},
		{command: "network", args: []string{"requests"}},
	}
	for _, tt := range tests {
		feature, action := cdpExclusiveFeatureAction(tt.command, tt.args)
		if feature != tt.feature || action != tt.action {
			t.Fatalf("%s %v: got (%q, %q), want (%q, %q)", tt.command, tt.args, feature, action, tt.feature, tt.action)
		}
	}
}

func TestHandleAgentBrowserLoadsSkillsInCDPModeWithoutSelectedTab(t *testing.T) {
	t.Setenv("CDP_HOST", "localhost")
	var got ShellExecuteRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(APIResponse{
			Success: true,
			Data: ShellExecuteResponse{
				Stdout:   `{"success":true,"data":[{"name":"core","content":"# Current core skill\n\nUse snapshot first."}]}`,
				ExitCode: 0,
			},
		})
	}))
	defer server.Close()

	executor := NewExecutor(NewClient(server.URL), WithCdpPort(9222))
	result, err := executor.HandleAgentBrowser(context.Background(), map[string]interface{}{
		"command": "skills",
		"args":    []string{"--cdp", "http://localhost:9222", "get", "core"},
		"session": "default",
	})
	if err != nil {
		t.Fatalf("HandleAgentBrowser() error = %v", err)
	}
	if !strings.Contains(result, "# Current core skill") || !strings.Contains(result, "Builder adapter note") {
		t.Fatalf("result = %q, want version-matched core skill", result)
	}
	for _, want := range []string{"skills get core", "--cdp http://localhost:9222", "--json"} {
		if !strings.Contains(got.Command, want) {
			t.Fatalf("workspace command %q missing %q", got.Command, want)
		}
	}
}

func TestFormatAgentBrowserSkillsListAsReadableMarkdown(t *testing.T) {
	got := formatAgentBrowserSkillsOutput(`{"success":true,"data":[{"name":"core","description":"Core browser usage"},{"name":"dogfood","description":"Exploratory QA"}]}`)
	for _, want := range []string{"Builder adapter note", "`core` — Core browser usage", "`dogfood` — Exploratory QA"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted skills output %q missing %q", got, want)
		}
	}
}

func TestResolveCDPInvocationRequiresExplicitMatchingEndpoint(t *testing.T) {
	t.Setenv("CDP_HOST", "browser-host")

	port, endpoint, err := resolveCDPInvocation([]int{9222}, cdpArgInfo{found: true, url: "http://localhost:9222", port: 9222})
	if err != nil {
		t.Fatalf("resolveCDPInvocation() error = %v", err)
	}
	if port != 9222 || endpoint != "http://browser-host:9222" {
		t.Fatalf("resolveCDPInvocation() = (%d, %q), want (9222, canonical backend endpoint)", port, endpoint)
	}

	if _, _, err := resolveCDPInvocation([]int{9222}, cdpArgInfo{}); err == nil || !strings.Contains(err.Error(), "requires an explicit --cdp") {
		t.Fatalf("missing explicit CDP endpoint error = %v", err)
	}
	if _, _, err := resolveCDPInvocation([]int{9222}, cdpArgInfo{found: true, url: "http://localhost:9223", port: 9223}); err == nil || !strings.Contains(err.Error(), "CDP_CONFIGURATION_MISMATCH") {
		t.Fatalf("mismatched CDP endpoint error = %v", err)
	}
	if _, _, err := resolveCDPInvocation(nil, cdpArgInfo{found: true, url: "http://localhost:9222", port: 9222}); err == nil || !strings.Contains(err.Error(), "CDP_NOT_CONFIGURED") {
		t.Fatalf("headless-to-CDP switch error = %v", err)
	}
}

func TestResolveCDPInvocationAllowsOnlyConfiguredPorts(t *testing.T) {
	t.Setenv("CDP_HOST", "localhost")
	for _, wantPort := range []int{9222, 9333} {
		port, endpoint, err := resolveCDPInvocation([]int{9222, 9333}, cdpArgInfo{
			found: true,
			url:   fmt.Sprintf("http://localhost:%d", wantPort),
			port:  wantPort,
		})
		if err != nil || port != wantPort || endpoint != fmt.Sprintf("http://localhost:%d", wantPort) {
			t.Fatalf("configured port %d resolved to (%d, %q, %v)", wantPort, port, endpoint, err)
		}
	}
	if _, _, err := resolveCDPInvocation([]int{9222, 9333}, cdpArgInfo{found: true, url: "http://localhost:9444", port: 9444}); err == nil || !strings.Contains(err.Error(), "[9222 9333]") {
		t.Fatalf("unconfigured port should be rejected with allowed ports, got: %v", err)
	}
}

func TestCDPUnavailableErrorUsesPortSpecificProfile(t *testing.T) {
	err := cdpUnavailableError(9333, fmt.Errorf("connection refused"))
	if !strings.Contains(err.Error(), `.chrome-cdp-profile-9333`) {
		t.Fatalf("error did not use a port-specific profile directory: %v", err)
	}
}

func TestParseCDPPortAcceptsURLAndBarePort(t *testing.T) {
	for _, input := range []string{"9222", "http://localhost:9222", "ws://localhost:9222/devtools/browser/test"} {
		if got := parseCDPPort(input); got != 9222 {
			t.Fatalf("parseCDPPort(%q) = %d, want 9222", input, got)
		}
	}
}

func TestAgentBrowserStatusResolvesAutoModeLive(t *testing.T) {
	var cdpReachable atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cdp-check":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"connected": cdpReachable.Load(),
			})
		case "/api/execute":
			_ = json.NewEncoder(w).Encode(APIResponse{
				Success: true,
				Data:    ShellExecuteResponse{Stdout: `{"success":true}`, ExitCode: 0},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runtime := NewBrowserRuntimeConfig("auto", []int{9222})
	executor := NewExecutor(NewClient(server.URL), WithBrowserRuntimeConfig(runtime))
	status := func() browserRuntimeStatus {
		t.Helper()
		result, err := executor.HandleAgentBrowser(context.Background(), map[string]interface{}{
			"command": "status",
			"session": "default",
		})
		if err != nil {
			t.Fatalf("status error: %v", err)
		}
		var got browserRuntimeStatus
		if err := json.Unmarshal([]byte(result), &got); err != nil {
			t.Fatalf("decode status %q: %v", result, err)
		}
		return got
	}

	if got := status(); got.ConfiguredMode != "auto" || got.EffectiveMode != "headless" || len(got.ReachableCDPPorts) != 0 {
		t.Fatalf("unreachable status = %#v", got)
	}

	cdpReachable.Store(true)
	if got := status(); got.EffectiveMode != "cdp" || len(got.ReachableCDPPorts) != 1 || got.ReachableCDPPorts[0] != 9222 || len(got.AuthorizedEndpoints) != 1 {
		t.Fatalf("reachable status = %#v", got)
	}

	// The same executor must now require the live CDP endpoint; no session or
	// prompt restart is involved in the headless -> CDP transition.
	if _, err := executor.HandleAgentBrowser(context.Background(), map[string]interface{}{
		"command": "open",
		"args":    []string{"https://example.com"},
		"session": "default",
	}); err == nil || !strings.Contains(err.Error(), "requires an explicit --cdp") {
		t.Fatalf("auto mode did not enforce newly reachable CDP: %v", err)
	}
}

func TestBrowserRuntimeConfigUpdateDoesNotStoreResolvedAvailability(t *testing.T) {
	runtime := NewBrowserRuntimeConfig("auto", []int{9222})
	mode, ports := runtime.Snapshot()
	if mode != "auto" || len(ports) != 1 || ports[0] != 9222 {
		t.Fatalf("initial snapshot = mode=%q ports=%v", mode, ports)
	}
	runtime.Update("auto", []int{9333})
	mode, ports = runtime.Snapshot()
	if mode != "auto" || len(ports) != 1 || ports[0] != 9333 {
		t.Fatalf("updated snapshot = mode=%q ports=%v", mode, ports)
	}
}

type errString string

func (e errString) Error() string {
	return string(e)
}

func TestExecuteCommandPassesTrustedAccountAndScope(t *testing.T) {
	t.Setenv("WORKSPACE_API_TOKEN", "service-token")
	var got ShellExecuteRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Workspace-Token") != "service-token" || r.Header.Get("X-User-ID") != "alice" {
			t.Error("missing trusted identity")
		}
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(APIResponse{Success: true, Data: ShellExecuteResponse{Stdout: "ok"}})
	}))
	defer server.Close()
	guard := &FolderGuardConfig{Enabled: true, BrowserSession: "workflow-1111111111111111--browser", ReadPaths: []string{"Workflow/a/"}, WritePaths: []string{"Workflow/a/"}}
	_, err := NewClient(server.URL).ExecuteCommand(context.Background(), []string{"get", "url"}, &ExecuteOptions{UserID: "alice", WorkingDirectory: "Workflow/a", FolderGuard: guard})
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkingDirectory != "Workflow/a" || got.FolderGuard == nil || got.FolderGuard.BrowserSession != guard.BrowserSession || !got.FolderGuard.Enabled {
		t.Fatal("scope guard not forwarded", got)
	}
}
