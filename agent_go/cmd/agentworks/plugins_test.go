package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
