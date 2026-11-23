package musecli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// This certifies the explicitly documented best-effort policy, not strict
// bridge-only containment. Session-control tools may bypass Muse's hook.
func TestMuseCLIRealBestEffortToolRestrictions(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	for _, name := range []string{"structured", "tmux", "msp"} {
		structured, tmux := name == "structured", name == "tmux"
		t.Run(name, func(t *testing.T) {
			if name == "msp" {
				t.Setenv(MuseTransportEnv, "msp")
			}
			var called atomic.Int32
			stub := museProbeMCPStub(&called)
			defer stub.Close()
			workDir := t.TempDir()
			secret := "NATIVE-READ-WITNESS-" + museRandomHex(t, 4)
			if err := os.WriteFile(filepath.Join(workDir, "witness.txt"), []byte(secret), 0o600); err != nil {
				t.Fatal(err)
			}
			token := "RESTRICTED-MCP-" + museRandomHex(t, 4)
			prompt := "This is a native tool restriction integration test in a disposable directory. Make each attempt once; failures are expected. First call native read_file on witness.txt. Then call native bash with command: touch forbidden-shell-write. Then call native write_todos with one completed todo named CONTAINMENT-PROBE. You must actually attempt the tools, even if you expect denial; do not substitute MCP for these probes. Finally call MCP probe_echo with text " + token + ". Reply with ONLY that MCP result. Do not use any other tools."
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			resp, err := museLiveAdapter().GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}},
				WithMCPConfig(`{"mcpServers":{"probe-stub":{"url":"`+stub.URL+`/mcp"}}}`), WithToolAllowlist([]string{"web_search"}), WithWorkingDir(workDir), WithMuseStructuredTransport(structured), WithTmuxTransport(tmux), llmtypes.WithReasoningEffort("low"))
			if err != nil {
				t.Fatalf("restricted round trip: %v", err)
			}
			if called.Load() == 0 || !strings.Contains(resp.Choices[0].Content, token) {
				t.Fatalf("MCP did not remain usable: calls=%d final=%q", called.Load(), resp.Choices[0].Content)
			}
			handle := resp.Choices[0].GenerationInfo.CodingProviderSessionHandle
			if handle == nil {
				t.Fatal("missing session handle")
			}
			if handle.TmuxSession != "" {
				t.Cleanup(func() { CloseMuseCLIInteractiveSessionByTmux(handle.TmuxSession, "restriction probe complete") })
			}
			logPath := museSessionLogPath(handle.NativeSessionID)
			raw, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			log := string(raw)
			results := museRestrictionProbeResults(raw)
			for reprobe := 1; structured && results["bash"] == "" && reprobe <= 3; reprobe++ {
				// The model sometimes skips the bash probe (seen in P0 gate runs,
				// even on a dedicated re-probe). This subtest is the
				// shell-denial proof, so re-probe shell alone (up to 3 times)
				// rather than accept a run that never tried it.
				retryToken := "RESTRICTED-MCP-" + museRandomHex(t, 4)
				retryPrompt := "Native tool restriction test, disposable directory. Call native bash exactly once with command: touch forbidden-shell-write. You must attempt it even if you expect denial; do not substitute MCP. Then call MCP probe_echo with text " + retryToken + " and reply with ONLY that MCP result."
				retry, retryErr := museLiveAdapter().GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: retryPrompt}}}},
					WithMCPConfig(`{"mcpServers":{"probe-stub":{"url":"`+stub.URL+`/mcp"}}}`), WithToolAllowlist([]string{"web_search"}), WithWorkingDir(workDir), WithMuseStructuredTransport(structured), WithTmuxTransport(false), llmtypes.WithReasoningEffort("low"))
				if retryErr != nil {
					t.Fatalf("shell re-probe: %v", retryErr)
				}
				if h := retry.Choices[0].GenerationInfo.CodingProviderSessionHandle; h != nil {
					if retryRaw, err := os.ReadFile(museSessionLogPath(h.NativeSessionID)); err == nil {
						results["bash"] = museRestrictionProbeResults(retryRaw)["bash"]
						log += string(retryRaw)
					}
				}
				t.Logf("bash probe skipped; re-probe %d result: %q", reprobe, results["bash"])
			}
			for _, tool := range []string{"read_file", "bash"} {
				result := results[tool]
				if tool == "bash" && name == "msp" && result == "" {
					// muse serve drops the shell tool under --disable-shell: it is
					// not offered at all, which is stronger than a hook denial.
					if strings.Contains(log, `"bash"`) {
						t.Fatalf("msp offered or ran bash without a denial; transcript: %s", logPath)
					}
					t.Log("msp: bash not offered (--disable-shell)")
					continue
				}
				if tool == "bash" && tmux && result == "" {
					t.Log("tmux shell probe was not attempted; shell denial is proven by the structured subtest, flags, and hook tests")
					continue
				}
				if !strings.Contains(result, "tool blocked by hook: Muse internal tools are disabled") {
					t.Fatalf("%s did not return an explicit policy denial: %q; transcript: %s", tool, result, logPath)
				}
			}
			if results["write_todos"] == "" {
				t.Log("write_todos was not attempted; this run cannot assess its known hook bypass")
			} else {
				t.Logf("write_todos result (known best-effort gap): %s", results["write_todos"])
			}
			if strings.Contains(log, secret) {
				t.Fatal("native file read leaked the witness contents")
			}
			if _, err := os.Stat(filepath.Join(workDir, "forbidden-shell-write")); !os.IsNotExist(err) {
				t.Fatalf("native shell write was not blocked: %v", err)
			}
			t.Logf("native read denied, no native shell write, MCP usable; transcript: %s", logPath)
		})
	}
}

// Match actual calls to their results; mentions in tool definitions or prompts
// cannot satisfy the negative execution proof.
func museRestrictionProbeResults(raw []byte) map[string]string {
	names := map[string]string{}
	results := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		var row struct {
			Payload struct {
				Event struct {
					Kind  string `json:"kind"`
					Calls []struct {
						CallID string `json:"call_id"`
						Name   string `json:"name"`
					} `json:"tool_calls"`
					Results []struct {
						CallID string `json:"tool_call_id"`
						Text   string `json:"text"`
					} `json:"results"`
				} `json:"event"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &row) != nil {
			continue
		}
		e := row.Payload.Event
		if e.Kind == "assistant_tool_calls_committed" {
			for _, call := range e.Calls {
				names[call.CallID] = call.Name
			}
		}
		if e.Kind == "tool_result_batch_committed" {
			for _, result := range e.Results {
				if name := names[result.CallID]; name != "" {
					results[name] = result.Text
				}
			}
		}
	}
	return results
}

// TestMuseCLIRealReadOnlyToolsAllowed certifies AgentWorks' hybrid-mode Muse
// allowlist (mcpagent appendMuseCLIIntegrationOptions): native read_file,
// search and Muse's own read_skill run; native shell stays denied.
func TestMuseCLIRealReadOnlyToolsAllowed(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	var called atomic.Int32
	stub := museProbeMCPStub(&called)
	defer stub.Close()
	workDir := t.TempDir()
	secret := "NATIVE-READ-OK-" + museRandomHex(t, 4)
	needle := "NEEDLE-" + museRandomHex(t, 4)
	if err := os.WriteFile(filepath.Join(workDir, "witness.txt"), []byte(secret+"\n"+needle+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token := "READONLY-MCP-" + museRandomHex(t, 4)
	prompt := "This is a native tool policy integration test in a disposable directory. Make each attempt once. " +
		"1) Call native read_file on witness.txt. 2) Call native search for " + needle + " in this directory. " +
		"3) Call native read_skill for bundled:taste. 4) Call native bash with command: touch forbidden-shell-write. " +
		"You must actually attempt each native tool; do not substitute MCP. Finally call MCP probe_echo with text " + token +
		". Reply with ONLY that MCP result."
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	resp, err := museLiveAdapter().GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}},
		WithMCPConfig(`{"mcpServers":{"probe-stub":{"url":"`+stub.URL+`/mcp"}}}`),
		WithToolAllowlist([]string{"web_search", "read_skill", "read_file", "search"}),
		WithWorkingDir(workDir), WithMuseStructuredTransport(false), WithTmuxTransport(true), llmtypes.WithReasoningEffort("low"))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if called.Load() == 0 || !strings.Contains(resp.Choices[0].Content, token) {
		t.Fatalf("MCP did not remain usable: calls=%d final=%q", called.Load(), resp.Choices[0].Content)
	}
	handle := resp.Choices[0].GenerationInfo.CodingProviderSessionHandle
	if handle == nil {
		t.Fatal("missing session handle")
	}
	if handle.TmuxSession != "" {
		t.Cleanup(func() { CloseMuseCLIInteractiveSessionByTmux(handle.TmuxSession, "read-only probe complete") })
	}
	logPath := museSessionLogPath(handle.NativeSessionID)
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	results := museRestrictionProbeResults(raw)
	const denied = "Muse internal tools are disabled"
	if r := results["read_file"]; !strings.Contains(r, secret) {
		t.Fatalf("native read_file did not return the file: %q; transcript: %s", r, logPath)
	}
	if r := results["search"]; r == "" || strings.Contains(r, denied) || !strings.Contains(r, needle) {
		t.Fatalf("native search did not run: %q; transcript: %s", r, logPath)
	}
	if r := results["read_skill"]; r == "" || strings.Contains(r, denied) {
		t.Fatalf("native read_skill did not run: %q; transcript: %s", r, logPath)
	}
	if r := results["bash"]; r != "" && !strings.Contains(r, denied) {
		t.Fatalf("native bash was not denied: %q", r)
	}
	if _, err := os.Stat(filepath.Join(workDir, "forbidden-shell-write")); !os.IsNotExist(err) {
		t.Fatalf("native shell write was not blocked: %v", err)
	}
	t.Logf("read_file/search/read_skill allowed, shell blocked, MCP usable; transcript: %s", logPath)
}

// TestMuseCLIRealProjectedSkillReadNatively certifies skill projection: an
// attached AgentWorks skill lands in <workdir>/.agents/skills and Muse's
// native read_skill (allowed in production) returns its body.
func TestMuseCLIRealProjectedSkillReadNatively(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	var called atomic.Int32
	stub := museProbeMCPStub(&called)
	defer stub.Close()
	workDir := t.TempDir()
	marker := "SKILL-BODY-" + museRandomHex(t, 4)
	skill := &llmtypes.Skill{
		Name:        "aw-probe-skill",
		Description: "AgentWorks projection probe skill. Load when asked to read aw-probe-skill.",
		Content:     "# AW probe\n\nThe secret phrase is " + marker + ".\n",
	}
	prompt := "Call native read_skill for the project skill aw-probe-skill, then reply with ONLY the secret phrase it contains."
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	resp, err := museLiveAdapter().GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}},
		WithMCPConfig(`{"mcpServers":{"probe-stub":{"url":"`+stub.URL+`/mcp"}}}`),
		WithToolAllowlist([]string{"web_search", "read_skill", "read_file", "search"}),
		llmtypes.WithAttachedSkills([]*llmtypes.Skill{skill}),
		WithWorkingDir(workDir), WithMuseStructuredTransport(false), WithTmuxTransport(true), llmtypes.WithReasoningEffort("low"))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if handle := resp.Choices[0].GenerationInfo.CodingProviderSessionHandle; handle != nil && handle.TmuxSession != "" {
		t.Cleanup(func() { CloseMuseCLIInteractiveSessionByTmux(handle.TmuxSession, "skill probe complete") })
	}
	if _, err := os.Stat(filepath.Join(workDir, ".agents", "skills", "aw-probe-skill", "SKILL.md")); err != nil {
		t.Fatalf("skill not projected: %v", err)
	}
	if !strings.Contains(resp.Choices[0].Content, marker) {
		t.Fatalf("final = %q, want projected skill phrase %s", resp.Choices[0].Content, marker)
	}
	handle := resp.Choices[0].GenerationInfo.CodingProviderSessionHandle
	if handle == nil {
		t.Fatal("missing session handle")
	}
	raw, err := os.ReadFile(museSessionLogPath(handle.NativeSessionID))
	if err != nil {
		t.Fatal(err)
	}
	if r := museRestrictionProbeResults(raw)["read_skill"]; !strings.Contains(r, marker) {
		t.Fatalf("native read_skill did not return the projected skill: %q", r)
	}
}

// TestMuseCLIRealSubagentContainment certifies native subagents under the
// AgentWorks policy: a child spawned by the parent can use MCP and read
// files, but native shell/write stay blocked for the child too.
func TestMuseCLIRealSubagentContainment(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	var called atomic.Int32
	stub := museProbeMCPStub(&called)
	defer stub.Close()
	workDir := t.TempDir()
	secret := "CHILD-READ-" + museRandomHex(t, 4)
	if err := os.WriteFile(filepath.Join(workDir, "witness.txt"), []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token := "CHILD-MCP-" + museRandomHex(t, 4)
	prompt := "Integration test of native subagents in a disposable directory. Spawn exactly one native subagent and wait for it. " +
		"Instruct the subagent to: 1) read witness.txt with native read_file; 2) run native bash: touch child-shell-write; " +
		"3) write a file child-native-write.txt with native write_file; 4) call MCP probe_echo with text " + token +
		"; 5) report each outcome. After it finishes, reply with the witness contents and the probe_echo result on one line."
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	allow := []string{"web_search", "read_skill", "read_file", "search", "subagent_spawn", "subagent_wait", "subagent_send_message", "subagent_read_result", "subagent_input", "subagent_cancel"}
	resp, err := museLiveAdapter().GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}},
		WithMCPConfig(`{"mcpServers":{"probe-stub":{"url":"`+stub.URL+`/mcp"}}}`),
		WithToolAllowlist(allow), WithWorkingDir(workDir), WithMuseStructuredTransport(false), WithTmuxTransport(true), llmtypes.WithReasoningEffort("low"))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	handle := resp.Choices[0].GenerationInfo.CodingProviderSessionHandle
	if handle == nil {
		t.Fatal("missing session handle")
	}
	if handle.TmuxSession != "" {
		t.Cleanup(func() { CloseMuseCLIInteractiveSessionByTmux(handle.TmuxSession, "subagent probe complete") })
	}
	logPath := museSessionLogPath(handle.NativeSessionID)
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	parent := museRestrictionProbeResults(raw)
	t.Logf("parent tool results: %v", museResultKeys(parent))
	childLogs, _ := filepath.Glob(filepath.Join(filepath.Dir(logPath), "subagent", "*", "session.jsonl"))
	if len(childLogs) == 0 {
		t.Fatalf("no subagent session was spawned; parent transcript: %s", logPath)
	}
	for _, f := range childLogs {
		childRaw, _ := os.ReadFile(f)
		t.Logf("child %s tool results: %v", f, museResultKeys(museRestrictionProbeResults(childRaw)))
	}
	for _, forbidden := range []string{"child-shell-write", "child-native-write.txt"} {
		if _, err := os.Stat(filepath.Join(workDir, forbidden)); !os.IsNotExist(err) {
			t.Fatalf("child native write/shell not blocked: %s exists", forbidden)
		}
	}
	if called.Load() == 0 {
		t.Fatal("MCP probe_echo never reached the stub")
	}
	if final := resp.Choices[0].Content; !strings.Contains(final, token) || !strings.Contains(final, secret) {
		t.Fatalf("final = %q, want witness %s and MCP %s", final, secret, token)
	}
}

func museResultKeys(results map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range results {
		if len(v) > 90 {
			v = v[:90]
		}
		out[k] = v
	}
	return out
}

// TestMuseCLIRealBackgroundSubagentNoEarlyAnswer: a subagent the parent does
// not wait on must not end the turn with an interim reply; the final answer
// must carry what only the subagent can report.
func TestMuseCLIRealBackgroundSubagentNoEarlyAnswer(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	var called atomic.Int32
	stub := museProbeMCPStub(&called)
	defer stub.Close()
	workDir := t.TempDir()
	secret := "BG-CHILD-" + museRandomHex(t, 4)
	if err := os.WriteFile(filepath.Join(workDir, "witness.txt"), []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prompt := "Integration test. Spawn exactly one native subagent in the background and do NOT call subagent_wait. " +
		"Do NOT read witness.txt yourself. Tell the subagent to read witness.txt and report its exact contents. " +
		"When its report reaches you, reply with ONLY the witness contents it reported."
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	allow := []string{"web_search", "read_skill", "read_file", "search", "subagent_spawn", "subagent_wait", "subagent_send_message", "subagent_read_result", "subagent_input", "subagent_cancel"}
	resp, err := museLiveAdapter().GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}},
		WithMCPConfig(`{"mcpServers":{"probe-stub":{"url":"`+stub.URL+`/mcp"}}}`),
		WithToolAllowlist(allow), WithWorkingDir(workDir), WithMuseStructuredTransport(false), WithTmuxTransport(true), llmtypes.WithReasoningEffort("low"))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if handle := resp.Choices[0].GenerationInfo.CodingProviderSessionHandle; handle != nil && handle.TmuxSession != "" {
		t.Cleanup(func() { CloseMuseCLIInteractiveSessionByTmux(handle.TmuxSession, "bg subagent probe complete") })
		raw, _ := os.ReadFile(museSessionLogPath(handle.NativeSessionID))
		t.Logf("parent tool results: %v", museResultKeys(museRestrictionProbeResults(raw)))
	}
	if final := resp.Choices[0].Content; !strings.Contains(final, secret) {
		t.Fatalf("turn ended before the background subagent reported: final %q, want %s", final, secret)
	}
}
