package mcpagent

import (
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"testing"
)

func TestProviderAccountContinuationCannotCrossAccounts(t *testing.T) {
	a := &Agent{provider: llm.ProviderCodexCLI, modelID: "gpt-test", sessionID: "owner-B", llmConfig: AgentLLMConfiguration{Primary: LLMModel{Provider: "codex-cli", ModelID: "gpt-test", ConnectionID: "account-B"}}}
	foreign := &AgentSessionHandle{SessionID: "owner-A", ConnectionID: "account-A", Provider: llmtypes.CodingProviderSessionHandle{Provider: "codex-cli", NativeSessionID: "thread-A"}}
	a.applyAgentSessionHandle(foreign)
	if a.codexSessionID != "" || a.sessionID != "owner-B" {
		t.Fatal("foreign account's continuation was applied")
	}
	if a.currentAgentSessionHandle().ConnectionID != "account-B" {
		t.Fatal("continuation lost account binding")
	}
	// The same account can resume its own native thread; denying everything
	// would hide a broken account resolver (PLAT-872-19).
	own := &AgentSessionHandle{SessionID: "owner-B", ConnectionID: "account-B", Provider: llmtypes.CodingProviderSessionHandle{Provider: "codex-cli", NativeSessionID: "thread-B"}}
	a.applyAgentSessionHandle(own)
	if a.codexSessionID != "thread-B" || a.sessionID != "owner-B" {
		t.Fatal("same-account native continuation was refused")
	}
}
