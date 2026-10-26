package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	cursorcliadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
)

// A workflow step reported STATUS: FAILED with "Ask mode blocks browser
// automation and all required writes under $STEP_OUTPUT_DIR; switch to Agent
// mode and re-execute", and the run was abandoned on that basis.
//
// The mode claim is false on its face: `cursor-agent --mode` accepts only the
// read-only "plan" and "ask", omitting it IS agent mode, and nothing in this
// codebase passes it. But "the model made it up" and "the bridge never attached,
// so the agent really could not act" produce the same visible outcome — a step
// that burns a minute and writes nothing — and blaming the wrong one sends you
// to the wrong fix.
//
// These two cases separate them. Same model, same deny-builtin hooks, same
// absence of --force and --mode; the only difference is whether the api-bridge
// MCP server is reachable.
//
//   - bridge healthy → agent mode must complete the write.
//   - bridge unreachable → the run must fail, and must say the bridge is
//     missing rather than blaming a mode it is not in.
//
// Neither case may produce the ask-mode claim. An earlier version of this test
// omitted the bridge config entirely and so only ever exercised the second case,
// which made a broken environment look like a misbehaving model.
//
// Gated on RUN_CURSOR_CLI_REAL_E2E=1: real tokens on a real account.
func TestCursorAgentModeWithAndWithoutBridge(t *testing.T) {
	requireCursorAskModeE2E(t)

	model := strings.TrimSpace(os.Getenv("CURSOR_CLI_REAL_E2E_MODEL"))
	if model == "" {
		// The failing step used grok-4.5, since retired from Cursor's catalog
		// (superseded by the cursor-grok-4.6-* generation) — grok-4.6 is the
		// current equivalent default; the test isn't about which grok model,
		// just holding "same model" constant across the two sub-cases.
		model = "grok-4.6"
	}

	t.Run("bridge healthy writes the file", func(t *testing.T) {
		workDir := t.TempDir()
		proof := filepath.Join(workDir, "proof.txt")
		bridgeURL, token := startProofWritingBridge(t, workDir)

		said := runCursorProbe(t, model, workDir, mcpBridgeConfigJSON(t, bridgeURL, token, true))

		assertNoAskModeClaim(t, model, said)
		if _, err := os.Stat(proof); err != nil {
			t.Fatalf("bridge was reachable and its execute_shell_command tool was advertised, "+
				"but no file was written (%v).\nResponse:\n%s", err, said)
		}
	})

	// The transport production actually uses. Everything above runs the tmux
	// path, where WithDenyBuiltinTools installs a .cursor/hooks.json and the
	// agent keeps full agent-mode capability. Structured mode has no hook
	// mechanism before a one-shot --print launch, so the adapter substitutes
	// cursor's own containment and resolves deny-builtins to "--mode ask"
	// (cursorcli_structured_adapter.go: `if mode == "" { ... mode = "ask" }`).
	//
	// That makes the step read-only. Every workflow step that writes to
	// $STEP_OUTPUT_DIR or drives a browser is launched exactly this way, so the
	// containment costs the step the capability it exists to use — and the
	// resulting "Ask mode blocks browser automation" report is accurate, not a
	// model excusing itself.
	//
	// This asserts the behavior we want: the same bridge, the same denial of
	// built-ins, and a completed write. It fails until structured mode is
	// contained by something other than a read-only mode.
	t.Run("native ACP must not degrade to ask mode", func(t *testing.T) {
		workDir := t.TempDir()
		proof := filepath.Join(workDir, "proof.txt")
		bridgeURL, token := startProofWritingBridge(t, workDir)

		said := runCursorProbe(t, model, workDir, mcpBridgeConfigJSON(t, bridgeURL, token, true))

		assertNoAskModeClaim(t, model, said)
		if _, err := os.Stat(proof); err != nil {
			t.Fatalf("structured transport wrote nothing (%v). deny-builtins resolved to "+
				"--mode ask, which is read-only, so the bridge's write tool could not be "+
				"used.\nResponse:\n%s", err, said)
		}
	})

	t.Run("bridge unreachable reports the bridge not the mode", func(t *testing.T) {
		workDir := t.TempDir()
		// A config pointing at a port nothing is listening on: the bridge process
		// starts, fails to reach its API, and advertises no tools.
		said := runCursorProbe(t, model, workDir, mcpBridgeConfigJSON(t, "http://127.0.0.1:1", "dead-token", false))

		assertNoAskModeClaim(t, model, said)
		if _, err := os.Stat(filepath.Join(workDir, "proof.txt")); err == nil {
			t.Fatal("a file was written with no working bridge and built-ins denied — " +
				"the deny hook is not holding")
		}
		// It must name the real constraint. Anything else and the operator is
		// handed a wrong lead, which is how this investigation started.
		if !mentionsBridgeOrDenial(said) {
			t.Fatalf("run failed without naming the bridge or the built-in denial, "+
				"so the transcript gives no usable cause.\nResponse:\n%s", said)
		}
	})
}

func requireCursorAskModeE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("RUN_CURSOR_CLI_REAL_E2E") == "" {
		t.Skip("set RUN_CURSOR_CLI_REAL_E2E=1 to run this real cursor-cli e2e")
	}
	if _, err := exec.LookPath("cursor-agent"); err != nil {
		t.Skipf("cursor-agent binary not found: %v", err)
	}
	if _, err := cursorBridgeBinaryPath(); err != nil {
		t.Skipf("%v (run `go install ./cmd/mcpbridge` in mcpagent)", err)
	}
}

func cursorBridgeBinaryPath() (string, error) {
	if p, err := exec.LookPath("mcpbridge"); err == nil {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err == nil {
		candidate := filepath.Join(home, "go", "bin", "mcpbridge")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("mcpbridge binary not found")
}

// startProofWritingBridge serves one tool, execute_shell_command, matching what
// workflow steps actually use.
//
// The name matters. Without --force, cursor honors the operator's approvalMode
// and per-tool allowlist in ~/.cursor/cli-config.json, so a tool absent from it
// comes back "Tool rejected: User rejected MCP: ...". execute_shell_command is
// on that list precisely because production depends on it, which makes this the
// faithful choice as well as the working one.
func startProofWritingBridge(t *testing.T, shellDir string) (url, token string) {
	t.Helper()
	token = fmt.Sprintf("cursor-ask-mode-e2e-%d", time.Now().UnixNano())

	runShell := func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Command string `json:"command"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		command := strings.TrimSpace(body.Command)
		if command == "" {
			http.Error(w, `{"error":"command required"}`, http.StatusBadRequest)
			return
		}
		// Same contract as the real tool: run it, hand back stdout/stderr/exit.
		cmd := exec.Command("sh", "-c", command)
		cmd.Dir = shellDir
		out, runErr := cmd.CombinedOutput()
		exitCode := 0
		if runErr != nil {
			exitCode = 1
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": runErr == nil,
			"data": map[string]interface{}{
				"stdout":    string(out),
				"stderr":    "",
				"exit_code": exitCode,
			},
		})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// The bridge addresses tools by several shapes depending on how it was
	// launched; accept any path ending in the tool name rather than guessing one.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusOK)
			return
		}
		if auth := r.Header.Get("Authorization"); token != "" && auth != "" &&
			!strings.Contains(auth, token) {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/execute_shell_command") {
			runShell(w, r)
			return
		}
		http.Error(w, `{"error":"unknown tool"}`, http.StatusNotFound)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL, token
}

// mcpBridgeConfigJSON builds the same .cursor/mcp.json shape production builds:
// an "api-bridge" server run from the mcpbridge binary, with its tool list and
// API endpoint supplied through the environment.
func mcpBridgeConfigJSON(t *testing.T, apiURL, token string, advertiseTool bool) string {
	t.Helper()
	bridgePath, err := cursorBridgeBinaryPath()
	if err != nil {
		t.Fatalf("bridge binary: %v", err)
	}

	tools := []map[string]interface{}{}
	if advertiseTool {
		// Field names and "type" follow mcpbridge's ToolDef exactly: it reads
		// `input_schema` (snake_case) and routes a "custom" tool to
		// {apiURL}/tools/custom/{name}. An earlier version used `inputSchema`
		// and omitted the type, and cursor reported "Schema lists no
		// parameters" then called the tool with no arguments.
		schema, marshalErr := json.Marshal(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command": map[string]interface{}{"type": "string", "description": "shell command to run"},
			},
			"required": []string{"command"},
		})
		if marshalErr != nil {
			t.Fatalf("marshal input schema: %v", marshalErr)
		}
		tools = append(tools, map[string]interface{}{
			"name":         "execute_shell_command",
			"description":  "Run a shell command. The ONLY way to touch the filesystem in this session.",
			"input_schema": json.RawMessage(schema),
			"type":         "custom",
		})
	}
	toolsJSON, err := json.Marshal(tools)
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}

	config := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"api-bridge": map[string]interface{}{
				"command": bridgePath,
				"args":    []string{},
				"env": map[string]string{
					"MCP_API_URL":    apiURL,
					"MCP_API_TOKEN":  token,
					"MCP_TOOLS_FILE": writeMCPToolsFileForTest(t, toolsJSON),
					"MCP_BRIDGE_LOG": filepath.Join(t.TempDir(), "mcpbridge.log"),
				},
				"trust": true,
			},
		},
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal mcp config: %v", err)
	}
	return string(configJSON)
}

// runCursorProbe runs one turn in the production configuration and returns
// everything the model said.
func runCursorProbe(t *testing.T, model, workDir, mcpConfig string, extra ...llmtypes.CallOption) string {
	t.Helper()

	streamChan := make(chan llmtypes.StreamChunk, 512)
	var streamed strings.Builder
	streamDone := make(chan struct{})
	go func() {
		for chunk := range streamChan {
			streamed.WriteString(chunk.Content)
		}
		close(streamDone)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	adapter := cursorcliadapter.NewCursorCLIAdapter("", model, &e2eMockLogger{})
	messages := []llmtypes.MessageContent{{
		Role: llmtypes.ChatMessageTypeHuman,
		Parts: []llmtypes.ContentPart{llmtypes.TextContent{
			Text: "Write the exact text CURSOR_AGENT_MODE_WROTE_THIS to the file " +
				filepath.Join(workDir, "proof.txt") + ", then reply DONE. " +
				"If something prevents the write, quote the exact error verbatim and name the tool that failed.",
		}},
	}}
	opts := []llmtypes.CallOption{
		cursorcliadapter.WithInteractiveSessionID(fmt.Sprintf("cursor-ask-mode-%d", time.Now().UnixNano())),
		cursorcliadapter.WithPersistentInteractiveSession(true),
		cursorcliadapter.WithWorkingDir(workDir),
		// Production configuration: bridge wired in and auto-approved, and
		// built-ins denied. No WithForce and no explicit WithMode — the callers
		// choose the transport, which is what decides how "deny built-ins" is
		// actually enforced.
		cursorcliadapter.WithMCPConfig(mcpConfig),
		cursorcliadapter.WithApproveMCPs(),
		cursorcliadapter.WithDenyBuiltinTools(true),
		llmtypes.WithStreamingChan(streamChan),
	}
	opts = append(opts, extra...)

	resp, callErr := adapter.GenerateContent(ctx, messages, opts...)
	<-streamDone

	if err := cursorcliadapter.CleanupCursorCLIInteractiveSessions(context.Background()); err != nil {
		t.Logf("cursor session cleanup: %v", err)
	}

	said := streamed.String()
	if resp != nil && len(resp.Choices) > 0 {
		said += "\n" + resp.Choices[0].Content
	}
	if callErr != nil {
		// A transport error is still evidence; the assertions read what was said.
		t.Logf("GenerateContent returned error (transcript still checked): %v", callErr)
	}
	return said
}

// assertNoAskModeClaim fails if the run reports being read-only. Matched on the
// distinctive phrasing rather than the bare word "ask", which occurs in ordinary
// prose.
//
// The report is accurate when it appears — nothing here is accusing the model of
// inventing it. Under structured transport the adapter passes --mode ask on the
// caller's behalf, so the step really is read-only and says so. What this
// assertion pins is that a step given a write tool must end up able to use it.
func assertNoAskModeClaim(t *testing.T, model, said string) {
	t.Helper()
	for _, claim := range []string{
		"Ask mode blocks",
		"switch to Agent mode",
		"Switch to Agent mode",
		"in Ask mode",
		"Ask mode is active",
	} {
		if strings.Contains(said, claim) {
			t.Fatalf("model %q reported %q. The step was launched with a write tool it is "+
				"expected to use; a read-only mode makes that impossible. Under structured "+
				"transport deny-builtins resolves to --mode ask "+
				"(cursorcli_structured_adapter.go), which is the likely source.\nResponse:\n%s",
				model, claim, said)
		}
	}
}

func mentionsBridgeOrDenial(said string) bool {
	for _, anchor := range []string{
		"api-bridge", "api_bridge", "bridge",
		"Built-in filesystem/shell/edit/search/delegation tools are disabled",
		"disabled in this session",
		"no tools", "No api-bridge",
	} {
		if strings.Contains(said, anchor) {
			return true
		}
	}
	return false
}
