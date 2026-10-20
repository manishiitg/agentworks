package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

func TestChatLabelUsesExactTitleAndExplicitName(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CODEX_HOME", base)
	t.Setenv("CLAUDE_CONFIG_DIR", base)
	const session = "74e326ed-4de2-4ef0-a503-bf8344e326ed"
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(base, "session_index.jsonl"), `{"id":"`+session+`","thread_name":"Earlier"}`+"\n"+
		`{"id":"another-session","thread_name":"Other chat"}`+"\n"+
		`{"id":"`+session+`","thread_name":"Current title"}`)
	a := agentworksclient.AgentAttachment{Provider: "codex", SessionID: session, AgentName: "codex-workspace-1234abcd"}
	if got := attachedChatLabel(a, ""); got != "Current title" {
		t.Fatal(got)
	}
	if got := attachedChatLabel(a, a.AgentName); got != a.AgentName {
		t.Fatal("literal generated-looking NAME lost", got)
	}
	a.AgentName = "codex-reviewer"
	if got := attachedChatLabel(a, ""); got != a.AgentName {
		t.Fatal("role alias lost", got)
	}
	a.Provider, a.AgentName = "claude", "claude-workspace-1234abcd"
	path := filepath.Join(base, "projects", "workspace", session+".jsonl")
	ai := `{"type":"ai-title","sessionId":"` + session + `","aiTitle":"Automatic"}` + "\n"
	write(path, ai)
	if got := attachedChatLabel(a, ""); got != a.AgentName {
		t.Fatal(got)
	}
	custom := `{"type":"custom-title","sessionId":"` + session + `","customTitle":"Chosen title"}` + "\n"
	// A big tool result is irrelevant; a later automatic title cannot override the person's rename.
	write(path, custom+strings.Repeat("x", 70<<10)+"\n"+ai)
	if got := attachedChatLabel(a, ""); got != "Chosen title" {
		t.Fatal(got)
	}
	write(path, custom+`{"type":"custom-title","sessionId":"other","customTitle":"Wrong chat"}`+"\n"+
		`{"type":"custom-title","sessionId":"`+session+`","customTitle":"Renamed\n title\u202e"}`)
	if got := attachedChatLabel(a, ""); got != "Renamed title" {
		t.Fatal(got)
	}
}
