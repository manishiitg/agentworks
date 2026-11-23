package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// claudeHybridLiveTools mirrors mcpagent's claudeHybridNativeTools.
const claudeHybridLiveTools = "WebSearch,WebFetch,Read,Grep,Glob,Skill,Agent,TaskCreate,TaskGet,TaskUpdate,TaskList,TodoWrite"

// claudeAssertNoNativeWrites fails if the parent or any subagent used a native
// shell or write tool, or if a probe file appeared on disk.
func claudeAssertNoNativeWrites(t *testing.T, names map[string]int, workDir string, probes ...string) {
	t.Helper()
	for _, forbidden := range []string{"Bash", "Write", "Edit", "MultiEdit", "NotebookEdit"} {
		if names[forbidden] > 0 {
			t.Fatalf("hybrid must never run native %s: %v", forbidden, names)
		}
	}
	for _, probe := range probes {
		if _, err := os.Stat(filepath.Join(workDir, probe)); !os.IsNotExist(err) {
			t.Fatalf("native shell/write happened: %s exists", probe)
		}
	}
}

func claudeNativeToolsOptions(t *testing.T, workDir, sessionID string) []llmtypes.CallOption {
	t.Helper()
	mcpServerPath := writeClaudeInteractiveSlowMCPServer(t, filepath.Join(t.TempDir(), "slow-tool-started"))
	return []llmtypes.CallOption{
		WithInteractiveSessionID(sessionID),
		WithPersistentInteractiveSession(true),
		WithWorkingDir(workDir),
		WithMCPConfig(fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":%q}}}`, mcpServerPath)),
		// Mirrors mcpagent's hybrid coding-tools mode (claudeHybridNativeTools):
		// native read/search, skills, todos, subagents and web tools; never Bash
		// or file writes. Auto permissions, the bridge still mounted.
		WithClaudeCodeTools(claudeHybridLiveTools),
		WithPermissionMode("auto"),
		WithAllowedTools("mcp__api-bridge__*,WebSearch"),
		WithEffort("low"),
	}
}

// claudeTranscriptToolNames lists every tool_use name in a Claude transcript,
// including subagent sidechains written to the same project directory.
func claudeTranscriptToolNames(t *testing.T, sessionID, workDir string) map[string]int {
	t.Helper()
	path, err := resolveClaudeTranscriptPath(sessionID, workDir, true)
	if err != nil {
		t.Fatalf("resolve transcript: %v", err)
	}
	files := []string{path}
	sub, _ := filepath.Glob(filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents", "*.jsonl"))
	files = append(files, sub...)
	names := map[string]int{}
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		for _, line := range strings.Split(string(raw), "\n") {
			var row struct {
				Message struct {
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(line), &row) != nil {
				continue
			}
			var blocks []struct {
				Type string `json:"type"`
				Name string `json:"name"`
			}
			if json.Unmarshal(row.Message.Content, &blocks) != nil {
				continue
			}
			for _, b := range blocks {
				if b.Type == "tool_use" {
					names[b.Name]++
				}
			}
		}
	}
	return names
}

// TestClaudeCodeTmuxRealHybridNativeToolsP0: in hybrid mode Claude's native
// Read/Grep/Skill/todo tools work alongside the MCP bridge, and projected
// AgentWorks skills load through the native Skill tool.
func TestClaudeCodeTmuxRealHybridNativeToolsP0(t *testing.T) {
	skipClaudeInteractivePersistentE2E(t)
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })
	workDir := t.TempDir()
	secret := "CLAUDE-READ-" + randomHex(4)
	needle := "CLAUDE-NEEDLE-" + randomHex(4)
	hidden := "CLAUDE-GREP-HIT-" + randomHex(4)
	if err := os.WriteFile(filepath.Join(workDir, "witness.txt"), []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workDir, "deep", "dir"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "deep", "dir", "notes.md"), []byte(needle+" "+hidden+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	skillPhrase := "SKILL-PHRASE-" + randomHex(4)
	opts := append(claudeNativeToolsOptions(t, workDir, "claude-native-tools-"+randomHex(4)),
		llmtypes.WithAttachedSkills([]*llmtypes.Skill{{
			Name:        "aw-probe-skill",
			Description: "AgentWorks probe skill. Use when asked for the aw-probe-skill phrase.",
			Content:     "The aw-probe-skill phrase is " + skillPhrase + ".\n",
		}}))
	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	prompt := "Integration test in a disposable directory. Do each step with your native tools: " +
		"1) Create a todo list for these steps. 2) Read witness.txt. 3) Search the directory for " + needle + " and note the token after it. " +
		"4) Use the aw-probe-skill skill to get its phrase. 5) Try to run the shell command: touch shell-write-attempt (report if unavailable). " +
		"6) Call the api-bridge slow_contract MCP tool with token BRIDGE-OK and delay_ms 100. " +
		"Finally reply on one line: the witness contents, the token after the needle, the skill phrase, and the MCP result."
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}}, opts...)
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	final := firstChoiceText(resp)
	for _, want := range []string{secret, hidden, skillPhrase, "BRIDGE-OK"} {
		if !strings.Contains(final, want) {
			t.Fatalf("final missing %q: %q", want, final)
		}
	}
	names := claudeTranscriptToolNames(t, experimentalClaudeSessionID(resp), workDir)
	t.Logf("tool_use names: %v", names)
	if names["Read"] == 0 || names["Skill"] == 0 {
		t.Fatalf("native Read/Skill not used: %v", names)
	}
	claudeAssertNoNativeWrites(t, names, workDir, "shell-write-attempt")
}

// TestClaudeCodeTmuxRealHybridBackgroundAgentP0: a subagent Claude runs in the
// background must not end the turn early. Claude records
// pendingBackgroundAgentCount on the turn_duration row; completion waits until
// the agent reports and Claude's follow-up turn ends with the count at 0.
func TestClaudeCodeTmuxRealHybridBackgroundAgentP0(t *testing.T) {
	skipClaudeInteractivePersistentE2E(t)
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })
	workDir := t.TempDir()
	secret := "CHILD-READ-" + randomHex(4)
	if err := os.WriteFile(filepath.Join(workDir, "witness.txt"), []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	prompt := "Integration test in a disposable directory. Use the Agent tool with run_in_background set to true to spawn exactly one subagent. " +
		"Do NOT read witness.txt yourself. Instruct the subagent to read witness.txt and report its exact contents. " +
		"When the subagent's report arrives, reply with ONLY the witness contents it reported."
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}},
		claudeNativeToolsOptions(t, workDir, "claude-native-agent-"+randomHex(4))...)
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	final := firstChoiceText(resp)
	names := claudeTranscriptToolNames(t, experimentalClaudeSessionID(resp), workDir)
	t.Logf("tool_use names (parent+subagents): %v; final: %.300s", names, final)
	if names["Agent"]+names["Task"] == 0 {
		t.Fatalf("no subagent was spawned: %v", names)
	}
	if !strings.Contains(final, secret) {
		t.Fatalf("turn ended before the background agent reported: final %q, want %s", final, secret)
	}
	claudeAssertNoNativeWrites(t, names, workDir)
}
