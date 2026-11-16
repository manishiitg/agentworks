package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// PLAT-746-1030: tools belong in collapsed native UI rows, never human/agent
// prompt history. Exercise the real bounded journal window, including append.
func TestNativeTerminalDisplayKeepsToolsOutOfPromptHistory(t *testing.T) {
	for _, provider := range []string{"claude-code", "codex-cli"} {
		t.Run(provider, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "native.jsonl")
			records := []string{
				`{"type":"user","timestamp":"2026-11-16T10:00:00Z","message":{"content":"Please **check** this"}}`,
				`{"type":"assistant","timestamp":"2026-11-16T10:00:01Z","message":{"content":[{"type":"text","text":"## Checking"},{"type":"tool_use","id":"call-1","name":"Bash","input":{"command":"printf checked"}}]}}`,
				`{"type":"user","timestamp":"2026-11-16T10:00:02Z","message":{"content":[{"type":"tool_result","tool_use_id":"call-1","content":[{"type":"text","text":"checked"}]}]}}`,
			}
			if provider == "codex-cli" {
				records = []string{
					`{"type":"response_item","timestamp":"2026-11-16T10:00:00Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Please **check** this"}]}}`,
					`{"type":"response_item","timestamp":"2026-11-16T10:00:01Z","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"## Checking"}]}}`,
					`{"type":"response_item","timestamp":"2026-11-16T10:00:01Z","payload":{"type":"function_call","call_id":"call-1","name":"exec_command","arguments":"{\"cmd\":\"printf checked\"}"}}`,
					`{"type":"response_item","timestamp":"2026-11-16T10:00:02Z","payload":{"type":"function_call_output","call_id":"call-1","output":"checked"}}`,
				}
			}
			if err := os.WriteFile(path, []byte(strings.Join(records, "\n")+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			display, _, err := readNativeTranscriptWindow(path, provider, time.Time{}, true)
			if err != nil || len(display) != 4 {
				t.Fatalf("display rows=%d error=%v", len(display), err)
			}
			if display[0].Role != "human" || display[1].Role != "ai" || display[2].Role != "tool" || display[3].Role != "tool" || display[2].ToolID == "" || display[2].ToolID != display[3].ToolID || display[2].ToolPhase != "call" || display[3].ToolPhase != "result" || !strings.Contains(builderConversationMessageText(display[2]), "printf checked") || builderConversationMessageText(display[3]) != "checked" {
				t.Fatal("lost native roles or typed tool call/result")
			}
			prompt, _, err := readRecentNativeTranscript(path, provider, time.Time{})
			if err != nil || len(prompt) != 2 || prompt[0].NativeID == display[0].NativeID {
				t.Fatal("display contaminated prompt history/identity")
			}
			for _, row := range prompt {
				if row.ToolID != "" || row.Role == "tool" || strings.Contains(builderConversationMessageText(row), "printf checked") {
					t.Fatal("tool became prompt text")
				}
			}
			// A large result is still bounded and an incomplete append does not advance.
			record := map[string]any{"type": "user", "timestamp": "2026-11-16T10:00:03Z", "message": map[string]any{"content": []map[string]any{{"type": "tool_result", "tool_use_id": "call-1", "content": strings.Repeat("x", nativeHistoryMessageBytes*2)}}}}
			if provider == "codex-cli" {
				record = map[string]any{"type": "response_item", "timestamp": "2026-11-16T10:00:03Z", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "call-1", "output": strings.Repeat("x", nativeHistoryMessageBytes*2)}}
			}
			data, _ := json.Marshal(record)
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			f.Write(data)
			f.Close()
			partial, _, err := readNativeTranscriptWindow(path, provider, time.Time{}, true)
			if err != nil || len(partial) != 4 {
				t.Fatal("partial tool row was consumed")
			}
			f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			f.WriteString("\n")
			f.Close()
			next, _, err := readNativeTranscriptWindow(path, provider, time.Time{}, true)
			if err != nil || len(next) != 5 || len(builderConversationMessageText(next[4])) > nativeHistoryMessageBytes+3 {
				t.Fatal("append lost tool result or text bound")
			}
			// Redact complete JSON before bounding: truncating first can remove
			// the closing quote needed to recognize a long credential value.
			secret := strings.Repeat("credential-value-", 2000)
			args := map[string]any{"api_key": secret, "command": "printf checked"}
			record = map[string]any{"type": "assistant", "timestamp": "2026-11-16T10:00:04Z", "message": map[string]any{"content": []map[string]any{{"type": "tool_use", "id": "secret-call", "name": "Bash", "input": args}}}}
			if provider == "codex-cli" {
				rawArgs, _ := json.Marshal(args)
				record = map[string]any{"type": "response_item", "timestamp": "2026-11-16T10:00:04Z", "payload": map[string]any{"type": "function_call", "call_id": "secret-call", "name": "exec_command", "arguments": string(rawArgs)}}
			}
			data, _ = json.Marshal(record)
			f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			f.Write(data)
			f.WriteString("\n")
			f.Close()
			redacted, _, err := readNativeTranscriptWindow(path, provider, time.Time{}, true)
			if err != nil || len(redacted) != 6 {
				t.Fatal("secret-shaped tool call lost")
			}
			text := builderConversationMessageText(redacted[5])
			if strings.Contains(text, "credential-value-") || !strings.Contains(text, "[redacted]") {
				t.Fatal("display bounded before credential redaction")
			}

		})
	}
}
