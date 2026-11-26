package codexcli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/agentreview"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// writeWorkbenchMCPServer writes a real stdio MCP server exposing web_search
// (deterministic code word), write_file and read_file (real file I/O under
// outDir). Baked-in codeWord/outDir keep the test hermetic while exercising a
// realistic edit-a-file + search workflow through the MCP bridge.
func writeWorkbenchMCPServer(t *testing.T, outDir, codeWord string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workbench-mcp.js")
	script := fmt.Sprintf(`#!/usr/bin/env node
const fs = require("fs");
const path = require("path");
const readline = require("readline");
const OUT = %q;
const CODEWORD = %q;
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
function send(m){ process.stdout.write(JSON.stringify(m) + "\n"); }
rl.on("line", (line) => {
  if (!line.trim()) return;
  let msg; try { msg = JSON.parse(line); } catch (e) { return; }
  if (msg.method === "initialize") { send({jsonrpc:"2.0",id:msg.id,result:{protocolVersion:"2024-11-05",capabilities:{tools:{}},serverInfo:{name:"workbench",version:"1.0.0"}}}); return; }
  if (msg.method === "notifications/initialized") return;
  if (msg.method === "tools/list") {
    send({jsonrpc:"2.0",id:msg.id,result:{tools:[
      {name:"web_search",description:"Search the web for a query and return the top result.",inputSchema:{type:"object",properties:{query:{type:"string"}},required:["query"]}},
      {name:"write_file",description:"Write text content to a file by name.",inputSchema:{type:"object",properties:{name:{type:"string"},content:{type:"string"}},required:["name","content"]}},
      {name:"read_file",description:"Read a file's text content by name.",inputSchema:{type:"object",properties:{name:{type:"string"}},required:["name"]}}
    ]}}); return;
  }
  if (msg.method === "tools/call") {
    const name = msg.params && msg.params.name;
    const args = (msg.params && msg.params.arguments) || {};
    let text = "";
    try {
      if (name === "web_search") { text = "Top result: the project code word is " + CODEWORD + "."; }
      else if (name === "write_file") { fs.writeFileSync(path.join(OUT, String(args.name||"")), String(args.content||"")); text = "WROTE " + args.name; }
      else if (name === "read_file") { text = fs.readFileSync(path.join(OUT, String(args.name||"")), "utf8"); }
      else { text = "unknown tool"; }
      send({jsonrpc:"2.0",id:msg.id,result:{content:[{type:"text",text}],isError:false}});
    } catch (e) {
      send({jsonrpc:"2.0",id:msg.id,result:{content:[{type:"text",text:"ERR " + e.message}],isError:true}});
    }
    return;
  }
  if (msg.id !== undefined) send({jsonrpc:"2.0",id:msg.id,result:{}});
});
`, outDir, codeWord)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write workbench MCP server: %v", err)
	}
	return path
}

const workbenchRealWorldTask = "You have three tools from the 'workbench' MCP server: web_search, write_file, read_file. " +
	"Do these steps in order, writing one short sentence of narration BEFORE each tool call:\n" +
	"1. Use web_search with the query \"project code word\" to find the code word.\n" +
	"2. Use write_file to save ONLY that code word into a file named exactly result.txt.\n" +
	"3. Use read_file on result.txt to confirm what was saved.\n" +
	"Finally, reply with the code word on its own line."

func distinctToolNames(names []string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

// TestCodexAppServerStreamingRealWorldLive is the realistic Codex P0 test: a
// real Codex app-server turn, bridge-only, driving search → edit-a-file → read-back
// over a real MCP server. Proves native item events stream a heavier lifelike
// turn (multiple content chunks + distinct tool calls in order) and that real
// work happened (result.txt written to disk with the searched code word).
func TestCodexAppServerStreamingRealWorldLive(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	workDir := t.TempDir()
	outDir := t.TempDir()
	codeWord := "ZEBRA_" + codexRandomHex(4)

	mcpConfig, err := json.Marshal(map[string]any{"workbench": map[string]any{"command": writeWorkbenchMCPServer(t, outDir, codeWord)}})
	if err != nil {
		t.Fatal(err)
	}

	streamChan := make(chan llmtypes.StreamChunk, 2048)
	captureDone := collectCodexTranscriptStream(streamChan)
	opts := []llmtypes.CallOption{
		WithInteractiveSessionID("codex-realworld-stream-" + codexRandomHex(4)),
		WithPersistentInteractiveSession(true),
		WithProjectDirID(workDir),
		WithDisableShellTool(),
		WithApprovalPolicy("never"),
		WithReasoningEffort("low"),
		WithMCPServers(string(mcpConfig)),
		WithStreamTranscript(true),
		llmtypes.WithStreamingChan(streamChan),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, workbenchRealWorldTask)},
		opts...,
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	capture := <-captureDone

	final := ""
	if len(resp.Choices) == 1 {
		final = strings.TrimSpace(resp.Choices[0].Content)
	}
	t.Logf("codex realworld stream: %d content, %d tool start(s) %v, order=%v; streamed=%q final=%q",
		capture.contentChunks, capture.toolStarts, capture.toolNames, capture.order,
		strings.TrimSpace(capture.content.String()), final)

	wrote, readErr := os.ReadFile(filepath.Join(outDir, "result.txt"))
	if readErr != nil {
		t.Fatalf("result.txt was not written to disk (real file edit via bridge did not happen): %v", readErr)
	}
	if !strings.Contains(string(wrote), codeWord) {
		t.Fatalf("result.txt does not contain the searched code word %q; got %q", codeWord, string(wrote))
	}
	if capture.contentChunks < 2 {
		t.Fatalf("expected multiple narration content chunks, got %d; order=%v", capture.contentChunks, capture.order)
	}
	if len(distinctToolNames(capture.toolNames)) < 2 {
		t.Fatalf("expected >= 2 DISTINCT tool calls streamed (search/write/read), got %v", capture.toolNames)
	}
	if !strings.Contains(final, codeWord) {
		t.Fatalf("final response missing the code word %q; final=%q", codeWord, final)
	}

	// Agentic validation (see agentreview): record the REAL streamed output and
	// require an agent to review it. This is what catches quality regressions a
	// deterministic assert misses — e.g. the Codex double-write that streamed
	// every line twice yet still satisfied contentChunks>=2.
	rec := agentreview.Write(t, "TestCodexAppServerStreamingRealWorldLive",
		"Codex bridge-only: web_search -> write_file (to disk) -> read_file, streamed live",
		map[string]any{
			"content_chunks":   capture.contentTexts, // discrete chunks — review formatting/readability
			"streamed_content": strings.TrimSpace(capture.content.String()),
			"stream_order":     capture.order,
			"tool_names":       capture.toolNames,
			"final":            final,
			"file_on_disk":     string(wrote),
		},
		map[string]any{"distinct_tools": sortedKeys(distinctToolNames(capture.toolNames))},
	)
	agentreview.RequireReviewed(t, rec)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
