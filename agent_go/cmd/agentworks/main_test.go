//go:build !windows

// These contract tests need the server product definition, which does not build on Windows; the Windows CLI tests are in
// executor_test.go.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestCLIPlanAndFileArguments(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		input  string
		tool   string
		fields map[string]any
	}{
		{[]string{"plan", "get", "--workflow", "wf-1"}, "", "get_plan", map[string]any{"workflow_id": "wf-1"}},
		{[]string{"files", "read", "--workflow", "wf-1", "--path", "notes.md"}, "", "read_file", map[string]any{"workflow_id": "wf-1", "path": "notes.md"}},
		{[]string{"files", "list", "--workflow", "wf-1", "--path", "code", "--glob", "**/*.py"}, "", "list_files", map[string]any{"workflow_id": "wf-1", "path": "code", "glob": "**/*.py"}},
		{[]string{"files", "code", "--workflow", "wf-1", "--step-id", "run-basic-smoke"}, "", "list_step_code", map[string]any{"workflow_id": "wf-1", "step_id": "run-basic-smoke"}},
		{[]string{"guidance", "topic", "--topic", "plan-change-impact"}, "", "get_guidance_topic", map[string]any{"topic": "plan-change-impact"}},
		{[]string{"knowledge", "read", "--workflow", "wf-1", "--path", "learnings/_global/SKILL.md"}, "", "read_workflow_knowledge", map[string]any{"workflow_id": "wf-1", "path": "learnings/_global/SKILL.md"}},
		{[]string{"tools", "call", "native_future_tool", "--set", `nested={"enabled":true}`, "--set", `count=9007199254740993`}, "", "native_future_tool", map[string]any{"nested": map[string]any{"enabled": true}}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			var called bool
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if r.URL.Path != "/api/external/v1/call" || r.Header.Get("Authorization") != "Bearer jwt" {
					t.Errorf("request %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
				}
				var body struct {
					Name      string
					Arguments map[string]any
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Name != tc.tool {
					t.Errorf("name=%s", body.Name)
				}
				for k, v := range tc.fields {
					actual, _ := json.Marshal(body.Arguments[k])
					expected, _ := json.Marshal(v)
					if string(actual) != string(expected) {
						t.Errorf("%s: got %s want %s", k, actual, expected)
					}
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer s.Close()
			var stdout, stderr bytes.Buffer
			args := append([]string{"--config", filepath.Join(t.TempDir(), "config.json"), "--server", s.URL, "--json"}, tc.args...)
			code := run(context.Background(), args, strings.NewReader(tc.input), &stdout, &stderr, func(key string) string {
				if key == "AGENTWORKS_TOKEN" {
					return "jwt"
				}
				return ""
			})
			if code != 0 || !called || stdout.String() != "{\"ok\":true}\n" || stderr.Len() != 0 {
				t.Fatalf("code=%d called=%v out=%s err=%s", code, called, &stdout, &stderr)
			}
		})
	}
}

func TestCLIRunAndScheduleArguments(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		tool   string
		fields map[string]any
	}{
		{[]string{"runs", "start-step", "--workflow", "wf-1", "--step-id", "fetch", "--group", "g1", "--tier", "high"}, "execute_step", map[string]any{"workflow_id": "wf-1", "step_id": "fetch", "group_name": "g1", "tier": "high"}},
		{[]string{"runs", "start-step", "--workflow", "wf-1", "--step-id", "fetch", "--set", `script_parameters={"limit":10}`}, "execute_step", map[string]any{"workflow_id": "wf-1", "step_id": "fetch", "script_parameters": map[string]any{"limit": float64(10)}}},
		{[]string{"runs", "start-workflow", "--workflow", "wf-1", "--group", "g1"}, "run_full_workflow", map[string]any{"workflow_id": "wf-1", "group_name": "g1"}},
		{[]string{"runs", "status", "--workflow", "wf-1", "--session", "s1"}, "run_status", map[string]any{"workflow_id": "wf-1", "session_id": "s1"}},
		{[]string{"runs", "message", "--workflow", "wf-1", "--session", "s1", "--execution-id", "e1", "--message", "slow down"}, "send_step_message", map[string]any{"workflow_id": "wf-1", "session_id": "s1", "execution_id": "e1", "message": "slow down"}},
		{[]string{"runs", "stop", "--workflow", "wf-1", "--session", "s1", "--execution-id", "e1"}, "stop_step", map[string]any{"workflow_id": "wf-1", "session_id": "s1", "execution_id": "e1"}},
		{[]string{"runs", "stop-all", "--workflow", "wf-1", "--session", "s1"}, "stop_all_executions", map[string]any{"workflow_id": "wf-1", "session_id": "s1"}},
		{[]string{"runs", "executions", "--workflow", "wf-1"}, "list_executions", map[string]any{"workflow_id": "wf-1"}},
		{[]string{"schedules", "list", "--workflow", "wf-1"}, "list_schedules", map[string]any{"workflow_id": "wf-1"}},
		{[]string{"schedules", "runs", "--workflow", "wf-1", "--schedule-id", "daily"}, "get_schedule_runs", map[string]any{"workflow_id": "wf-1", "schedule_id": "daily"}},
		{[]string{"schedules", "trigger", "--workflow", "wf-1", "--schedule-id", "daily"}, "trigger_schedule", map[string]any{"workflow_id": "wf-1", "schedule_id": "daily"}},
		{[]string{"chat", "ask", "--workflow", "wf-1", "--message", "why did step 1 fail?"}, "chat", map[string]any{"workflow_id": "wf-1", "message": "why did step 1 fail?"}},
		{[]string{"chat", "ask", "--workflow", "wf-1", "--session", "s1", "--message", "and then?"}, "chat", map[string]any{"workflow_id": "wf-1", "session_id": "s1", "message": "and then?"}},
		{[]string{"runs", "reply", "--workflow", "wf-1", "--session", "s1", "--request-id", "r1", "--response", "yes"}, "run_reply_input", map[string]any{"workflow_id": "wf-1", "session_id": "s1", "request_id": "r1", "response": "yes"}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			var called bool
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				var body struct {
					Name      string
					Arguments map[string]any
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Name != tc.tool {
					t.Errorf("name=%s", body.Name)
				}
				for k, v := range tc.fields {
					actual, _ := json.Marshal(body.Arguments[k])
					expected, _ := json.Marshal(v)
					if string(actual) != string(expected) {
						t.Errorf("%s: got %s want %s", k, actual, expected)
					}
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer s.Close()
			var stdout, stderr bytes.Buffer
			args := append([]string{"--config", filepath.Join(t.TempDir(), "config.json"), "--server", s.URL, "--json"}, tc.args...)
			code := run(context.Background(), args, strings.NewReader(""), &stdout, &stderr, func(key string) string {
				if key == "AGENTWORKS_TOKEN" {
					return "jwt"
				}
				return ""
			})
			if code != 0 || !called || stdout.String() != "{\"ok\":true}\n" || stderr.Len() != 0 {
				t.Fatalf("code=%d called=%v out=%s err=%s", code, called, &stdout, &stderr)
			}
		})
	}
}

func TestCLIWriteCommandsRejected(t *testing.T) {
	for _, args := range [][]string{
		{"plan", "update-scripted-step", "--workflow", "wf-1"},
		{"files", "write", "--workflow", "wf-1", "--path", "notes.md"},
		{"files", "patch", "--workflow", "wf-1", "--path", "notes.md"},
		{"builder", "chat", "--workflow", "wf-1", "--message", "hi"},
	} {
		var stdout, stderr bytes.Buffer
		full := append([]string{"--config", filepath.Join(t.TempDir(), "config.json"), "--server", "http://127.0.0.1:1", "--json"}, args...)
		if code := run(context.Background(), full, strings.NewReader(""), &stdout, &stderr, func(string) string { return "jwt" }); code == 0 {
			t.Fatalf("%v accepted", args)
		}
	}
}

func TestSkillsInstallWritesBundledSkill(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"skills", "install", "--dir", dir}, strings.NewReader(""), &stdout, &stderr, func(string) string { return "" })
	if code != 0 {
		t.Fatalf("code=%d err=%s", code, &stderr)
	}
	data, err := os.ReadFile(filepath.Join(dir, "agentworks", "SKILL.md"))
	if err != nil || !strings.Contains(string(data), "`help` action=context") {
		t.Fatalf("installed skill missing guidance entrypoint: %v", err)
	}
	// A second install refuses to clobber; --force overwrites.
	var second bytes.Buffer
	if code := run(context.Background(), []string{"skills", "install", "--dir", dir}, strings.NewReader(""), &stdout, &second, func(string) string { return "" }); code == 0 {
		t.Fatal("reinstall without --force succeeded")
	}
	stdout.Reset()
	if code := run(context.Background(), []string{"skills", "install", "--dir", dir, "--force"}, strings.NewReader(""), &stdout, &stderr, func(string) string { return "" }); code != 0 {
		t.Fatalf("force reinstall failed: %s", &stderr)
	}
}

func TestInvalidInputDoesNotCallServer(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{} {}`, `{"x":`} {
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), []string{"--config", filepath.Join(t.TempDir(), "missing"), "tools", "call", "get_plan", "--input", "-"}, strings.NewReader(raw), &stdout, &stderr, func(string) string { return "" })
		if code == 0 || !strings.Contains(stderr.String(), "--input") {
			t.Fatalf("input %q: code %d %s", raw, code, &stderr)
		}
	}
}

func TestLoginLogoutAndServerCredentialIsolation(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer aw_pat_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
			t.Error("wrong login token")
		}
		_, _ = w.Write([]byte(`{"tools":[]}`))
	}))
	defer s.Close()
	path := filepath.Join(t.TempDir(), "config.json")
	var stdout, stderr bytes.Buffer
	env := func(string) string { return "" }
	code := run(context.Background(), []string{"--config", path, "--server", s.URL, "login", "--token-stdin"}, strings.NewReader("aw_pat_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"), &stdout, &stderr, env)
	if code != 0 {
		t.Fatalf("login: %s", &stderr)
	}
	if strings.Contains(stdout.String(), "aw_pat_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatal("token exposed in output")
	}
	cfg, err := agentworksclient.LoadConfig(path)
	if err != nil || cfg.Token != "aw_pat_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("saved: %#v %v", cfg, err)
	}
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	stdout.Reset()
	stderr.Reset()
	code = run(context.Background(), []string{"--config", path, "--server", target.URL, "tools", "list"}, strings.NewReader(""), &stdout, &stderr, env)
	if code == 0 || targetCalls.Load() != 0 {
		t.Fatal("saved token sent to server override")
	}
	stdout.Reset()
	stderr.Reset()
	code = run(context.Background(), []string{"--config", path, "logout"}, strings.NewReader(""), &stdout, &stderr, env)
	if code != 0 {
		t.Fatalf("logout: %s", &stderr)
	}
	cfg, err = agentworksclient.LoadConfig(path)
	if err != nil || cfg.Token != "" {
		t.Fatalf("logout retained token: %#v %v", cfg, err)
	}
}

func TestBrowserLoginRefreshAndLogout(t *testing.T) {
	var serverURL string
	var polls, refreshes, revokes atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/oauth/cli/device":
			_ = json.NewEncoder(w).Encode(map[string]any{"device_code": "cli_device_example", "verification_uri_complete": serverURL + "/oauth/cli?code=cli_verify_" + strings.Repeat("a", 64), "user_code": "AAAAAAAA", "expires_in": 30, "interval": 1})
		case "/api/oauth/cli/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("resource") != serverURL+"/api/external/v1" || r.Form.Get("client_id") != "agentworks-cli" {
				t.Errorf("wrong OAuth audience: %v", r.Form)
			}
			if r.Form.Get("grant_type") == "refresh_token" {
				refreshes.Add(1)
				if r.Form.Get("refresh_token") != "aw_cli_refresh_first" {
					t.Errorf("wrong refresh credential")
				}
				_, _ = w.Write([]byte(`{"access_token":"aw_cli_second","refresh_token":"aw_cli_refresh_second","expires_in":3600}`))
				return
			}
			if polls.Add(1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"aw_cli_first","refresh_token":"aw_cli_refresh_first","expires_in":3600}`))
		case "/api/external/v1/tools":
			if r.Header.Get("Authorization") != "Bearer aw_cli_second" {
				t.Errorf("tools used stale credential: %q", r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"tools":[]}`))
		case "/api/oauth/cli/revoke":
			revokes.Add(1)
			_ = r.ParseForm()
			if r.Form.Get("token") != "aw_cli_refresh_second" {
				t.Errorf("logout used stale refresh credential")
			}
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer s.Close()
	serverURL = s.URL
	path := filepath.Join(t.TempDir(), "config.json")
	var stdout, stderr bytes.Buffer
	env := func(string) string { return "" }
	if code := run(context.Background(), []string{"--config", path, "--server", serverURL, "login", "--no-browser"}, strings.NewReader(""), &stdout, &stderr, env); code != 0 {
		t.Fatalf("login failed: %s", &stderr)
	}
	if polls.Load() != 2 || !strings.Contains(stderr.String(), serverURL+"/oauth/cli") || !strings.Contains(stderr.String(), "AAAAAAAA") || strings.Contains(stdout.String()+stderr.String(), "aw_cli_") {
		t.Fatalf("unexpected login output or polling: polls=%d out=%s err=%s", polls.Load(), &stdout, &stderr)
	}
	cfg, err := agentworksclient.LoadConfig(path)
	if err != nil || cfg.Token != "aw_cli_first" || cfg.RefreshToken != "aw_cli_refresh_first" {
		t.Fatalf("browser login not saved: %#v %v", cfg, err)
	}
	cfg.ExpiresAt = time.Now().Add(-time.Minute)
	if err := agentworksclient.SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"--config", path, "tools", "list"}, strings.NewReader(""), &stdout, &stderr, env); code != 0 {
		t.Fatalf("refresh failed: %s", &stderr)
	}
	if refreshes.Load() != 1 {
		t.Fatalf("refresh count=%d", refreshes.Load())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"--config", path, "logout"}, strings.NewReader(""), &stdout, &stderr, env); code != 0 || revokes.Load() != 1 {
		t.Fatalf("logout code=%d revokes=%d stderr=%s", code, revokes.Load(), &stderr)
	}
	cfg, err = agentworksclient.LoadConfig(path)
	if err != nil || cfg.Token != "" || cfg.RefreshToken != "" {
		t.Fatalf("credentials retained after logout: %#v %v", cfg, err)
	}
}

func TestStructuredConflictExit(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"error":{"code":"revision_conflict","message":"stale revision"}}`))
	}))
	defer s.Close()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--config", filepath.Join(t.TempDir(), "missing"), "--server", s.URL, "--json", "plan", "get", "--workflow", "wf"}, strings.NewReader(""), &stdout, &stderr, func(key string) string {
		if key == "AGENTWORKS_TOKEN" {
			return "jwt"
		}
		return ""
	})
	if code != 4 || stdout.Len() != 0 || !json.Valid(stderr.Bytes()) || !strings.Contains(stderr.String(), "revision_conflict") {
		t.Fatalf("code=%d out=%s err=%s", code, &stdout, &stderr)
	}
}

func TestArgumentPrecisionAndInputOverride(t *testing.T) {
	cmd := newCommand(&options{stdin: strings.NewReader(""), stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, getenv: os.Getenv})
	plan, _, err := cmd.Find([]string{"plan"})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.ParseFlags([]string{"--input", "-", "--workflow", "new", "--set", "count=9007199254740993"}); err != nil {
		t.Fatal(err)
	}
	args, err := operationArguments(plan, strings.NewReader(`{"workflow_id":"old","number":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(args)
	if args["workflow_id"] != "new" || !strings.Contains(string(data), `"count":9007199254740993`) || !strings.Contains(string(data), `"number":9007199254740993`) {
		t.Fatalf("arguments %s", data)
	}
}

// Exercise actual stdio framing from a client, including discovery and a call
// forwarded to the hosted HTTP API. Any non-protocol stdout breaks this test.
func TestMCPStdioRoundtrip(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer jwt" {
			t.Error("missing hosted token")
		}
		if r.URL.Path == "/api/external/v1/tools" {
			_, _ = w.Write([]byte(`{"tools":[{"name":"get_plan","description":"Plan","inputSchema":{"type":"object","properties":{"workflow_id":{"type":"string"}},"required":["workflow_id"]}}]}`))
			return
		}
		var body struct {
			Name      string
			Arguments map[string]any
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Name != "get_plan" || body.Arguments["workflow_id"] != "wf" {
			t.Errorf("call: %#v", body)
		}
		_, _ = w.Write([]byte(`{"revision":"v1","plan":{"steps":[]}}`))
	}))
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	defer serverIn.Close()
	defer clientOut.Close()
	defer clientIn.Close()
	defer serverOut.Close()
	var stderr bytes.Buffer
	done := make(chan int, 1)
	configPath := filepath.Join(t.TempDir(), "missing")
	go func() {
		done <- run(ctx, []string{"--config", configPath, "--server", s.URL, "mcp", "serve"}, serverIn, serverOut, &stderr, func(key string) string {
			if key == "AGENTWORKS_TOKEN" {
				return "jwt"
			}
			return ""
		})
	}()
	client := mcpclient.NewClient(transport.NewIO(clientIn, clientOut, nil))
	defer client.Close()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	request := mcp.InitializeRequest{}
	request.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	request.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "1"}
	initialized, err := client.Initialize(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(initialized.Instructions, "get_agent_context") {
		t.Fatalf("initialize instructions missing guidance entrypoint: %q", initialized.Instructions)
	}
	listed, err := client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil || len(listed.Tools) != 1 || listed.Tools[0].Name != "get_plan" {
		t.Fatalf("list: %#v %v", listed, err)
	}
	result, err := client.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "get_plan", Arguments: map[string]any{"workflow_id": "wf"}}})
	if err != nil || result.IsError || len(result.Content) != 1 {
		t.Fatalf("call: %#v %v", result, err)
	}
	content, ok := result.Content[0].(mcp.TextContent)
	if !ok || !strings.Contains(content.Text, `"revision":"v1"`) {
		t.Fatalf("result content: %#v", result.Content)
	}
	_ = clientOut.Close()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("bridge exited %d: %s", code, &stderr)
		}
	case <-ctx.Done():
		t.Fatal("bridge failed to stop at EOF")
	}
}

func TestCLILoginRejectsPasswordAndSessionJWT(t *testing.T) {
	for _, args := range [][]string{{"login", "--username", "alice", "--password-stdin"}, {"login", "--token-stdin"}} {
		var out, stderr bytes.Buffer
		code := run(context.Background(), append([]string{"--config", filepath.Join(t.TempDir(), "config"), "--server", "http://127.0.0.1:1"}, args...), strings.NewReader("session.jwt.token"), &out, &stderr, func(string) string { return "" })
		if code == 0 {
			t.Fatal("legacy CLI login accepted")
		}
	}
}

func TestVersionCommand(t *testing.T) {
	var out bytes.Buffer
	if code := run(context.Background(), []string{"version"}, strings.NewReader(""), &out, io.Discard, func(string) string { return "" }); code != 0 {
		t.Fatalf("version exited %d", code)
	}
	for _, want := range []string{`"version"`, `"os"`, `"arch"`, runtime.GOOS, runtime.GOARCH} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("version output missing %q: %s", want, out.String())
		}
	}
}

func TestUpdateCommand(t *testing.T) {
	const newSHA = "0123456789abcdef0123456789abcdef01234567"
	fakeBinary := "#!/bin/sh\necho '{\"version\": \"" + newSHA + "\"}'\n"
	sum := sha256.Sum256([]byte(fakeBinary))
	hexSum := hex.EncodeToString(sum[:])
	asset := "agentworks-" + runtime.GOOS + "-" + runtime.GOARCH
	mux := http.NewServeMux()
	mux.HandleFunc("/api/downloads/cli/version.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"version":%q,"release":"test-release"}`, newSHA)
	})
	mux.HandleFunc("/api/downloads/cli/"+asset, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fakeBinary))
	})
	mux.HandleFunc("/api/downloads/cli/"+asset+".sha256", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hexSum, asset)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	runUpdate := func(t *testing.T, target, version string, extra ...string) (int, string) {
		t.Helper()
		oldTarget, oldVersion := updateTargetOverride, cliVersion
		updateTargetOverride, cliVersion = target, version
		defer func() { updateTargetOverride, cliVersion = oldTarget, oldVersion }()
		var out bytes.Buffer
		args := append([]string{"--server", server.URL, "update"}, extra...)
		code := run(context.Background(), args, strings.NewReader(""), &out, io.Discard, func(string) string { return "" })
		return code, out.String()
	}

	t.Run("installs verified binary", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "agentworks")
		if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
			t.Fatal(err)
		}
		code, out := runUpdate(t, target, "old-version-sha")
		if code != 0 {
			t.Fatalf("update exited %d: %s", code, out)
		}
		got, err := os.ReadFile(target)
		if err != nil || string(got) != fakeBinary {
			t.Fatalf("target not replaced: %q %v", got, err)
		}
		if _, err := os.Stat(target + ".bak"); !os.IsNotExist(err) {
			t.Fatal("backup left behind")
		}
		if !strings.Contains(out, newSHA) {
			t.Fatalf("output missing new version: %s", out)
		}
	})

	t.Run("up to date short-circuits", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "agentworks")
		if err := os.WriteFile(target, []byte("current"), 0o755); err != nil {
			t.Fatal(err)
		}
		code, out := runUpdate(t, target, newSHA)
		if code != 0 {
			t.Fatalf("update exited %d: %s", code, out)
		}
		if got, _ := os.ReadFile(target); string(got) != "current" {
			t.Fatal("up-to-date binary was touched")
		}
	})

	t.Run("check only reports", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "agentworks")
		if err := os.WriteFile(target, []byte("current"), 0o755); err != nil {
			t.Fatal(err)
		}
		code, out := runUpdate(t, target, "old-version-sha", "--check")
		if code != 0 {
			t.Fatalf("update --check exited %d: %s", code, out)
		}
		if !strings.Contains(out, `"update_available": true`) {
			t.Fatalf("check output wrong: %s", out)
		}
		if got, _ := os.ReadFile(target); string(got) != "current" {
			t.Fatal("--check touched the binary")
		}
	})

	t.Run("checksum mismatch aborts", func(t *testing.T) {
		badMux := http.NewServeMux()
		badMux.HandleFunc("/api/downloads/cli/version.json", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"version":%q,"release":"test-release"}`, newSHA)
		})
		badMux.HandleFunc("/api/downloads/cli/"+asset, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("tampered-bytes"))
		})
		badMux.HandleFunc("/api/downloads/cli/"+asset+".sha256", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "%s  %s\n", hexSum, asset)
		})
		bad := httptest.NewServer(badMux)
		defer bad.Close()
		target := filepath.Join(t.TempDir(), "agentworks")
		if err := os.WriteFile(target, []byte("current"), 0o755); err != nil {
			t.Fatal(err)
		}
		oldTarget, oldVersion := updateTargetOverride, cliVersion
		updateTargetOverride, cliVersion = target, "old-version-sha"
		defer func() { updateTargetOverride, cliVersion = oldTarget, oldVersion }()
		var out, stderr bytes.Buffer
		code := run(context.Background(), []string{"--server", bad.URL, "update"}, strings.NewReader(""), &out, &stderr, func(string) string { return "" })
		if code == 0 {
			t.Fatal("tampered update accepted")
		}
		if got, _ := os.ReadFile(target); string(got) != "current" {
			t.Fatal("target modified despite checksum failure")
		}
		if _, err := os.Stat(target + ".bak"); !os.IsNotExist(err) {
			t.Fatal("backup left behind after failure")
		}
	})
}

func TestBarePlanAndCallShowUsage(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"plan"}, "plan get"},
		{[]string{"tools", "call"}, "call NAME"},
	} {
		var out bytes.Buffer
		if code := run(context.Background(), tc.args, strings.NewReader(""), &out, io.Discard, func(string) string { return "" }); code != 0 {
			t.Fatalf("%v exited %d", tc.args, code)
		}
		if !strings.Contains(out.String(), "Usage:") || !strings.Contains(out.String(), tc.want) {
			t.Fatalf("%v printed no usage: %s", tc.args, out.String())
		}
	}
}

func TestCLIOperationsStayAdmitted(t *testing.T) {
	admitted := map[string]bool{}
	for _, name := range agentworksproduct.RunExternalTools() {
		admitted[name] = true
	}
	for _, name := range agentworksproduct.RunTools() {
		admitted[name] = true
	}
	mapped := map[string]string{}
	for _, group := range cliOperationGroups {
		for _, op := range group.operations {
			mapped[op.tool] = group.name + " " + op.command
		}
	}
	mapped["get_plan"] = "plan get"
	for tool, command := range mapped {
		if !admitted[tool] {
			t.Fatalf("CLI %q calls %s, which product.yaml no longer admits", command, tool)
		}
	}
}
