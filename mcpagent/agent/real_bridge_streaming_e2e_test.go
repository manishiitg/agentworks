package mcpagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/agent/codeexec/shellfixture"
	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/internal/agentreview"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/agycli"
)

// cleanChunk is one Source!=terminal content chunk plus the delta marker that
// decides how it must be re-joined.
type cleanChunk struct {
	text    string
	isDelta bool
}

// reassembleCleanStream rebuilds the message a UI would actually display from
// the clean chunk run — the ONLY form in which formatting can be judged.
//
// The join is per-chunk, not per-provider, because the marker is per-chunk:
//   - delta chunks are fragments of one continuous message (pi splits mid-word:
//     "workspace_" + "advanced` server.") and MUST be concatenated verbatim.
//   - block chunks are each a complete message and are separated by a newline.
//
// Joining everything with "\n" — which this test used to do — silently corrupts
// every delta provider: pi's markdown table came out with newlines injected
// mid-row ("|-------" + "\n" + "|"), yet still satisfied the old
// Contains("|") && Contains(buildID) assertions. That is precisely the
// "deterministic asserts pass on visibly-degraded output" trap agentreview
// exists to close, so the reassembled form is what we now assert AND record.
// malformedTableLines returns every markdown-table line in s that is structurally
// broken: a line that opens a table row with "|" but does not close it with "|".
//
// This is the provider-agnostic signature of newline-injection between delta
// fragments. Anchoring on a specific row instead does NOT work: whether a given
// row survives depends on where the fragment boundaries happen to land, so a
// Contains("| build_id | ... |") check passes on visibly garbled output whenever
// the split misses that one row (it did — pi's break landed on the separator row,
// "|-------|-------" + "\n" + "|"). Checking the invariant instead of a sample
// catches the break wherever it lands, and stays robust to cell spacing.
func malformedTableLines(s string) []string {
	var bad []string
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimRight(line, " \t")
		if !strings.HasPrefix(strings.TrimSpace(trimmed), "|") {
			continue
		}
		if !strings.HasSuffix(trimmed, "|") || strings.Count(trimmed, "|") < 2 {
			bad = append(bad, line)
		}
	}
	return bad
}

func reassembleCleanStream(chunks []cleanChunk) string {
	var b strings.Builder
	for _, c := range chunks {
		if b.Len() > 0 && !c.isDelta {
			b.WriteString("\n")
		}
		b.WriteString(c.text)
	}
	return b.String()
}

func realBridgeRandHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// isBridgeOrWebsearchTool reports whether a streamed tool name is ALLOWED under
// the bridge-only policy: an mcpbridge tool (execute_shell_command,
// diff_patch_workspace_file, agent_browser, get_api_spec, read_skill — directly or via
// claude's mcp__api-bridge__ prefix), a provider's MCP-access meta-tool (how
// cursor/pi reach the bridge), or websearch — the ONE built-in tool we permit.
// Anything else is a NATIVE tool (codex exec/shell, claude Bash/Read/Write,
// cursor Shell/Edit, ...) that ran OUTSIDE the bridge — no executor, no
// session-scoping, no controlled tool set — which the policy forbids.
func isBridgeOrWebsearchTool(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, b := range []string{"execute_shell_command", "diff_patch_workspace_file", "agent_browser", "get_api_spec", "read_skill"} {
		if strings.Contains(n, b) {
			return true
		}
	}
	switch n {
	case "getmcptools", "callmcptool", "listmcptools", "listmcpresources", "readmcpresource", "mcp",
		"calldynamictool", "getdynamictools":
		// cursor's MCP meta-tools and pi's generic "mcp" bridge label. Current
		// cursor-agent builds surface a bridge/MCP tool invocation as the
		// dynamic-tool pair GetDynamicTools (discovery) + CallDynamicTool
		// (dispatch) rather than the older get/callmcptool names — these are the
		// bridge-routing mechanism, NOT native tools (proven by the injected
		// bridge handler recording calls>=2 whenever they appear).
		return true
	}
	if strings.Contains(n, "web_search") || strings.Contains(n, "websearch") {
		return true
	}
	return false
}

// callInvokesBridge reports whether a tool call reaches the bridge through its
// BODY rather than its name.
//
// codex's code-mode `exec` is a real example: the tool name is "exec", but the
// script it runs is
//
//	await tools.mcp__api_bridge__execute_shell_command({command: "..."})
//
// so the work genuinely goes through the bridge. Judging that call by its outer
// name alone reports a bridge BYPASS that never happened (it did — this test
// failed with "no bridge tool was used" on a turn whose every action was
// bridge-routed), and would equally miss a real native call hiding behind a
// familiar name. The body is the only place the truth is visible.
func callInvokesBridge(args string) bool {
	a := strings.ToLower(args)
	return strings.Contains(a, "mcp__api_bridge__") || strings.Contains(a, "mcp__api-bridge__")
}

// assertBridgeOrWebsearchOnly fails if any streamed tool is a native tool — the
// strict bridge-only-plus-websearch policy. Empty toolNames is fine (no tools used).
func assertBridgeOrWebsearchOnly(t *testing.T, toolNames []string, tl toolLifecycle) {
	t.Helper()
	var native []string
	for _, id := range tl.order {
		tn := tl.starts[id]
		if !tl.denied[id] && !isBridgeOrWebsearchTool(tn) && !callInvokesBridge(tl.args[id]) {
			native = append(native, tn)
		}
	}
	if len(native) > 0 {
		t.Fatalf("BRIDGE-ONLY POLICY VIOLATED: native (non-bridge, non-websearch) tools ran, bypassing the bridge: %v (all tools: %v)", native, toolNames)
	}
}

// isNativeWriteTool reports whether a native tool name implies a WRITE/mutation
// (as opposed to a read/exec). Used for codex's no-native-writes guarantee.
func isNativeWriteTool(name string) bool {
	n := strings.ToLower(name)
	for _, w := range []string{"write", "edit", "apply_patch", "patch", "create_file", "create-file", "replace"} {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

// assertNoNativeWrites is the codex-specific policy check (see the P0 note): codex
// cannot drop its core functions.exec tool, so it runs read-only. This asserts the
// remaining guarantee — that WRITES are bridge-routed: a bridge tool was actually
// used, and no native WRITE/edit/patch tool appears. Native read-only exec is a
// documented, tolerated exception (harmless under the read-only sandbox).
func assertNoNativeWrites(t *testing.T, toolNames []string, tl toolLifecycle) {
	t.Helper()
	usedBridge := false
	var nativeWrites []string
	for _, id := range tl.order {
		tn := tl.starts[id]
		if isBridgeOrWebsearchTool(tn) || callInvokesBridge(tl.args[id]) {
			usedBridge = true
			continue
		}
		if isNativeWriteTool(tn) {
			nativeWrites = append(nativeWrites, tn)
		}
	}
	if !usedBridge {
		t.Fatalf("no bridge tool was used — the file write did not go through the bridge; tools=%v", toolNames)
	}
	if len(nativeWrites) > 0 {
		t.Fatalf("NATIVE WRITE tools used (mutations must be bridge-routed): %v (all tools: %v)", nativeWrites, toolNames)
	}
}

// startRealExecutorServer boots the executor HTTP API the mcpbridge posts tool
// calls to and registers cleanup. t-based convenience wrapper around bootRealExecutor.
func startRealExecutorServer(t *testing.T, configPath string) (string, string) {
	t.Helper()
	url, token, stop, err := bootRealExecutor(configPath)
	if err != nil {
		t.Fatalf("executor boot: %v", err)
	}
	t.Cleanup(stop)
	return url, token
}

// bootRealExecutor is the t-less core (usable from concurrency goroutines): it
// boots the executor HTTP API — the SAME wiring examples/basic_claude_code uses —
// on 127.0.0.1:0 and returns its URL, token, and a stop func. It does NOT set any
// global env, so multiple executors can run in parallel; each Agent gets its
// URL/token via withAPIConfig.
func bootRealExecutor(configPath string) (string, string, func(), error) {
	apiToken := executor.GenerateAPIToken()
	handlers := executor.NewExecutorHandlers(configPath, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/mcp/execute", handlers.HandleMCPExecute)
	mux.HandleFunc("/api/custom/execute", handlers.HandleCustomExecute)
	mux.HandleFunc("/api/virtual/execute", handlers.HandleVirtualExecute)
	mux.HandleFunc("/tools/mcp/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path[len("/tools/mcp/"):]
		slash := strings.IndexByte(path, '/')
		if slash <= 0 || slash >= len(path)-1 {
			http.Error(w, "invalid tool path", http.StatusBadRequest)
			return
		}
		handlers.HandlePerToolMCPRequest(w, r, path[:slash], path[slash+1:])
	})
	mux.HandleFunc("/tools/custom/", func(w http.ResponseWriter, r *http.Request) {
		tool := r.URL.Path[len("/tools/custom/"):]
		if tool == "" {
			http.Error(w, "missing custom tool name", http.StatusBadRequest)
			return
		}
		handlers.HandlePerToolCustomRequest(w, r, tool)
	})
	mux.HandleFunc("/tools/virtual/", func(w http.ResponseWriter, r *http.Request) {
		tool := r.URL.Path[len("/tools/virtual/"):]
		if tool == "" {
			http.Error(w, "missing virtual tool name", http.StatusBadRequest)
			return
		}
		handlers.HandlePerToolVirtualRequest(w, r, tool)
	})

	// Production bridge metadata returns session-prefixed routes. Keep the
	// test executor reachable through those same routes, not just unscoped URLs.
	mux.HandleFunc("/s/", func(w http.ResponseWriter, r *http.Request) {
		remainder := strings.TrimPrefix(r.URL.Path, "/s/")
		_, toolPath, ok := strings.Cut(remainder, "/")
		if !ok || !strings.HasPrefix(toolPath, "tools/") {
			http.NotFound(w, r)
			return
		}
		scoped := r.Clone(r.Context())
		scopedURL := *r.URL
		scopedURL.Path = "/" + toolPath
		scoped.URL = &scopedURL
		mux.ServeHTTP(w, scoped)
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", nil, err
	}
	server := &http.Server{Handler: executor.AuthMiddleware(apiToken)(mux)} //nolint:gosec // test server, no timeouts needed
	go func() { _ = server.Serve(listener) }()
	stop := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}
	apiBaseURL := "http://" + listener.Addr().String()
	time.Sleep(300 * time.Millisecond)
	return apiBaseURL, apiToken, stop, nil
}

// ensureRealBridgeBinary builds cmd/mcpbridge from source into a temp path so the
// test drives the ACTUAL production bridge binary (with its readiness marker +
// HTTP forwarding), not a stand-in.
func ensureRealBridgeBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "mcpbridge")
	//nolint:gosec // G204: constant build command, temp output path.
	out, err := exec.Command("go", "build", "-o", bin, "../cmd/mcpbridge").CombinedOutput()
	if err != nil {
		t.Fatalf("build mcpbridge: %v\n%s", err, out)
	}
	return bin
}

// realBridgeProviderCase is one coding-agent provider exercised through the REAL
// bridge. apiKeyEnvs, if set, names the env vars to source the provider key from
// (pi); CLI-native-auth providers (claude/codex/cursor) leave it empty.
type realBridgeProviderCase struct {
	name       string
	provider   llm.Provider
	modelID    string
	cliBin     string
	apiKeyEnvs []string
	makeKeys   func(key string) *llm.ProviderAPIKeys
	// strictBridgeOnly enforces the BRIDGE-ONLY tool policy: EVERY tool the model
	// uses must be an mcpbridge tool or websearch, with NO native shell/exec/edit/
	// read tools that bypass the executor (no session-scoping, no controlled tool
	// set, arbitrary host access). True for providers whose native tools can be
	// disabled (claude via bridge-only tools, cursor via deny-builtins, pi via
	// bridge-only-tools). FALSE for codex — see the P0 policy note below.
	strictBridgeOnly bool
}

// ---- BRIDGE-ONLY TOOL POLICY (P0) ----
//
// Coding agents must route ALL tool use through the mcpbridge (→ executor → the
// controlled, session-scoped tool set) plus at most the websearch built-in.
// Native tools (a CLI's own shell/exec/edit/read) bypass that control and are
// forbidden. TestRealBridgeStreamingE2E enforces this per provider:
//
//	claude / cursor / pi : STRICT — assertBridgeOrWebsearchOnly fails on ANY
//	                       native tool. Their native tools are fully disabled
//	                       (claude bridge-only tools, cursor deny-builtins, pi
//	                       bridge-only-tools), so they use only mcp__api-bridge__*
//	                       / CallMcpTool / mcp.
//
//	codex                : DOCUMENTED EXCEPTION. Codex ALWAYS advertises a core
//	                       `functions.exec` tool that CANNOT be removed by any
//	                       flag or config — verified that it survives
//	                       --disable unified_exec/shell_tool/multi_agent/
//	                       code_mode_*, read-only sandbox, and -c tools.exec=false.
//	                       So codex cannot be strictly tool-only-through-the-bridge.
//	                       mcpagent's DEFAULT (appendCodexCLIIntegrationOptions) is
//	                       WORKSPACE-WRITE — native writes allowed, matching how
//	                       codex ran for most of this project's life, and correct
//	                       for the common case where blocking codex's native
//	                       writes stops nothing real (the bridge already grants
//	                       shell access, or the caller is interactive/single-owner
//	                       and bridge-only containment buys no real safety). This
//	                       test's codex case explicitly opts INTO "read-only" (see
//	                       the withCodexSandbox call above) specifically to keep
//	                       the narrower containment guarantee under live test
//	                       coverage: under read-only, native exec can read but
//	                       CANNOT write or mutate the host — every state change is
//	                       forced through the bridge (which runs in the executor,
//	                       not codex's sandbox). The codex case therefore asserts
//	                       the weaker but safety-relevant guarantee: NO NATIVE
//	                       WRITES — a real file was written (report.md on disk)
//	                       which, under the read-only sandbox, only the bridge
//	                       tool could have done. Read-only is for a caller that
//	                       deliberately restricts its tool set (e.g. "web_search
//	                       only, no shell on the bridge" — read-only is the only
//	                       thing that makes that restriction hold for codex) or
//	                       needs an audit trail native exec would bypass — see
//	                       Agent.CodexSandboxMode / withCodexSandbox /
//	                       withCodexNetworkAccess and
//	                       TestAppendCodexCLIIntegrationOptionsSandbox
//	                       ReadOnlyOptIn.
func realBridgeProviderCases() []realBridgeProviderCase {
	return []realBridgeProviderCase{
		{name: "claude", provider: llm.ProviderClaudeCode, modelID: "claude-haiku-4-5", cliBin: "claude", strictBridgeOnly: true},
		// codex: strictBridgeOnly=false — functions.exec is unremovable; read-only
		// sandbox makes it read-only so writes are bridge-routed (see policy above).
		{name: "codex", provider: llm.ProviderCodexCLI, modelID: "gpt-5.6-luna", cliBin: "codex", strictBridgeOnly: false},
		// cursor reaches the bridge via its GetMcpTools/CallMcpTool meta-tools; the
		// mcpagent cursor integration auto-approves the MCP bridge (WithCursorApproveMCPs).
		{name: "cursor", provider: llm.ProviderCursorCLI, modelID: "composer-2.5", cliBin: "cursor-agent", strictBridgeOnly: true},
		// pi streams structured chunks natively via its injected marker hook and
		// needs a Gemini/Pi key.
		{
			name: "pi", provider: llm.ProviderPiCLI, modelID: "google/gemini-3.8-flash", cliBin: "pi",
			apiKeyEnvs:       []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "PI_API_KEY"},
			makeKeys:         func(k string) *llm.ProviderAPIKeys { return &llm.ProviderAPIKeys{PiCLI: &k} },
			strictBridgeOnly: true,
		},
		// muse: strictBridgeOnly=false — native session controls can bypass the hook,
		// with best-effort coverage in TestMuseCLIRealBestEffortToolRestrictions.
		{name: "muse", provider: llm.ProviderMuseCLI, modelID: "muse-spark-1.3-contributor", cliBin: "muse", strictBridgeOnly: false},
		// agy: strictBridgeOnly=false — a mounted turn approves natives
		// alongside the bridge (the only tool switch is
		// --dangerously-skip-permissions). The tmux legs run the TUI
		// sidecar (session id + persistent flag, like the multi-turn
		// table); the structured leg is skipped (the exec lane is owned
		// by the Layer-2 json rows, and exec streams no tool events).
		{name: "agy", provider: llm.ProviderAgyCLI, modelID: "gemini-3.8-flash-high", cliBin: "agy", strictBridgeOnly: false},
	}
}

// TestRealBridgeStreamingE2E is the production-fidelity streaming test the
// stand-in-MCP-server tests were missing: a real coding-agent turn whose tools go
// through the REAL mcpbridge → executor HTTP API → a REAL mcpagent tool
// (execute_shell_command running an actual shell), with structured streaming
// captured at the mcpagent layer (events.StreamingChunkEvent). It proves the
// whole production path streams: bridge tool-call chunks reach the app AND the
// real shell tool actually ran — per provider.
//
// Gated by RUN_MCPAGENT_REAL_BRIDGE_E2E=1 (optional MCPAGENT_REAL_BRIDGE_ONLY=<name>);
// requires the provider's authenticated CLI, tmux, and go (to build the bridge).
// No node / stand-in server.
func TestRealBridgeStreamingE2E(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run the real-bridge streaming e2e")
	}
	only := os.Getenv("MCPAGENT_REAL_BRIDGE_ONLY")
	// MCPAGENT_REAL_BRIDGE_TRANSPORT pins one transport ("tmux" / "structured")
	// when iterating; unset runs the full matrix. Both halves use the SAME real
	// bridge and real shell tool — only the CLI protocol differs.
	onlyTransport := os.Getenv("MCPAGENT_REAL_BRIDGE_TRANSPORT")
	bridgeBin := ensureRealBridgeBinary(t)
	for _, pc := range realBridgeProviderCases() {
		if only != "" && only != pc.name {
			continue
		}
		for _, tr := range realBridgeTransports(pc.provider) {
			if onlyTransport != "" && onlyTransport != tr.name {
				continue
			}
			if pc.provider == llm.ProviderAgyCLI && tr.name == "structured" {
				// Agy's exec lane is owned by the Layer-2 json rows (which
				// assert its single-chunk shape); this matrix leg demands
				// streamed tool events exec never emits.
				continue
			}
			t.Run(pc.name+"/"+tr.name, func(t *testing.T) { runRealBridgeStreaming(t, pc, bridgeBin, tr) })
		}
	}
}

// realBridgeTestAgent is a live agent wired to the REAL mcpbridge -> executor ->
// execute_shell_command path, ready for a task. Shared setup for every
// TestRealBridge* e2e in this file — factored out once a second test
// (markdown fidelity) needed the identical ~60 lines of boilerplate.
// realBridgeTransport is the transport dimension of the matrix. Both run through
// the SAME real mcpbridge -> executor -> real execute_shell_command path; only
// the CLI's own protocol differs. Covering both is the point: a product pinning
// one transport gets completely different streaming and tool-lifecycle behaviour
// from the other, and until now only tmux was ever exercised here.
type realBridgeTransport struct {
	name string
	opts []agentOption
}

// The measured results of this matrix — which providers stream, which surface
// reasoning, and the evidence behind pinning cursor to structured — are recorded
// in the coding-agent-loop repo at
// docs/design/product_api_transport_for_coding_agents.md, section
// "Measured matrix". Update that table when these numbers change materially;
// it is the durable proof the product transport decision rests on.
func realBridgeTransports(provider llm.Provider) []realBridgeTransport {
	if provider == llm.ProviderCursorCLI {
		return []realBridgeTransport{{name: "acp"}}
	}
	if provider == llm.ProviderPiCLI {
		return []realBridgeTransport{{name: "rpc"}}
	}
	if provider == llm.ProviderCodexCLI {
		return []realBridgeTransport{{name: "app-server"}}
	}
	return []realBridgeTransport{
		{name: "tmux"},
		{name: "structured", opts: []agentOption{withCodingAgentTransport(llm.CodingAgentTransportStructured)}},
	}
}

func newRealBridgeTestAgent(t *testing.T, pc realBridgeProviderCase, bridgeBin string, extraOpts ...agentOption) (agent *Agent, ctx context.Context, apiURL, apiToken, workDir string) {
	t.Helper()
	if _, err := exec.LookPath(pc.cliBin); err != nil {
		t.Skipf("authenticated %q CLI required", pc.cliBin)
	}

	t.Setenv("MCP_BRIDGE_BINARY", bridgeBin)

	configPath := filepath.Join(t.TempDir(), "mcp_servers.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	apiURL, apiToken = startRealExecutorServer(t, configPath)

	cfg := llm.Config{Provider: pc.provider, ModelID: pc.modelID}
	if len(pc.apiKeyEnvs) > 0 {
		for _, envPath := range []string{"../.env", "../../multi-llm-provider-go/.env"} {
			_ = godotenv.Load(envPath)
		}
		var key string
		for _, e := range pc.apiKeyEnvs {
			if v := strings.TrimSpace(os.Getenv(e)); v != "" {
				key = v
				break
			}
		}
		if key == "" {
			t.Skipf("one of %v required for %s", pc.apiKeyEnvs, pc.name)
		}
		if pc.makeKeys != nil {
			cfg.APIKeys = pc.makeKeys(key)
		}
	}
	llmModel, err := llm.InitializeLLM(cfg)
	if err != nil {
		t.Fatalf("InitializeLLM: %v", err)
	}

	workDir = t.TempDir()
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)

	agentOpts := []agentOption{
		withProvider(pc.provider),
		withAPIConfig(apiURL, apiToken),
		withStreaming(true),
		withCodingAgentWorkingDir(workDir),
	}
	if pc.provider == llm.ProviderCodexCLI {
		// The P0 guarantee this test enforces for codex (assertNoNativeWrites,
		// below) only holds under read-only — mcpagent's DEFAULT is
		// workspace-write (see Agent.CodexSandboxMode doc), so opt in explicitly
		// to keep this containment actually tested rather than silently
		// untested once the default changed.
		agentOpts = append(agentOpts, withCodexSandbox("read-only"))
	}
	if pc.provider == llm.ProviderAgyCLI {
		// Agy's tmux legs run the TUI sidecar: without a session id + the
		// persistent flag this builder's agent would silently run exec.
		sid := "rb-" + realBridgeRandHex(4)
		agentOpts = append(agentOpts, withSessionID(sid), withAgyPersistentInteractiveSession(true))
		untrust, terr := trustAgyWorkdirForTmuxRow(workDir)
		if terr != nil {
			t.Fatal(terr)
		}
		keyed, keyErr := agyKeyModeForTmuxRow(untrust)
		if keyErr != nil {
			untrust()
			t.Fatal(keyErr)
		}
		untrust = keyed
		t.Cleanup(func() {
			agycli.CloseAgyCLIInteractiveSessionForOwner(sid, "test done")
			untrust()
		})
	}
	agentOpts = append(agentOpts, extraOpts...)
	agent, err = newAgent(ctx, llmModel, configPath, agentOpts...)
	if err != nil {
		t.Fatalf("newAgent: %v", err)
	}
	t.Cleanup(func() { _ = agent.Close() })

	// Register the REAL shell tool the bridge will expose and route to.
	shellEnv := append(BuildSafeEnvironment(), "MCP_API_URL="+apiURL, "MCP_API_TOKEN="+apiToken)
	if err := agent.registerCustomTool(
		"execute_shell_command",
		codeexec.ShellCommandDescription,
		codeexec.ShellCommandParams,
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			return shellfixture.ExecuteShellCommand(ctx, args, shellEnv)
		},
		"workspace_advanced",
	); err != nil {
		t.Fatalf("RegisterCustomTool: %v", err)
	}
	return agent, ctx, apiURL, apiToken, workDir
}

func runRealBridgeStreaming(t *testing.T, pc realBridgeProviderCase, bridgeBin string, tr realBridgeTransport) {
	agent, ctx, _, _, workDir := newRealBridgeTestAgent(t, pc, bridgeBin, tr.opts...)

	listener := &recordingAgentEventListener{}
	agent.addEventListener(listener)

	// A rich, real multi-step task: READ a real file (a project build-id that is
	// only in the file, not the prompt — anti-cheat, benign framing so safety-tuned
	// models don't refuse), WRITE a real file (a markdown table), then read it back.
	// Absolute paths so they're independent of the shell tool's cwd.
	codeWord := "BUILD_ID_" + realBridgeRandHex(6)
	buildIDPath := filepath.Join(workDir, "build_id.txt")
	reportPath := filepath.Join(workDir, "report.md")
	if err := os.WriteFile(buildIDPath, []byte(codeWord), 0o600); err != nil {
		t.Fatal(err)
	}
	task := fmt.Sprintf(
		"You are a build assistant with one tool: execute_shell_command, which runs a shell command and returns its output. "+
			"Do these steps in order, writing one short sentence of narration BEFORE each command:\n"+
			"1. Run: cat %[1]s   — this prints the project build id.\n"+
			"2. Using a shell command, write a GitHub-flavored markdown report table to the file %[2]s with EXACTLY this "+
			"structure, substituting <BUILD_ID> with the build id from step 1:\n"+
			"| Field | Value |\n|-------|-------|\n| build_id | <BUILD_ID> |\n| status | ok |\n"+
			"3. Run: cat %[2]s\n"+
			"Finally, reply with the exact contents of %[2]s (the markdown table).",
		buildIDPath, reportPath)

	// turnStart anchors the perceived-latency numbers below. The question this
	// test now also answers is not just "did content stream" but "would a user
	// watching this see something happening, soon and steadily" — a provider can
	// stream perfectly clean text and still feel dead if the first chunk lands
	// 40s in, or if there is one silent 30s gap in the middle.
	turnStart := time.Now()
	answer, err := agent.ask(ctx, task)
	if err != nil {
		t.Fatalf("agent.Ask: %v", err)
	}

	// Collect the structured stream the mcpagent layer emitted. Content arrives as
	// StreamingChunkEvent; tool calls arrive as ToolCallStartEvent (a distinct
	// event type at this layer) — both must appear for a streamed tool turn.
	// StreamingChunkEvent.Source now separates raw terminal frames from clean
	// content, so a no-terminal UI selects Source != "terminal" (no heuristics).
	var contentChunks, cleanContentChunks, deltaContentChunks, toolChunks, thinkingChunks int
	var cleanTexts, toolNames, thinkingTexts []string
	var cleanRun []cleanChunk
	// Perceived-latency capture. signalAt collects, in arrival order, the offset
	// of every event a UI could actually SHOW the user (clean content, tool call,
	// thinking) — the union matters more than any single stream, because a
	// provider that never streams text can still feel alive on tool activity
	// alone. Reasoning is tracked separately because it is the one signal that is
	// provider-discretionary: claude emits it, codex encrypts it, pi emits none.
	var cleanAt []time.Time
	var signalAt []time.Duration
	// Milliseconds, not time.Duration: a Duration marshals to NANOseconds, so
	// recording it under a *_ms key wrote 6556365459 where the reader expects
	// 6556 — the artifact silently lied about its own units.
	firstAt := map[string]int64{}
	mark := func(kind string, ts time.Time) {
		if ts.IsZero() {
			return
		}
		off := ts.Sub(turnStart)
		if off < 0 {
			return
		}
		signalAt = append(signalAt, off)
		if _, seen := firstAt[kind]; !seen {
			firstAt[kind] = off.Milliseconds()
		}
	}
	for _, ev := range listener.events {
		switch d := ev.Data.(type) {
		case *events.StreamingChunkEvent:
			if d.IsToolCall || d.Content == "" || (!d.IsDelta && strings.TrimSpace(d.Content) == "") {
				continue
			}
			contentChunks++
			if d.Source != events.StreamingChunkSourceTerminal {
				cleanContentChunks++
				cleanTexts = append(cleanTexts, d.Content)
				cleanRun = append(cleanRun, cleanChunk{text: d.Content, isDelta: d.IsDelta})
				if d.IsDelta {
					deltaContentChunks++
				}
				if !ev.Timestamp.IsZero() {
					cleanAt = append(cleanAt, ev.Timestamp)
				}
				mark("clean_content", ev.Timestamp)
			}
		case *events.ToolCallStartEvent:
			toolChunks++
			toolNames = append(toolNames, d.ToolName)
			mark("tool_call", ev.Timestamp)
		case *events.ConversationThinkingEvent:
			if strings.TrimSpace(d.Thinking) == "" {
				continue
			}
			thinkingChunks++
			thinkingTexts = append(thinkingTexts, d.Thinking)
			mark("thinking", ev.Timestamp)
		}
	}
	var cleanSpreadMS int64 = -1
	if len(cleanAt) > 1 {
		cleanSpreadMS = cleanAt[len(cleanAt)-1].Sub(cleanAt[0]).Milliseconds()
	} else if len(cleanAt) == 1 {
		cleanSpreadMS = 0
	}
	sort.Slice(signalAt, func(i, j int) bool { return signalAt[i] < signalAt[j] })
	ms := func(d time.Duration) int64 { return d.Milliseconds() }
	var firstSignalMS, longestSilenceMS int64 = -1, -1
	if len(signalAt) > 0 {
		firstSignalMS = ms(signalAt[0])
		// Longest silence includes the opening wait (turnStart -> first signal),
		// which is exactly the stall a user feels most.
		longestSilenceMS = ms(signalAt[0])
		for i := 1; i < len(signalAt); i++ {
			if gap := ms(signalAt[i] - signalAt[i-1]); gap > longestSilenceMS {
				longestSilenceMS = gap
			}
		}
	}
	t.Logf("real-bridge stream: %d content chunk(s) (%d clean transcript, %d delta, rest terminal), %d tool-call event(s) %v, %d thinking event(s); answer=%q",
		contentChunks, cleanContentChunks, deltaContentChunks, toolChunks, toolNames, thinkingChunks, strings.TrimSpace(answer))
	t.Logf("perceived latency: first signal %dms, longest silence %dms, %d total signal(s); first-by-kind %v",
		firstSignalMS, longestSilenceMS, len(signalAt), firstAt)

	// The clean view must be free of raw terminal frames (ANSI escapes) now that
	// Source separates them — proves the fix on real output, not a heuristic.
	for _, c := range cleanTexts {
		if strings.Contains(c, "\x1b") {
			t.Fatalf("a Source!=terminal chunk still contained raw terminal ANSI: %q", c)
		}
	}

	// Real READ through the bridge: the build id (never in the prompt) proves the
	// tool genuinely ran the `cat build_id.txt` step.
	if !strings.Contains(answer, codeWord) {
		t.Fatalf("answer %q does not contain the file build id %q — the real shell tool did not run through the bridge", answer, codeWord)
	}
	// Real WRITE through the bridge: the model actually created report.md on disk.
	//nolint:gosec // G304: reportPath is a test-controlled temp path (t.TempDir()).
	report, readErr := os.ReadFile(reportPath)
	if readErr != nil {
		t.Fatalf("report.md was not written by the real shell tool through the bridge: %v", readErr)
	}
	reportStr := string(report)
	// The written file is a real markdown table carrying the build id it just read.
	if !strings.Contains(reportStr, codeWord) || !strings.Contains(reportStr, "|") ||
		!strings.Contains(reportStr, "build_id") || !strings.Contains(reportStr, "status") {
		t.Fatalf("report.md is not the expected markdown table with the build id: %q", reportStr)
	}
	// Streaming through the real bridge: the tool call streamed as its own event...
	// Tool calls must COMPLETE, not merely start: an unpaired start is what
	// leaves a product's tool chip spinning forever. Counting starts (the old
	// `toolChunks == 0` check) cannot see that.
	toolLife := collectToolLifecycle(listener.events)
	// Log the pairing per call: when the lifecycle assertion fails, the useful
	// information is WHICH tools ended and which did not (bridge vs native), and
	// a bare failure message hides that.
	for _, id := range toolLife.order {
		endName, ended := toolLife.ends[id]
		t.Logf("tool lifecycle: name=%s id=%s ended=%t end_name=%s", toolLife.starts[id], id, ended, endName)
	}
	tokenUsage := collectTurnTokenUsage(listener.events)
	// ...and CLEAN transcript content (no raw terminal frames) reached the app,
	// INCLUDING the rich markdown table the model produced — i.e. a no-terminal UI
	// receives the renderable table, not just plain lines.
	if cleanContentChunks == 0 {
		t.Fatalf("no clean transcript content streamed (%d content chunks were all raw terminal frames)", contentChunks)
	}
	// Bridge-only tool policy (see the P0 policy note above realBridgeProviderCases).
	if pc.strictBridgeOnly {
		// claude/cursor/pi: NO native tools at all.
		assertBridgeOrWebsearchOnly(t, toolNames, toolLife)
	} else {
		// codex: functions.exec is unremovable, so it runs read-only. Assert the
		// weaker guarantee — NO NATIVE WRITES: a bridge tool was used and report.md
		// on disk (asserted above) could only have been written by the bridge under
		// the read-only sandbox; no native write/edit/patch tool appears.
		assertNoNativeWrites(t, toolNames, toolLife)
	}
	// Reassemble the way a real UI must, then assert on THAT — not on a lossy
	// "\n"-join of the fragments.
	// Deferred until AFTER the bridge-only / no-native-writes policy checks
	// above: containment is the stronger guarantee, and a tool-lifecycle failure
	// must never mask a policy violation by aborting the test first.
	assertToolLifecycleComplete(t, toolLife)
	if contract, ok := llm.GetCodingAgentProviderContract(pc.provider, pc.modelID); ok && contract.SurfacesTokenUsage {
		assertTurnTokenUsage(t, tokenUsage)
	}

	cleanJoined := reassembleCleanStream(cleanRun)
	if !strings.Contains(cleanJoined, "|") || !strings.Contains(cleanJoined, codeWord) {
		t.Fatalf("the markdown table (pipes + build id) did not stream as clean content; clean stream:\n%s", cleanJoined)
	}
	// The real formatting check: every table row in the message a user would SEE
	// must be structurally intact. Contains("|")+Contains(buildID) passes happily
	// on a table whose rows were split across lines, which is exactly how a
	// garbled stream survived review before.
	if bad := malformedTableLines(cleanJoined); len(bad) > 0 {
		t.Fatalf("the reassembled clean stream contains %d broken markdown table row(s) %q — the streamed table is garbled "+
			"(delta fragments re-joined wrongly, or the provider split content it did not mark as deltas).\nreassembled:\n%s",
			len(bad), bad, cleanJoined)
	}

	// SECOND TURN on the SAME warm session. The first turn's time-to-first-signal
	// includes one-off setup (CLI process spawn, MCP bridge handshake, session
	// bootstrap); a second turn on the same session pays none of that. Comparing
	// the two is the only way to tell a warm-up cost from a per-turn cost, and
	// the two call for opposite product fixes: a warm-up cost is hidden by
	// keeping the session alive, a per-turn cost needs a progress affordance on
	// every send.
	turn2From := len(listener.events)
	turn2Start := time.Now()
	turn2Answer, turn2Err := agent.ask(ctx, "In one short sentence, what build id did you find? Do not use any tools.")
	turn2FirstSignalMS := int64(-1)
	if turn2Err == nil {
		if d, ok := firstSignalAfter(listener.events, turn2From, turn2Start); ok {
			turn2FirstSignalMS = d.Milliseconds()
		}
	}
	turn2TotalMS := time.Since(turn2Start).Milliseconds()
	t.Logf("turn 2 (warm session): first signal %dms, total %dms, err=%v, answer=%q",
		turn2FirstSignalMS, turn2TotalMS, turn2Err, strings.TrimSpace(turn2Answer))

	// The tmux record keeps its historical name so previously approved reviews
	// stay valid; the structured half is a new artifact.
	recordName := "TestRealBridgeStreaming_" + pc.name
	if tr.name != "tmux" {
		recordName += "_" + tr.name
	}
	if pc.provider == llm.ProviderCodexCLI {
		recordName = "TestRealBridgeStreamingE2E_Codex"
	}
	if pc.provider == llm.ProviderPiCLI {
		recordName = "TestRealBridgeStreamingE2E_Pi"
	}
	rec := agentreview.Write(t, recordName,
		pc.name+" over "+tr.name+" via the REAL mcpbridge → executor → real execute_shell_command: read a build-id file, write a markdown table, read it back — streamed at the mcpagent layer",
		map[string]any{
			"transport":          tr.name,
			"tool_calls_started": len(toolLife.starts),
			"tool_calls_ended":   len(toolLife.ends),
			// The call BODIES, not just names. A reviewer cannot otherwise tell a
			// bridge-routed call from raw native shell use: codex's code-mode tool
			// is always named "exec", and only its body reveals whether the work
			// went through tools.mcp__api_bridge__*. Recording names alone made
			// that question unanswerable from the artifact.
			"tool_calls":               toolCallRecords(toolLife),
			"token_usage":              tokenUsage,
			"clean_transcript_content": cleanTexts,
			// The reassembled message is what a human actually reads, so it is what
			// the "proper formatting / no garbled or merged text" criterion must be
			// judged against. The raw chunk array above is kept for provenance, but
			// judging formatting from fragments is how a garbled table passed review.
			"reassembled_message":  cleanJoined,
			"clean_content_count":  cleanContentChunks,
			"delta_content_count":  deltaContentChunks,
			"total_content_chunks": contentChunks,
			"tool_call_events":     toolChunks,
			"tool_names":           toolNames,
			"thinking_event_count": thinkingChunks,
			"thinking_texts":       thinkingTexts,
			"first_signal_ms":      firstSignalMS,
			"longest_silence_ms":   longestSilenceMS,
			// Turn 2 runs on the same warm session, so comparing it against
			// first_signal_ms separates one-off warm-up from a per-turn cost.
			"turn2_first_signal_ms": turn2FirstSignalMS,
			"turn2_total_ms":        turn2TotalMS,
			"turn2_answer":          strings.TrimSpace(turn2Answer),
			// spread_ms is what separates real streaming from a late single
			// delivery: chunks that all land within a few ms did not stream,
			// however many of them there were.
			"clean_content_spread_ms":  cleanSpreadMS,
			"delivered_in_one_block":   cleanContentChunks <= 1,
			"total_signal_events":      len(signalAt),
			"first_signal_ms_by_kind":  firstAt,
			"answer":                   strings.TrimSpace(answer),
			"report_md_on_disk":        reportStr,
			"build_id_only_via_tool":   codeWord,
			"went_through_real_bridge": true,
		},
		// Shape must stay token-INdependent, so the timings above are deliberately
		// NOT in the fingerprint — they vary every run and would make every stored
		// review permanently stale. Whether the provider emits reasoning at all IS
		// stable per-provider behavior, so it belongs here: if a CLI release starts
		// (or stops) surfacing thinking, the fingerprint changes and the review is
		// correctly forced to be redone.
		map[string]any{
			"transport":              tr.name,
			"streamed_clean_content": cleanContentChunks > 0,
			"streamed_tool":          toolChunks > 0,
			"streamed_table":         strings.Contains(cleanJoined, "|"),
			"streamed_thinking":      thinkingChunks > 0,
			"tools_all_completed":    len(toolLife.starts) > 0 && len(toolLife.ends) >= len(toolLife.starts),
		},
	)
	agentreview.RequireReviewed(t, rec)
}

// TestRealBridgeMarkdownFidelity is the P0 companion to
// TestRealBridgeStreamingE2E, purpose-built to answer "can we properly extract
// complex markdown, including nested code fences, with no duplication" — the
// existing table test only exercised a GFM table with presence-only
// (strings.Contains) assertions, which cannot fail even if the extracted text
// were duplicated or the table came back mangled.
//
// This test tightens that in two ways: (1) the file the model writes is
// asserted BYTE-EXACT against a known template (not just "contains a pipe"),
// and (2) both the on-disk file and the streamed clean transcript assert
// Count(...)==1 on structural markdown markers (the nested fence's opening
// ```bash, the table's build_id row) — the generalized fix for the
// presence-only-assertion class of bug this project already hit once today
// (the cursor wrapped-prompt leak, whose test asserted a token was PRESENT but
// never that a 706-char leak was ABSENT).
//
// Gated by the same RUN_MCPAGENT_REAL_BRIDGE_E2E=1 / MCPAGENT_REAL_BRIDGE_ONLY
// convention as TestRealBridgeStreamingE2E; runs across all CLI providers.
func TestRealBridgeMarkdownFidelity(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run the real-bridge markdown fidelity e2e")
	}
	only := os.Getenv("MCPAGENT_REAL_BRIDGE_ONLY")
	bridgeBin := ensureRealBridgeBinary(t)
	for _, pc := range realBridgeProviderCases() {
		if only != "" && only != pc.name {
			continue
		}
		t.Run(pc.name, func(t *testing.T) { runRealBridgeMarkdownFidelity(t, pc, bridgeBin) })
	}
}

// realBridgeMarkdownTemplate is the exact content the model must write, with
// <BUILD_ID> as a placeholder it substitutes in both places (anti-cheat: the
// real id only exists in a file it must read first). Deliberately contains a
// nested code fence (a 4-backtick fence wrapping a 3-backtick one) — the
// real-world "documenting a code block that itself contains a code block"
// case that a naive markdown-fence stripper/extractor mishandles.
func realBridgeMarkdownTemplate() string {
	fence4 := strings.Repeat("`", 4)
	fence3 := strings.Repeat("`", 3)
	return strings.Join([]string{
		"# Report",
		"",
		"| Field | Value |",
		"|-------|-------|",
		"| build_id | <BUILD_ID> |",
		"| status | ok |",
		"",
		"Nested fence example:",
		"",
		fence4 + "text",
		"Outer fence wraps an inner one:",
		fence3 + "bash",
		`echo "nested-<BUILD_ID>"`,
		fence3,
		fence4,
	}, "\n")
}

func runRealBridgeMarkdownFidelity(t *testing.T, pc realBridgeProviderCase, bridgeBin string) {
	agent, ctx, _, _, workDir := newRealBridgeTestAgent(t, pc, bridgeBin)

	listener := &recordingAgentEventListener{}
	agent.addEventListener(listener)

	codeWord := "BUILD_ID_" + realBridgeRandHex(6)
	buildIDPath := filepath.Join(workDir, "build_id.txt")
	reportPath := filepath.Join(workDir, "report.md")
	if err := os.WriteFile(buildIDPath, []byte(codeWord), 0o600); err != nil {
		t.Fatal(err)
	}

	template := realBridgeMarkdownTemplate()
	task := fmt.Sprintf(
		"You are a build assistant with one tool: execute_shell_command, which runs a shell command and returns its output. "+
			"Do these steps in order, writing one short sentence of narration BEFORE each command:\n"+
			"1. Run: cat %[1]s   — this prints the project build id.\n"+
			"2. Write EXACTLY the following markdown content to the file %[2]s, substituting <BUILD_ID> in BOTH places with "+
			"the id from step 1. Use a quoted shell heredoc (cat > %[2]s <<'MDEOF' ... MDEOF) so the backticks and quotes "+
			"below are written literally, with no reformatting, extra blank lines, or extra text added:\n\n%[3]s\n\n"+
			"3. Run: cat %[2]s\n"+
			"Finally, reply with exactly the contents of %[2]s, and nothing else — no preamble, no explanation.",
		buildIDPath, reportPath, template)

	answer, err := agent.ask(ctx, task)
	if err != nil {
		t.Fatalf("agent.Ask: %v", err)
	}

	var cleanTexts, toolNames []string
	var cleanRun []cleanChunk
	var toolChunks int
	for _, ev := range listener.events {
		switch d := ev.Data.(type) {
		case *events.StreamingChunkEvent:
			if d.IsToolCall || d.Content == "" || (!d.IsDelta && strings.TrimSpace(d.Content) == "") {
				continue
			}
			if d.Source != events.StreamingChunkSourceTerminal {
				cleanTexts = append(cleanTexts, d.Content)
				cleanRun = append(cleanRun, cleanChunk{text: d.Content, isDelta: d.IsDelta})
			}
		case *events.ToolCallStartEvent:
			toolChunks++
			toolNames = append(toolNames, d.ToolName)
		}
	}
	cleanJoined := reassembleCleanStream(cleanRun)
	t.Logf("markdown fidelity: %d tool-call event(s) %v; answer=%q", toolChunks, toolNames, strings.TrimSpace(answer))

	// Real WRITE through the bridge.
	//nolint:gosec // G304: reportPath is a test-controlled temp path (t.TempDir()).
	report, readErr := os.ReadFile(reportPath)
	if readErr != nil {
		t.Fatalf("report.md was not written by the real shell tool through the bridge: %v", readErr)
	}
	reportStr := string(report)

	// EXACT match, not presence: the heredoc mechanism guarantees byte-fidelity
	// once the model substitutes <BUILD_ID> correctly, so any deviation here is
	// a real extraction/formatting problem, not test flakiness.
	expected := strings.ReplaceAll(template, "<BUILD_ID>", codeWord)
	if strings.TrimSpace(reportStr) != strings.TrimSpace(expected) {
		t.Fatalf("report.md is not byte-exact.\n--- want ---\n%s\n--- got ---\n%s", expected, reportStr)
	}

	// Duplication guard on disk: the build id is designed to appear EXACTLY
	// twice (table row + nested echo) and the nested fence opens EXACTLY once.
	// A duplication bug (the original motivation for this test) shows up here
	// as a count > expected, not just "the token is present somewhere".
	if n := strings.Count(reportStr, codeWord); n != 2 {
		t.Fatalf("report.md build id appears %d times, want exactly 2 (table row + nested echo) — possible duplication: %q", n, reportStr)
	}
	if n := strings.Count(reportStr, "```bash"); n != 1 {
		t.Fatalf("report.md nested fence opens %d times, want exactly 1: %q", n, reportStr)
	}

	if toolChunks == 0 {
		t.Fatalf("no ToolCallStartEvent — the real bridge tool call did not stream to the mcpagent layer")
	}
	if len(cleanTexts) == 0 {
		t.Fatalf("no clean transcript content streamed")
	}
	toolLife := collectToolLifecycle(listener.events)
	if pc.strictBridgeOnly {
		assertBridgeOrWebsearchOnly(t, toolNames, toolLife)
	} else {
		assertNoNativeWrites(t, toolNames, toolLife)
	}

	// Same duplication guard, applied to the STREAMED transcript rather than the
	// file on disk — this is the artifact a no-terminal UI actually renders, and
	// the one the user's original report ("lots of duplicate text in streaming
	// extraction") was about. Structural markers (not the build id, which
	// narration sentences may legitimately repeat) give a stable signal.
	if n := strings.Count(cleanJoined, "```bash"); n != 1 {
		t.Fatalf("STREAMED transcript: nested fence opening appears %d times, want exactly 1 (duplication in streaming extraction): %q", n, cleanJoined)
	}
	if n := strings.Count(cleanJoined, "| build_id |"); n != 1 {
		t.Fatalf("STREAMED transcript: table row appears %d times, want exactly 1 (duplication in streaming extraction): %q", n, cleanJoined)
	}
	// Also require the ANSWER — what the app actually shows as the final
	// reply — is free of duplication, independent of the mid-turn stream.
	if n := strings.Count(answer, "```bash"); n > 1 {
		t.Fatalf("final answer: nested fence opening appears %d times, want at most 1 (duplication in final extraction): %q", n, answer)
	}

	rec := agentreview.Write(t, "TestRealBridgeMarkdownFidelity_"+strings.ToUpper(pc.name[:1])+pc.name[1:],
		pc.name+" via the REAL mcpbridge → executor → real execute_shell_command: write+read back a markdown table AND a nested code fence, byte-exact, no duplication, streamed at the mcpagent layer",
		agyReviewFacts(pc.provider, map[string]any{
			"clean_transcript_content": cleanTexts,
			"tool_names":               toolNames,
			"answer":                   strings.TrimSpace(answer),
			"report_md_on_disk":        reportStr,
			"expected_template":        template,
			"build_id_only_via_tool":   codeWord,
		}),
		map[string]any{
			"byte_exact_on_disk":       strings.TrimSpace(reportStr) == strings.TrimSpace(expected),
			"no_duplication_on_disk":   strings.Count(reportStr, codeWord) == 2,
			"no_duplication_in_stream": strings.Count(cleanJoined, "```bash") == 1,
			"nested_fence_preserved":   strings.Contains(reportStr, "```bash") && strings.Contains(reportStr, strings.Repeat("`", 4)),
		},
	)
	agentreview.RequireReviewed(t, rec)
}

// --- shared P0 assertions ----------------------------------------------------

// toolLifecycle is the start/end pairing of every tool call in a turn.
type toolLifecycle struct {
	starts map[string]string // toolCallID -> name
	args   map[string]string // toolCallID -> call body (arguments / code-mode script)
	ends   map[string]string // toolCallID -> name
	denied map[string]bool   // exact native permission refusal, not a successful execution
	order  []string
}

func collectToolLifecycle(evs []*events.AgentEvent) toolLifecycle {
	tl := toolLifecycle{starts: map[string]string{}, args: map[string]string{}, ends: map[string]string{}, denied: map[string]bool{}}
	for _, ev := range evs {
		switch d := ev.Data.(type) {
		case *events.ToolCallStartEvent:
			if _, seen := tl.starts[d.ToolCallID]; !seen {
				tl.order = append(tl.order, d.ToolCallID)
			}
			tl.starts[d.ToolCallID] = d.ToolName
			tl.args[d.ToolCallID] = d.ToolParams.Arguments
		case *events.ToolCallEndEvent:
			tl.ends[d.ToolCallID] = d.ToolName
		case *events.ToolCallErrorEvent:
			tl.ends[d.ToolCallID] = d.ToolName
			tl.denied[d.ToolCallID] = strings.TrimSpace(d.Error) == "Error: Permission denied"
		}
	}
	return tl
}

// assertToolLifecycleComplete fails when any tool call started without a
// matching end.
//
// This generalizes requireCodexStructuredToolIdentity, which asserted exactly
// this but ONLY for codex AND only on the structured transport. The gap was not
// theoretical: codex over tmux emits ToolCallStart with no ToolCallEnd, so the
// product's tool chip spins forever, and every existing assertion passed anyway
// because the suite only ever checked `toolChunks > 0` — the count of STARTS.
// An unfinished tool call is indistinguishable from a completed one unless the
// pairing itself is asserted.
func toolLifecycleError(tl toolLifecycle) error {
	if len(tl.starts) == 0 {
		return fmt.Errorf("no ToolCallStartEvent — the real bridge tool call did not stream to the mcpagent layer")
	}
	var unfinished []string
	for _, id := range tl.order {
		startName := tl.starts[id]
		if strings.TrimSpace(startName) == "" {
			return fmt.Errorf("ToolCallStart %q has an empty name; the app would render a generic \"tool\"", id)
		}
		endName, ok := tl.ends[id]
		if !ok {
			unfinished = append(unfinished, startName+"("+id+")")
			continue
		}
		if endName != "" && endName != startName {
			return fmt.Errorf("tool %q changed name between start and end: start=%q end=%q", id, startName, endName)
		}
	}
	if len(unfinished) > 0 {
		return fmt.Errorf("%d tool call(s) started but never ended: %s — a UI keyed on ToolCallID leaves these spinning forever",
			len(unfinished), strings.Join(unfinished, ", "))
	}
	return nil
}

func assertToolLifecycleComplete(t *testing.T, tl toolLifecycle) {
	t.Helper()
	if err := toolLifecycleError(tl); err != nil {
		t.Fatal(err)
	}
}

// turnTokenUsage is the usage a product surface displays under the answer.
type turnTokenUsage struct {
	Prompt     int `json:"prompt_tokens"`
	Completion int `json:"completion_tokens"`
	Total      int `json:"total_tokens"`
	Cache      int `json:"cache_tokens"`
	Reasoning  int `json:"reasoning_tokens"`
	Events     int `json:"token_usage_events"`
}

func collectTurnTokenUsage(evs []*events.AgentEvent) turnTokenUsage {
	var u turnTokenUsage
	for _, ev := range evs {
		d, ok := ev.Data.(*events.TokenUsageEvent)
		if !ok {
			continue
		}
		u.Events++
		// Keep the largest report rather than summing: providers emit both
		// per-call and conversation-total usage, and summing double-counts.
		if d.TotalTokens > u.Total {
			u.Prompt, u.Completion, u.Total = d.PromptTokens, d.CompletionTokens, d.TotalTokens
			u.Reasoning = d.ReasoningTokens
			if gi := d.GenerationInfo; gi != nil {
				for _, k := range []string{"cache_tokens", "cache_read_input_tokens", "cached_content_tokens"} {
					if v, ok := gi[k].(float64); ok && int(v) > u.Cache {
						u.Cache = int(v)
					}
					if v, ok := gi[k].(int); ok && v > u.Cache {
						u.Cache = v
					}
				}
			}
		}
	}
	return u
}

// assertTurnTokenUsage pins the usage a product surface renders under the
// answer. Input/output must be real numbers: a turn that reports zero prompt or
// completion tokens shows "Input 0 Output 0" in the product, which reads as a
// broken turn even when the answer is correct. Cache tokens are recorded but not
// required — only some providers report them, and a cold turn legitimately has
// none.
func turnTokenUsageError(u turnTokenUsage) error {
	if u.Events == 0 {
		return fmt.Errorf("no TokenUsageEvent — the product surface would render no usage line for this turn")
	}
	if u.Prompt <= 0 {
		return fmt.Errorf("prompt/input tokens = %d, want > 0 (usage line would show Input 0)", u.Prompt)
	}
	if u.Completion <= 0 {
		return fmt.Errorf("completion/output tokens = %d, want > 0 (usage line would show Output 0)", u.Completion)
	}
	if u.Total < u.Prompt+u.Completion {
		return fmt.Errorf("total tokens %d < prompt %d + completion %d — the usage line does not add up",
			u.Total, u.Prompt, u.Completion)
	}
	return nil
}

func assertTurnTokenUsage(t *testing.T, u turnTokenUsage) {
	t.Helper()
	if err := turnTokenUsageError(u); err != nil {
		t.Fatal(err)
	}
}

// toolCallRecords renders each tool call as name + whether it ended + a bounded
// slice of its body, so the agentic reviewer can verify routing and lifecycle
// from the artifact alone.
func toolCallRecords(tl toolLifecycle) []map[string]any {
	out := make([]map[string]any, 0, len(tl.order))
	for _, id := range tl.order {
		body := tl.args[id]
		const maxBody = 1200
		truncated := false
		if len(body) > maxBody {
			body, truncated = body[:maxBody], true
		}
		_, ended := tl.ends[id]
		out = append(out, map[string]any{
			"name":  tl.starts[id],
			"ended": ended,
			// Name OR body: claude reaches the bridge through the tool NAME
			// (mcp__api-bridge__*), codex through the BODY of its code-mode exec.
			// A body-only flag reads as "bypassed the bridge" for every claude
			// call, which is exactly backwards.
			"bridge_routed":  isBridgeOrWebsearchTool(tl.starts[id]) || callInvokesBridge(tl.args[id]),
			"bridge_via":     bridgeRoutingSource(tl.starts[id], tl.args[id]),
			"body":           body,
			"body_truncated": truncated,
		})
	}
	return out
}

// bridgeRoutingSource names WHERE a call's bridge routing is visible, so the
// recorded artifact cannot be misread: "name" for providers that call the bridge
// tool directly (claude), "body" for codex's code-mode exec whose script invokes
// tools.mcp__api_bridge__*, and "none" when neither shows bridge routing.
func bridgeRoutingSource(name, args string) string {
	switch {
	case isBridgeOrWebsearchTool(name):
		return "name"
	case callInvokesBridge(args):
		return "body"
	default:
		return "none"
	}
}

// firstSignalAfter returns how long after `since` the FIRST user-visible signal
// (clean content, tool call, or thinking) appeared among events[from:].
//
// Used to answer a question the single-turn measurement cannot: whether the
// multi-second wait before anything appears is one-off warm-up (process spawn,
// MCP handshake, session bootstrap) or a cost paid on every turn. Those have
// opposite product remedies — the first is hidden by a warm session, the second
// needs a progress affordance on every send.
func firstSignalAfter(evs []*events.AgentEvent, from int, since time.Time) (time.Duration, bool) {
	best := time.Duration(-1)
	for i := from; i < len(evs); i++ {
		ev := evs[i]
		if ev == nil || ev.Timestamp.IsZero() || ev.Timestamp.Before(since) {
			continue
		}
		visible := false
		switch d := ev.Data.(type) {
		case *events.StreamingChunkEvent:
			visible = !d.IsToolCall && strings.TrimSpace(d.Content) != "" && d.Source != events.StreamingChunkSourceTerminal
		case *events.ToolCallStartEvent:
			visible = true
		case *events.ConversationThinkingEvent:
			visible = strings.TrimSpace(d.Thinking) != ""
		}
		if !visible {
			continue
		}
		if off := ev.Timestamp.Sub(since); best < 0 || off < best {
			best = off
		}
	}
	if best < 0 {
		return 0, false
	}
	return best, true
}
