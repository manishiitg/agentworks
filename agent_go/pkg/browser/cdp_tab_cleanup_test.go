package browser

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func newCDPCleanupTestClient(t *testing.T) (*Client, <-chan ShellExecuteRequest) {
	t.Helper()
	requests := make(chan ShellExecuteRequest, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request ShellExecuteRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests <- request
		_ = json.NewEncoder(w).Encode(APIResponse{
			Success: true,
			Data: ShellExecuteResponse{
				Stdout:   `{"success":true}`,
				ExitCode: 0,
			},
		})
	}))
	t.Cleanup(server.Close)
	return NewClient(server.URL), requests
}

func awaitCDPCleanupRequest(t *testing.T, requests <-chan ShellExecuteRequest) ShellExecuteRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for delayed CDP tab cleanup")
		return ShellExecuteRequest{}
	}
}

func assertNoCDPCleanupRequest(t *testing.T, requests <-chan ShellExecuteRequest, wait time.Duration) {
	t.Helper()
	select {
	case request := <-requests:
		t.Fatalf("unexpected CDP cleanup request: %s", request.Command)
	case <-time.After(wait):
	}
}

func TestDelayedCDPTabCleanupClosesOnlyWorkflowOwnedTabs(t *testing.T) {
	resetCDPRegistryForTest(t)
	t.Setenv("CDP_HOST", "localhost")
	client, requests := newCDPCleanupTestClient(t)
	const (
		port  = 29222
		owner = "workflow-cleanup-owner"
	)

	setCDPTabAlias(port, owner, "workflow-created", "t7")
	markCDPTabOwned(port, owner, "workflow-created", "t7")
	setCDPTabAlias(port, owner, "user-existing", "t8")

	AcquireCDPTabOwnerLease(owner, []int{port})
	ReleaseCDPTabOwnerLease(owner, []int{port}, client, 10*time.Millisecond)

	request := awaitCDPCleanupRequest(t, requests)
	for _, want := range []string{"--session shared-cdp-29222", "tab close t7", "--cdp http://localhost:29222"} {
		if !strings.Contains(request.Command, want) {
			t.Fatalf("cleanup command %q missing %q", request.Command, want)
		}
	}
	if strings.Contains(request.Command, "t8") {
		t.Fatalf("cleanup must not close a pre-existing user tab: %s", request.Command)
	}
	assertNoCDPCleanupRequest(t, requests, 40*time.Millisecond)
	if tabs := ownedCDPTabsForOwner(port, owner); len(tabs) != 0 {
		t.Fatalf("owned tabs after cleanup = %#v, want none", tabs)
	}
	if got := getCDPTabAlias(port, owner, "user-existing"); got != "t8" {
		t.Fatalf("pre-existing tab alias was changed: got %q", got)
	}
}

func TestDelayedCDPTabCleanupWaitsForLastConcurrentRun(t *testing.T) {
	resetCDPRegistryForTest(t)
	t.Setenv("CDP_HOST", "localhost")
	client, requests := newCDPCleanupTestClient(t)
	const (
		port  = 29223
		owner = "workflow-concurrent-owner"
	)
	markCDPTabOwned(port, owner, "shared-work", "t11")

	AcquireCDPTabOwnerLease(owner, []int{port})
	AcquireCDPTabOwnerLease(owner, []int{port})
	ReleaseCDPTabOwnerLease(owner, []int{port}, client, 10*time.Millisecond)
	assertNoCDPCleanupRequest(t, requests, 50*time.Millisecond)

	ReleaseCDPTabOwnerLease(owner, []int{port}, client, 10*time.Millisecond)
	request := awaitCDPCleanupRequest(t, requests)
	if !strings.Contains(request.Command, "tab close t11") {
		t.Fatalf("cleanup command = %q, want final-run tab close", request.Command)
	}
}

func TestNewCDPTabLeaseCancelsPendingCleanup(t *testing.T) {
	resetCDPRegistryForTest(t)
	t.Setenv("CDP_HOST", "localhost")
	client, requests := newCDPCleanupTestClient(t)
	const (
		port  = 29224
		owner = "workflow-renewed-owner"
	)
	markCDPTabOwned(port, owner, "renewed-work", "t12")

	AcquireCDPTabOwnerLease(owner, []int{port})
	ReleaseCDPTabOwnerLease(owner, []int{port}, client, 80*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	AcquireCDPTabOwnerLease(owner, []int{port})
	assertNoCDPCleanupRequest(t, requests, 100*time.Millisecond)

	ReleaseCDPTabOwnerLease(owner, []int{port}, client, 10*time.Millisecond)
	request := awaitCDPCleanupRequest(t, requests)
	if !strings.Contains(request.Command, "tab close t12") {
		t.Fatalf("cleanup command = %q, want renewed-run tab close", request.Command)
	}
}

func TestAgentBrowserTabNewRegistersCleanupOwnership(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	resetCDPRegistryForTest(t)
	t.Setenv("CDP_HOST", "localhost")

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		stdout := `{"success":true,"data":{"tabs":[]}}`
		if requestCount > 1 {
			stdout = `{"success":true,"data":{"active":true,"label":"report","tabId":"t42","url":"https://example.com/"}}`
		}
		_ = json.NewEncoder(w).Encode(APIResponse{
			Success: true,
			Data: ShellExecuteResponse{
				Stdout:   stdout,
				ExitCode: 0,
			},
		})
	}))
	defer server.Close()

	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, "workflow-tab-owner")
	executor := NewExecutor(NewClient(server.URL), WithCdpPort(29225))
	if _, err := executor.HandleAgentBrowser(ctx, map[string]interface{}{
		"command": "tab",
		"args":    []string{"--cdp", "http://localhost:29225", "new", "--label", "report", "https://example.com"},
		"session": "agent-session",
	}); err != nil {
		t.Fatalf("tab new failed: %v", err)
	}

	tabs := ownedCDPTabsForOwner(29225, "workflow-tab-owner")
	if len(tabs) != 1 || tabs[0].Alias != "report" || tabs[0].TabID != "t42" {
		t.Fatalf("registered owned tabs = %#v, want report/t42", tabs)
	}
	// PLAT-746-444: a successful close returned only "No selected CDP tab",
	// which made a real Python browser agent report its task as failed.
	closed, err := executor.HandleAgentBrowser(ctx, map[string]interface{}{
		"command": "tab",
		"args":    []string{"--cdp", "http://localhost:29225", "close", "t42"},
		"session": "agent-session",
	})
	if err != nil || !strings.Contains(strings.ToLower(closed), "closed") || !strings.Contains(closed, "t42") || strings.Contains(closed, "No selected CDP tab") {
		t.Fatalf("successful close looked like missing browser state: %q %v", closed, err)
	}
	if tabs := ownedCDPTabsForOwner(29225, "workflow-tab-owner"); len(tabs) != 0 {
		t.Fatalf("closed tab retained cleanup ownership: %#v", tabs)
	}
}

func TestAgentBrowserTabNewReusesExactURLWithoutCreatingOrOwningTab(t *testing.T) {
	resetCDPRegistryForTest(t)
	t.Setenv("CDP_HOST", "localhost")
	const port = 29226
	var commands []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request ShellExecuteRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, request.Command)
		stdout := `{"success":true,"data":{"tabs":[{"active":false,"label":null,"tabId":"t9","title":"Existing","url":"https://example.com/"}]}}`
		if strings.Contains(request.Command, "tab t9") {
			stdout = `{"success":true,"data":{"active":true,"label":null,"tabId":"t9","title":"Existing","url":"https://example.com/"}}`
		}
		_ = json.NewEncoder(w).Encode(APIResponse{Success: true, Data: ShellExecuteResponse{Stdout: stdout, ExitCode: 0}})
	}))
	defer server.Close()

	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, "reuse-owner")
	result, err := NewExecutor(NewClient(server.URL), WithCdpPort(port)).HandleAgentBrowser(ctx, map[string]interface{}{
		"command": "tab",
		"args":    []string{"--cdp", "http://localhost:29226", "new", "--label", "requested-label", "https://example.com"},
		"session": "agent-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Reused existing CDP tab") || !strings.Contains(result, "id=t9") {
		t.Fatalf("reuse result = %q", result)
	}
	for _, command := range commands {
		if strings.Contains(command, "tab new") {
			t.Fatalf("exact URL reuse unexpectedly created a tab: %q", command)
		}
	}
	if tabs := ownedCDPTabsForOwner(port, "reuse-owner"); len(tabs) != 0 {
		t.Fatalf("pre-existing reused tab must not become workflow-owned: %#v", tabs)
	}
}

func TestAgentBrowserTabNewRefusesCreationWhenReuseCheckIsInvalid(t *testing.T) {
	resetCDPRegistryForTest(t)
	t.Setenv("CDP_HOST", "localhost")
	const port = 29228
	var commands []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request ShellExecuteRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, request.Command)
		_ = json.NewEncoder(w).Encode(APIResponse{
			Success: true,
			Data:    ShellExecuteResponse{Stdout: "not-json", ExitCode: 0},
		})
	}))
	defer server.Close()

	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, "failed-reuse-check-owner")
	_, err := NewExecutor(NewClient(server.URL), WithCdpPort(port)).HandleAgentBrowser(ctx, map[string]interface{}{
		"command": "tab",
		"args":    []string{"--cdp", "http://localhost:29228", "new", "--label", "requested-label", "https://example.com"},
		"session": "agent-session",
	})
	if err == nil || !strings.Contains(err.Error(), "CDP_TAB_REUSE_CHECK_INVALID") {
		t.Fatalf("error = %v, want CDP_TAB_REUSE_CHECK_INVALID", err)
	}
	if len(commands) != 1 || strings.Contains(commands[0], "tab new") {
		t.Fatalf("commands = %#v, want only the failed tab-list check", commands)
	}
}

func TestAgentBrowserPreservesFocusWhenRealTabIsAlreadyActive(t *testing.T) {
	resetCDPRegistryForTest(t)
	t.Setenv("CDP_HOST", "localhost")
	const (
		port  = 29227
		owner = "explicit-select-owner"
	)
	setCDPTabAlias(port, owner, "workflow-tab", "t7")
	setCDPTabSelection(port, owner, "t7")
	setCDPActiveTab(port, "t7")

	var commands []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request ShellExecuteRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, request.Command)
		stdout := `{"success":true,"data":{"active":true,"label":"workflow-tab","tabId":"t7","url":"https://example.com/"}}`
		if strings.Contains(request.Command, "snapshot") {
			stdout = `{"success":true,"data":{"snapshot":"ok"}}`
		}
		_ = json.NewEncoder(w).Encode(APIResponse{Success: true, Data: ShellExecuteResponse{Stdout: stdout, ExitCode: 0}})
	}))
	defer server.Close()

	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, owner)
	_, err := NewExecutor(NewClient(server.URL), WithCdpPort(port)).HandleAgentBrowser(ctx, map[string]interface{}{
		"command": "snapshot",
		"args":    []string{"--cdp", "http://localhost:29227", "tab", "workflow-tab", "-i"},
		"session": "agent-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 || strings.Contains(commands[0], "tab t7") || !strings.Contains(commands[0], " tab ") || !strings.Contains(commands[1], "snapshot") {
		t.Fatalf("commands = %#v, want focus-preserving tab-state check followed by snapshot", commands)
	}
}

func TestAgentBrowserBareTabIsNotForwardedAsPageActionArgument(t *testing.T) {
	resetCDPRegistryForTest(t)
	t.Setenv("CDP_HOST", "localhost")
	const (
		port  = 29229
		owner = "bare-tab-owner"
	)

	var commands []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request ShellExecuteRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, request.Command)
		stdout := `{"success":true,"data":{"tabs":[{"active":true,"tabId":"t2","url":"https://example.com/"}]}}`
		if strings.Contains(request.Command, " click ") {
			stdout = `{"success":true,"data":{"clicked":true}}`
		}
		_ = json.NewEncoder(w).Encode(APIResponse{Success: true, Data: ShellExecuteResponse{Stdout: stdout, ExitCode: 0}})
	}))
	defer server.Close()

	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, owner)
	_, err := NewExecutor(NewClient(server.URL), WithCdpPort(port)).HandleAgentBrowser(ctx, map[string]interface{}{
		"command": "click",
		"args":    []string{"--cdp", "http://localhost:29229", "t2", "e64"},
		"session": "agent-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 {
		t.Fatalf("commands = %#v, want tab-state check followed by click", commands)
	}
	clickCommand := commands[1]
	for _, unwanted := range []string{" click t2 ", " t2 e64"} {
		if strings.Contains(clickCommand, unwanted) {
			t.Fatalf("bare CDP tab leaked into page-action arguments: %q", clickCommand)
		}
	}
	for _, want := range []string{" click e64 ", " --cdp http://localhost:29229 ", " --json"} {
		if !strings.Contains(clickCommand, want) {
			t.Fatalf("click command %q missing %q", clickCommand, want)
		}
	}
}

func TestAgentBrowserSelectsRealTabWhenAnotherTabIsActive(t *testing.T) {
	resetCDPRegistryForTest(t)
	t.Setenv("CDP_HOST", "localhost")
	const (
		port  = 29226
		owner = "inactive-tab-owner"
	)
	setCDPTabAlias(port, owner, "workflow-tab", "t7")
	setCDPTabSelection(port, owner, "t7")

	var commands []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request ShellExecuteRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, request.Command)
		stdout := `{"success":true,"data":{"tabs":[{"active":false,"label":"workflow-tab","tabId":"t7","url":"https://example.com/"},{"active":true,"label":"other","tabId":"t8","url":"https://other.example/"}]}}`
		if strings.Contains(request.Command, "tab t7") {
			stdout = `{"success":true,"data":{"active":true,"label":"workflow-tab","tabId":"t7","url":"https://example.com/"}}`
		} else if strings.Contains(request.Command, "snapshot") {
			stdout = `{"success":true,"data":{"snapshot":"ok"}}`
		}
		_ = json.NewEncoder(w).Encode(APIResponse{Success: true, Data: ShellExecuteResponse{Stdout: stdout, ExitCode: 0}})
	}))
	defer server.Close()

	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, owner)
	_, err := NewExecutor(NewClient(server.URL), WithCdpPort(port)).HandleAgentBrowser(ctx, map[string]interface{}{
		"command": "snapshot",
		"args":    []string{"--cdp", "http://localhost:29226", "tab", "workflow-tab", "-i"},
		"session": "agent-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 3 || !strings.Contains(commands[1], "tab t7") || !strings.Contains(commands[2], "snapshot") {
		t.Fatalf("commands = %#v, want state check, required tab select, then snapshot", commands)
	}
}

func TestMissingCDPTabErrorRecognizesMissingLabel(t *testing.T) {
	if !isMissingCDPTabError(errors.New("No tab with label `old-label`; run agent-browser tab")) {
		t.Fatal("missing-label cleanup error should retire stale ownership instead of retrying forever")
	}
}
