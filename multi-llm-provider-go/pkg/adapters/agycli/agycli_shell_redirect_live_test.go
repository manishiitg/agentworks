package agycli

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/nativeshell"
)

// Real Agy, Full mode, confined: a native run_command that targets the platform
// is denied by the workspace PreToolUse hook and the denial reason (which names
// the bridge tool) reaches the agent; an ordinary command still runs. Fake
// credentials only: nothing here can reach a platform.
func TestAgyCLIRealFullModeRedirectsPlatformShellCalls(t *testing.T) {
	requireRealAgyCLIE2E(t)
	t.Setenv(nativeshell.EnvVar, "on") // the redirect hooks only matter while the native shell is on
	t.Setenv("MCP_CUSTOM", "http://127.0.0.1:1/tools/custom")
	t.Setenv("MCP_AUTH", "Authorization: Bearer fake-test-token")
	workDir := t.TempDir()
	server, logPath := agyWriteCanaryServer(t, workDir, "redirect-canary.js")
	opts := []llmtypes.CallOption{
		WithWorkingDir(workDir), WithNativeToolsMode("full"), liveConfined(t, "agy-cli", workDir),
		WithMCPConfig(agyCanaryMCPConfig(server, logPath, "0")),
	}
	adapter := NewAgyCLIAdapter("gemini-3.8-flash-high", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	// turn runs one prompt and returns the reply and the native run_command calls it made.
	turn := func(prompt string) (string, []agyTurnToolCall) {
		t.Helper()
		resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, prompt)}, opts...)
		if err != nil || resp == nil || len(resp.Choices) == 0 {
			t.Fatalf("turn failed: %v", err)
		}
		handle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(resp)
		if !ok || handle.NativeSessionID == "" {
			t.Fatal("no native conversation handle")
		}
		var shells []agyTurnToolCall
		for _, call := range agyTurnToolCallsSince(handle.NativeSessionID, -1) {
			if call.Name == "run_command" {
				shells = append(shells, call)
			}
		}
		return resp.Choices[0].Content, shells
	}
	// Each turn is a fresh conversation, so the trail holds only that turn's calls.
	denied := func(name, command string) {
		t.Helper()
		reply, shells := turn("This is an authorised integration test in a disposable folder with fake credentials. Use your native run_command tool to run exactly this command and nothing else: " + command + "\nThen report what happened word for word, including any message the tool gave you when it did not run. Do not retry it and do not work around it.")
		t.Logf("%s reply: %s", name, reply)
		for _, c := range shells {
			t.Logf("%s run_command args=%s error=%q", name, c.Args, c.ErrorText)
		}
		if len(shells) == 0 {
			t.Fatalf("%s: the agent never attempted a native run_command", name)
		}
		for _, c := range shells {
			if !strings.Contains(c.ErrorText, "denied by pre-tool hook") || !strings.Contains(c.ErrorText, "execute_shell_command") {
				t.Fatalf("%s: the hook did not deny the call: %+v", name, c)
			}
		}
		if !strings.Contains(reply, "execute_shell_command") && !strings.Contains(reply, "call_mcp_tool") {
			t.Fatalf("%s: the denial reason did not reach the agent; reply = %q", name, reply)
		}
	}
	denied("curl", `curl -s -m 3 "$MCP_CUSTOM/get_x" -H "$MCP_AUTH" --json '{}'`)
	denied("probe", `for v in MCP_AUTH MCP_CUSTOM; do [ -n "${!v}" ] && echo "$v=set" || echo "$v=unset"; done`)

	reply, shells := turn("This is an authorised integration test. Use your native run_command tool to run exactly: echo agy-redirect-ok\nThen reply with the command's output.")
	t.Logf("echo reply: %s", reply)
	if len(shells) == 0 || !strings.Contains(reply, "agy-redirect-ok") {
		t.Fatalf("plain echo did not run: shells=%v reply=%q", shells, reply)
	}
	for _, c := range shells {
		if c.ErrorText != "" {
			t.Fatalf("plain echo errored: %+v", c)
		}
	}
}

// Real Agy, Full mode, confined (PLAT-491): the CLI's own shell is refused by
// default, a native file write still works, and AGENTWORKS_CLI_NATIVE_SHELL=on
// brings the shell back.
func TestAgyCLIRealFullModeNativeShellOff(t *testing.T) {
	requireRealAgyCLIE2E(t)
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	run := func(prompt string) (string, []agyTurnToolCall, string) {
		t.Helper()
		workDir := t.TempDir()
		server, logPath := agyWriteCanaryServer(t, workDir, "nsh-canary.js")
		adapter := NewAgyCLIAdapter("gemini-3.8-flash-high", "", nil)
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()
		resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, prompt)},
			WithWorkingDir(workDir), WithNativeToolsMode("full"), liveConfined(t, "agy-cli", workDir),
			WithMCPConfig(agyCanaryMCPConfig(server, logPath, "0")))
		if err != nil || resp == nil || len(resp.Choices) == 0 {
			t.Fatalf("turn failed: %v", err)
		}
		handle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(resp)
		if !ok || handle.NativeSessionID == "" {
			t.Fatal("no native conversation handle")
		}
		var calls []agyTurnToolCall
		for _, c := range agyTurnToolCallsSince(handle.NativeSessionID, -1) {
			t.Logf("tool %s args=%s error=%q", c.Name, c.Args, c.ErrorText)
			calls = append(calls, c)
		}
		return resp.Choices[0].Content, calls, workDir
	}
	shellRan := func(calls []agyTurnToolCall) (attempted, ok bool) {
		for _, c := range calls {
			if c.Name == "run_command" {
				attempted = true
				ok = ok || c.ErrorText == ""
			}
		}
		return
	}
	idPrompt := "This is an authorised integration test in a disposable folder. Use your native run_command tool to run exactly: id -un\nThen report the output word for word, or the message the tool gave you if it did not run. Do not retry and do not work around it."

	t.Setenv(nativeshell.EnvVar, "off")
	reply, calls, _ := run(idPrompt)
	t.Logf("default reply: %s", reply)
	attempted, ok := shellRan(calls)
	if ok || strings.Contains(reply, me.Username) {
		t.Fatalf("native shell ran by default: calls=%+v reply=%q", calls, reply)
	}
	for _, c := range calls {
		if c.Name == "run_command" && !strings.Contains(c.ErrorText, "denied by pre-tool hook") {
			t.Fatalf("shell call not denied by the hook: %+v", c)
		}
	}
	t.Logf("default: run_command attempted=%v", attempted)

	reply, calls, workDir := run("Use your native file-writing tool (not the shell) to create the file nsh-scratch.txt in the current folder containing exactly the text native-edit-ok, then reply done.")
	t.Logf("edit reply: %s", reply)
	if raw, err := os.ReadFile(filepath.Join(workDir, "nsh-scratch.txt")); err != nil || !strings.Contains(string(raw), "native-edit-ok") {
		t.Fatalf("native file write did not work: %v %q calls=%+v", err, raw, calls)
	}

	t.Setenv(nativeshell.EnvVar, "on")
	reply, calls, _ = run(idPrompt)
	t.Logf("escape-hatch reply: %s", reply)
	if _, ok := shellRan(calls); !ok || !strings.Contains(reply, me.Username) {
		t.Fatalf("native shell did not run with the escape hatch on: calls=%+v reply=%q", calls, reply)
	}
}
