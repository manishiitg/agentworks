package musecli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

var codingCLIStress = flag.Bool("coding-cli-stress", false, "run the opt-in live hybrid-mode stress scenario")

// museHybridStressAllowlist mirrors mcpagent's hybrid Muse allowlist.
var museHybridStressAllowlist = []string{"web_search", "read_skill", "read_file", "search",
	"subagent_spawn", "subagent_wait", "subagent_send_message", "subagent_read_result", "subagent_input", "subagent_cancel"}

func requireMuseStress(t *testing.T) int {
	t.Helper()
	requireMetaMuseCLIE2E(t)
	if !*codingCLIStress {
		t.Skip("opt-in: pass -coding-cli-stress")
	}
	if n, err := strconv.Atoi(os.Getenv("CODING_CLI_STRESS_ITERATIONS")); err == nil && n > 0 {
		return n
	}
	return 2
}

// museSlowMCPStub serves slow_echo(text, delay_ms) -> "SLOW_ECHO:<text>"
// after the delay, in the same stateless Streamable-HTTP shape as
// museProbeMCPStub.
func museSlowMCPStub() *httptest.Server {
	write := func(w http.ResponseWriter, code int, obj any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		if obj != nil {
			body, _ := json.Marshal(obj)
			_, _ = w.Write(body)
		}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			write(w, 400, map[string]any{"error": "bad json"})
			return
		}
		switch msg.Method {
		case "initialize":
			write(w, 200, map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "mlp-slow-stub", "version": "0.1"}}})
		case "notifications/initialized":
			write(w, 202, nil)
		case "tools/list":
			write(w, 200, map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"tools": []any{map[string]any{
				"name":        "slow_echo",
				"description": "Echo text with a SLOW_ECHO prefix after delay_ms milliseconds.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}, "delay_ms": map[string]any{"type": "number"}}, "required": []any{"text"}},
			}}}})
		case "tools/call":
			args, _ := msg.Params["arguments"].(map[string]any)
			text, _ := args["text"].(string)
			if d, ok := args["delay_ms"].(float64); ok && d > 0 && d <= 60000 {
				time.Sleep(time.Duration(d) * time.Millisecond)
			}
			write(w, 200, map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "SLOW_ECHO:" + text}}}})
		default:
			write(w, 200, map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{}})
		}
	}))
}

// TestMuseCLIStressHybridParallelSubagents: in hybrid mode, three parallel
// background subagents, todos, a slow MCP call and a mid-turn steer must
// yield one final answer that exists only after every child reported; a
// follow-up turn on the retained session must stay consistent.
func TestMuseCLIStressHybridParallelSubagents(t *testing.T) {
	iterations := requireMuseStress(t)
	for i := 1; i <= iterations; i++ {
		t.Run(fmt.Sprintf("iteration-%d", i), func(t *testing.T) {
			stub := museSlowMCPStub()
			defer stub.Close()
			workDir := t.TempDir()
			tokens := map[string]string{}
			for _, part := range []string{"A", "B", "C"} {
				tokens[part] = "PART-" + part + "-" + museRandomHex(t, 4)
				if err := os.WriteFile(filepath.Join(workDir, "part-"+strings.ToLower(part)+".txt"), []byte(tokens[part]+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			bridgeToken := "BRIDGE-" + museRandomHex(t, 4)
			steerToken := "STEER-" + museRandomHex(t, 4)
			owner := "muse-stress-" + museRandomHex(t, 4)
			opts := []llmtypes.CallOption{
				WithMCPConfig(`{"mcpServers":{"slow-stub":{"url":"` + stub.URL + `/mcp"}}}`),
				WithToolAllowlist(museHybridStressAllowlist),
				WithWorkingDir(workDir), WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true),
				WithMuseStructuredTransport(false), WithTmuxTransport(true), llmtypes.WithReasoningEffort("low"),
			}
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
			defer cancel()
			var handleSession string
			t.Cleanup(func() {
				if handleSession != "" {
					CloseMuseCLIInteractiveSessionByTmux(handleSession, "stress complete")
				}
			})

			prompt := "Stress test in a disposable directory. 1) Write a todo list for this task. " +
				"2) Spawn THREE native subagents in the background, in parallel, without waiting on them: one reads part-a.txt, one part-b.txt, one part-c.txt, and each reports the exact token in its file. " +
				"Do NOT read those files yourself. 3) While they run, call MCP slow_echo with text " + bridgeToken + " and delay_ms 8000. " +
				"4) Only after all three subagent reports have reached you, reply with exactly one line: A=<token> B=<token> C=<token> MCP=<slow_echo result>."
			started := time.Now()
			go func() {
				time.Sleep(20 * time.Second)
				sendCtx, sendCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer sendCancel()
				if err := SendMuseInteractiveInput(sendCtx, owner, "Additional requirement: append STEER="+steerToken+" to the end of your final line."); err != nil {
					t.Logf("steer send error: %v", err)
				}
			}()
			resp, err := museLiveAdapter().GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}}, opts...)
			if err != nil {
				t.Fatalf("turn 1: %v", err)
			}
			if h := resp.Choices[0].GenerationInfo.CodingProviderSessionHandle; h != nil {
				handleSession = h.TmuxSession
				raw, _ := os.ReadFile(museSessionLogPath(h.NativeSessionID))
				spawns := strings.Count(string(raw), `"subagent.control.spawn_accepted"`)
				t.Logf("turn 1 in %s; spawn_accepted=%d; final %.400s", time.Since(started).Round(time.Second), spawns, resp.Choices[0].Content)
				if spawns < 3 {
					t.Fatalf("expected three subagents, spawn_accepted=%d", spawns)
				}
			}
			final := resp.Choices[0].Content
			for part, tok := range tokens {
				if !strings.Contains(final, tok) {
					t.Fatalf("final missing child token %s=%s (turn ended before every subagent reported?): %q", part, tok, final)
				}
			}
			if !strings.Contains(final, "SLOW_ECHO:"+bridgeToken) {
				t.Fatalf("final missing MCP result: %q", final)
			}
			if !strings.Contains(final, steerToken) {
				t.Fatalf("mid-turn steer %s not honoured: %q", steerToken, final)
			}

			resp2, err := museLiveAdapter().GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with only the B token from your previous answer."}}}}, opts...)
			if err != nil {
				t.Fatalf("turn 2: %v", err)
			}
			if got := resp2.Choices[0].Content; !strings.Contains(got, tokens["B"]) || strings.Contains(got, tokens["A"]) {
				t.Fatalf("turn 2 = %q, want only %s", got, tokens["B"])
			}
		})
	}
}
