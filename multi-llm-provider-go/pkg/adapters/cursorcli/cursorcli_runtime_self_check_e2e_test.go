package cursorcli

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/testcontracts"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestCursorCLIRealRuntimeSelfCheckContract(t *testing.T) {
	requireRealCursorCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCursorCLIInteractiveSessions(context.Background()) })

	adapter := NewCursorCLIAdapter("", "cursor-cli", &MockLogger{})
	sessionPrefix := "cursor-runtime-self-check-" + cursorRandomHex(4)
	bridgeToken := "CURSOR_RUNTIME_BRIDGE_" + cursorRandomHex(4)

	c := testcontracts.RuntimeSelfCheckCase{
		Provider:     "cursor-cli",
		SystemToken:  "CURSOR_SYSTEM_CANARY_" + cursorRandomHex(5),
		SkillName:    "runtime-self-check",
		SkillToken:   "CURSOR_SKILL_CANARY_" + cursorRandomHex(5),
		BridgeToken:  bridgeToken,
		BridgeResult: "BRIDGE_TOOL_OK_" + bridgeToken,
	}
	mcpConfig := fmt.Sprintf(`{"mcpServers":{"api-bridge":{"type":"stdio","command":"node","args":[%q]}}}`, writeCursorTmuxContractMCPServer(t))
	bridgeWorkDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	bridgeOpts := []llmtypes.CallOption{
		WithInteractiveSessionID(sessionPrefix + "-bridge"),
		WithPersistentInteractiveSession(true),
		WithWorkingDir(bridgeWorkDir),
		WithMCPConfig(mcpConfig),
		WithApproveMCPs(),
		WithForce(),
	}
	bridgeResp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "Use only declared MCP tools. Keep the final answer concise."),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, fmt.Sprintf("Call the api-bridge MCP tool named contract_echo_token with token %s. Then reply with MCP_ACCESS=yes and the exact text returned by the tool.", bridgeToken)),
	}, bridgeOpts...)
	if err != nil {
		t.Fatalf("GenerateContent runtime self-check bridge error = %v", err)
	}
	testcontracts.AssertRuntimeSelfCheckBridgeResponse(t, c, testcontracts.FirstChoiceContent(t, bridgeResp))

	systemOpts := []llmtypes.CallOption{
		WithInteractiveSessionID(sessionPrefix + "-system"),
		WithPersistentInteractiveSession(true),
		WithWorkingDir(t.TempDir()),
	}
	systemResp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, testcontracts.RuntimeSelfCheckSystemPrompt(c)),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, testcontracts.RuntimeSelfCheckSystemPromptPrompt(c)),
	}, systemOpts...)
	if err != nil {
		t.Fatalf("GenerateContent runtime self-check system prompt error = %v", err)
	}
	testcontracts.AssertRuntimeSelfCheckSystemPromptResponse(t, c, testcontracts.FirstChoiceContent(t, systemResp))

	skillOpts := []llmtypes.CallOption{
		WithInteractiveSessionID(sessionPrefix + "-skill"),
		WithPersistentInteractiveSession(true),
		WithWorkingDir(t.TempDir()),
		llmtypes.WithAttachedSkills([]*llmtypes.Skill{testcontracts.RuntimeSelfCheckSkill(c.SkillName, c.SkillToken)}),
	}
	skillResp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, testcontracts.RuntimeSelfCheckSystemPrompt(c)),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, testcontracts.RuntimeSelfCheckSkillPrompt(c)),
	}, skillOpts...)
	if err != nil {
		t.Fatalf("GenerateContent runtime self-check skill error = %v", err)
	}
	testcontracts.AssertRuntimeSelfCheckSkillResponse(t, c, testcontracts.FirstChoiceContent(t, skillResp))
}
