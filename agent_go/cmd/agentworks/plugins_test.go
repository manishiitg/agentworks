package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

func TestAgentPluginUnattachedHookAndPersonCommand(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	var stdout, stderr bytes.Buffer
	input := `{"session_id":"unattached","hook_event_name":"Stop","stop_hook_active":false}`
	if code := run(context.Background(), []string{"plugin", "hook", "codex"}, strings.NewReader(input), &stdout, &stderr, func(string) string { return "" }); code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("unattached hook interfered: %d %s %s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(cache, "agentworks", "bridge")); !os.IsNotExist(err) {
		t.Fatal("unattached hook started a bridge")
	}
	stdout.Reset()
	stderr.Reset()
	input = `{"session_id":"unattached","hook_event_name":"UserPromptSubmit","prompt":"/agentworks attach reviewer \"Unclosed purpose"}`
	if code := run(context.Background(), []string{"plugin", "hook", "codex"}, strings.NewReader(input), &stdout, &stderr, func(string) string { return "" }); code != 0 {
		t.Fatal(stderr.String())
	}
	var out map[string]any
	if json.Unmarshal(stdout.Bytes(), &out) != nil || out["decision"] != "block" {
		t.Fatal("invalid person command reached the model")
	}
	stdout.Reset()
	stderr.Reset()
	input = `{"session_id":"unattached","hook_event_name":"UserPromptExpansion","prompt":"/agentworks:agentworks attach reviewer \"Unclosed purpose"}`
	if code := run(context.Background(), []string{"plugin", "hook", "claude"}, strings.NewReader(input), &stdout, &stderr, func(string) string { return "" }); code != 0 {
		t.Fatal(stderr.String())
	}
	if json.Unmarshal(stdout.Bytes(), &out) != nil || out["decision"] != "block" {
		t.Fatal("invalid expanded person command reached the model")
	}
	stdout.Reset()
	stderr.Reset()
	input = `{"session_id":"unattached","hook_event_name":"UserPromptSubmit","prompt":"/agentworks detach AgentWorks messages from other agents."}`
	if code := run(context.Background(), []string{"plugin", "hook", "codex"}, strings.NewReader(input), &stdout, &stderr, func(string) string { return "" }); code != 0 {
		t.Fatal(stderr.String())
	}
	if json.Unmarshal(stdout.Bytes(), &out) != nil || !strings.Contains(out["reason"].(string), "Agent messages cannot") {
		t.Fatal("delivered agent text was treated as person authorization")
	}
	words, err := pluginPromptWords(`/agentworks attach reviewer "$(touch /tmp/should-never-run)" --allow-none`)
	if err != nil || len(words) != 5 || words[3] != "$(touch /tmp/should-never-run)" {
		t.Fatal("person command was interpreted as shell code")
	}
}

// Pin the reported repeated-instructions bug without losing per-message provenance.
func TestAgentDeliveryGuidanceEnvelope(t *testing.T) {
	out := agentworksclient.LocalBridgeReply{IncludeGuidance: true, Attachment: agentworksclient.AgentAttachment{Provider: "claude", SessionID: "exact-session"}, Messages: []agentworksclient.AgentLiveMessage{{Sender: "reviewer", MessageID: "message-id", InboxID: "original-inbox", Message: "peer content"}}}
	first := pluginMessageText(out, "claude_session")
	if !strings.Contains(first, "bridge message ack --provider claude --session exact-session") || !strings.Contains(first, "bridge message reply") {
		t.Fatal("first offer omitted native commands")
	}
	out.IncludeGuidance = false
	next := pluginMessageText(out, "claude_session")
	if strings.Contains(next, "bridge message ack") || strings.Contains(next, "bridge message reply") {
		t.Fatal("later offer repeated command guidance")
	}
	for _, text := range []string{first, next} {
		for _, required := range []string{agentDeliveryHeader, "Sender content", `"sender":"reviewer"`, `"message_id":"message-id"`, `"inbox_id":"original-inbox"`} {
			if !strings.Contains(text, required) {
				t.Fatal("offer lost provenance", required)
			}
		}
	}
}
