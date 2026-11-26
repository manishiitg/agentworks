package claudecode

import (
	"context"
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

// writeWorkbenchMCPServer writes a real stdio MCP server exposing three tools
// that do actual work — web_search (returns a deterministic code word),
// write_file and read_file (real file I/O under outDir). codeWord + outDir are
// baked into the script so the test stays hermetic (no env/network) while
// exercising a realistic edit-a-file + search workflow through the MCP bridge.
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

// TestClaudeCodeTranscriptStreamingRealWorldLive is a realistic P0 test: a real
// Claude tmux turn, bridge-only, driving a multi-tool workflow (search → edit a
// file on disk → read it back) over a real MCP server. It proves the tailer
// streams a heavier, lifelike turn — MANY content chunks + MULTIPLE DISTINCT
// tool calls in order — AND that real work happened (result.txt was written to
// disk with the code word).
func TestClaudeCodeTranscriptStreamingRealWorldLive(t *testing.T) {
	skipClaudeInteractiveIntegration(t)
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	workDir := t.TempDir()
	outDir := t.TempDir()
	codeWord := "ZEBRA_" + randomHex(4)
	mcpConfig := fmt.Sprintf(`{"mcpServers":{"workbench":{"command":%q}}}`, writeWorkbenchMCPServer(t, outDir, codeWord))

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	streamChan := make(chan llmtypes.StreamChunk, 2048)
	captureDone := collectTranscriptStream(streamChan)

	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, workbenchRealWorldTask)},
		WithWorkingDir(workDir),
		WithMCPConfig(mcpConfig),
		WithClaudeCodeTools(""),
		WithAllowedTools("mcp__workbench__web_search mcp__workbench__write_file mcp__workbench__read_file"),
		WithEffort("low"),
		WithStreamTranscript(true),
		llmtypes.WithStreamingChan(streamChan),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	capture := <-captureDone

	final := ""
	if len(resp.Choices) == 1 {
		final = strings.TrimSpace(resp.Choices[0].Content)
	}
	t.Logf("realworld stream: %d content, %d tool start(s) %v, order=%v; streamed=%q final=%q",
		capture.contentChunks, capture.toolStarts, capture.toolNames, capture.order,
		strings.TrimSpace(capture.content.String()), final)

	// Real work happened: the file was actually written to disk via the bridge.
	wrote, readErr := os.ReadFile(filepath.Join(outDir, "result.txt"))
	if readErr != nil {
		t.Fatalf("result.txt was not written to disk (real file edit via bridge did not happen): %v", readErr)
	}
	if !strings.Contains(string(wrote), codeWord) {
		t.Fatalf("result.txt does not contain the searched code word %q; got %q", codeWord, string(wrote))
	}
	// The streaming carried the heavier, multi-tool turn.
	if capture.contentChunks < 2 {
		t.Fatalf("expected multiple narration content chunks, got %d; order=%v", capture.contentChunks, capture.order)
	}
	distinct := distinctToolNames(capture.toolNames)
	if len(distinct) < 2 {
		t.Fatalf("expected >= 2 DISTINCT tool calls streamed (search/write/read), got %v", capture.toolNames)
	}
	if !strings.Contains(final, codeWord) {
		t.Fatalf("final response missing the code word %q; final=%q", codeWord, final)
	}

	// Agentic validation: record the REAL streamed output and require an agent
	// to have reviewed and approved it. Deterministic checks above cannot judge
	// quality (coherence, no duplicated lines, natural interleaving) — an agent
	// must read testdata/agent-reviews/<test>.json and sign off. The fingerprint
	// is over the stable shape (order + distinct tools), so a behavior change in
	// a future CLI release invalidates the sign-off and forces a fresh review.
	rec := agentreview.Write(t, "TestClaudeCodeTranscriptStreamingRealWorldLive",
		"Claude bridge-only: web_search -> write_file (to disk) -> read_file, streamed live",
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

func distinctToolNames(names []string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
