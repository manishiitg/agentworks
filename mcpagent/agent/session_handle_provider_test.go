package mcpagent

import (
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func testCodexAgentForHandle() *Agent {
	return &Agent{provider: llm.ProviderCodexCLI, modelID: "gpt-5.3-codex-spark", sessionID: "owner-current"}
}

type handleStateSnapshot struct {
	sessionID                 string
	codingProviderSessionHold llmtypes.CodingProviderSessionHandle
	codexSessionID            string
	claudeCodeSessionID       string
	provider                  string
	modelID                   string
	workingDir                string
}

func snapshotHandleState(a *Agent) handleStateSnapshot {
	return handleStateSnapshot{
		sessionID:                 a.sessionID,
		codingProviderSessionHold: a.codingProviderSessionHandle,
		codexSessionID:            a.codexSessionID,
		claudeCodeSessionID:       a.claudeCodeSessionID,
		provider:                  string(a.provider),
		modelID:                   a.modelID,
		workingDir:                a.codingAgentWorkingDir,
	}
}

func assertHandleStateUnchanged(t *testing.T, before handleStateSnapshot, after *Agent) {
	t.Helper()
	if after.sessionID != before.sessionID {
		t.Fatalf("sessionID = %q, want unchanged %q", after.sessionID, before.sessionID)
	}
	if after.codingProviderSessionHandle != before.codingProviderSessionHold {
		t.Fatalf("codingProviderSessionHandle = %+v, want unchanged %+v", after.codingProviderSessionHandle, before.codingProviderSessionHold)
	}
	if after.codexSessionID != before.codexSessionID || after.claudeCodeSessionID != before.claudeCodeSessionID {
		t.Fatal("per-provider native session fields must not change on rejection")
	}
	if string(after.provider) != before.provider || after.modelID != before.modelID {
		t.Fatal("configured provider/model must not change on rejection")
	}
	if after.codingAgentWorkingDir != before.workingDir {
		t.Fatal("working dir must not change on rejection")
	}
}

func TestApplyAgentSessionHandleRejectsForeignProvider(t *testing.T) {
	newClaudeHandle := func() *AgentSessionHandle {
		return &AgentSessionHandle{
			SessionID: "owner-previous",
			OwnerID:   "previous-owner",
			Provider: llmtypes.CodingProviderSessionHandle{
				Provider:        "claude-code",
				Model:           "claude-opus",
				NativeSessionID: "claude-native-id",
				WorkingDir:      "/old/workspace",
			},
		}
	}

	t.Run("empty connection IDs", func(t *testing.T) {
		agent := testCodexAgentForHandle()
		before := snapshotHandleState(agent)
		if accepted := agent.applyAgentSessionHandle(newClaudeHandle()); accepted {
			t.Fatal("a Claude handle must not restore into a Codex agent")
		}
		assertHandleStateUnchanged(t, before, agent)
		if _, ok := agent.codingProviderContinuationHandleForModel(llm.ProviderCodexCLI, agent.modelID); ok {
			t.Fatal("no Codex continuation may be obtainable from a rejected foreign handle")
		}
	})

	t.Run("equal named connections", func(t *testing.T) {
		agent := testCodexAgentForHandle()
		agent.llmConfig = AgentLLMConfiguration{Primary: LLMModel{Provider: "codex-cli", ModelID: agent.modelID, ConnectionID: "account-A"}}
		handle := newClaudeHandle()
		handle.ConnectionID = "account-A"
		before := snapshotHandleState(agent)
		if accepted := agent.applyAgentSessionHandle(handle); accepted {
			t.Fatal("equal connections must not admit a foreign provider")
		}
		assertHandleStateUnchanged(t, before, agent)
	})

	t.Run("global connection normalization", func(t *testing.T) {
		agent := testCodexAgentForHandle()
		agent.llmConfig = AgentLLMConfiguration{Primary: LLMModel{Provider: "codex-cli", ModelID: agent.modelID, ConnectionID: "global:codex-cli"}}
		handle := newClaudeHandle()
		handle.ConnectionID = "global:codex-cli"
		before := snapshotHandleState(agent)
		if accepted := agent.applyAgentSessionHandle(handle); accepted {
			t.Fatal("normalized global connections must not admit a foreign provider")
		}
		assertHandleStateUnchanged(t, before, agent)
	})

	t.Run("connection mismatch still rejects", func(t *testing.T) {
		agent := testCodexAgentForHandle()
		agent.llmConfig = AgentLLMConfiguration{Primary: LLMModel{Provider: "codex-cli", ModelID: agent.modelID, ConnectionID: "account-B"}}
		handle := &AgentSessionHandle{
			SessionID:    "owner-A",
			ConnectionID: "account-A",
			Provider:     llmtypes.CodingProviderSessionHandle{Provider: "codex-cli", NativeSessionID: "thread-A"},
		}
		before := snapshotHandleState(agent)
		if accepted := agent.applyAgentSessionHandle(handle); accepted {
			t.Fatal("a different connection must not restore")
		}
		assertHandleStateUnchanged(t, before, agent)
	})

	t.Run("nil handle rejects", func(t *testing.T) {
		agent := testCodexAgentForHandle()
		before := snapshotHandleState(agent)
		if accepted := agent.applyAgentSessionHandle(nil); accepted {
			t.Fatal("a nil handle must not restore")
		}
		assertHandleStateUnchanged(t, before, agent)
	})
}

func TestApplyAgentSessionHandleAcceptsSameProvider(t *testing.T) {
	t.Run("model change within provider", func(t *testing.T) {
		agent := testCodexAgentForHandle()
		handle := &AgentSessionHandle{
			SessionID: "owner-previous",
			Provider: llmtypes.CodingProviderSessionHandle{
				Provider:        "codex-cli",
				Model:           "gpt-5.2-codex",
				NativeSessionID: "codex-thread-1",
				Transport:       "tmux",
				TmuxSession:     "obsolete-terminal",
				WorkingDir:      "/work/project",
			},
		}
		if accepted := agent.applyAgentSessionHandle(handle); !accepted {
			t.Fatal("a same-provider handle must restore")
		}
		if snapshot := agent.currentAgentSessionHandle(); snapshot.Provider.Transport != "app-server" || snapshot.Provider.TmuxSession != "" {
			t.Fatalf("obsolete terminal identity survived restore: %#v", snapshot)
		}
		// The configured model wins for the new turn; the native
		// conversation identity comes from the handle.
		if agent.provider != llm.ProviderCodexCLI || agent.modelID != "gpt-5.3-codex-spark" {
			t.Fatalf("configured provider/model = %q/%q, want codex-cli/gpt-5.3-codex-spark", agent.provider, agent.modelID)
		}
		if agent.codingProviderSessionHandle.NativeSessionID != "codex-thread-1" {
			t.Fatalf("native session = %q, want codex-thread-1", agent.codingProviderSessionHandle.NativeSessionID)
		}
		if agent.sessionID != "owner-previous" {
			t.Fatalf("sessionID = %q, want owner-previous", agent.sessionID)
		}
		resolved, ok := agent.codingProviderContinuationHandleForModel(llm.ProviderCodexCLI, agent.modelID)
		if !ok || resolved.NativeSessionID != "codex-thread-1" {
			t.Fatalf("continuation = (%+v %v), want the restored native session", resolved, ok)
		}
	})

	t.Run("provider match is case-insensitive", func(t *testing.T) {
		agent := testCodexAgentForHandle()
		handle := &AgentSessionHandle{
			SessionID: "owner-previous",
			Provider:  llmtypes.CodingProviderSessionHandle{Provider: "CODEX-CLI", NativeSessionID: "codex-thread-1"},
		}
		if accepted := agent.applyAgentSessionHandle(handle); !accepted {
			t.Fatal("provider comparison must match the continuation check's case-insensitive rule")
		}
	})

	t.Run("session-only handle imports identity without native state", func(t *testing.T) {
		agent := testCodexAgentForHandle()
		handle := &AgentSessionHandle{SessionID: "owner-previous"}
		if accepted := agent.applyAgentSessionHandle(handle); !accepted {
			t.Fatal("a session-only handle must still import session identity")
		}
		if agent.sessionID != "owner-previous" {
			t.Fatalf("sessionID = %q, want owner-previous", agent.sessionID)
		}
		if !agent.codingProviderSessionHandle.Empty() {
			t.Fatalf("native handle = %+v, want empty", agent.codingProviderSessionHandle)
		}
	})
}
