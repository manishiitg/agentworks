package codexcli

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/testcontracts"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestCodexCLIRealRuntimeSelfCheckContract(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	ownerSessionID := "codex-runtime-self-check-" + codexRandomHex(4)
	workDir := t.TempDir()
	bridgeToken := "CODEX_RUNTIME_BRIDGE_" + codexRandomHex(4)

	c := testcontracts.RuntimeSelfCheckCase{
		Provider:     "codex-cli",
		SystemToken:  "CODEX_SYSTEM_CANARY_" + codexRandomHex(5),
		SkillName:    "runtime-self-check",
		SkillToken:   "CODEX_SKILL_CANARY_" + codexRandomHex(5),
		BridgeToken:  bridgeToken,
		BridgeResult: "BRIDGE_TOOL_OK_" + bridgeToken,
	}
	mcpCommandOverride, err := codexStringConfigOverride("mcp_servers.api-bridge.command", writeCodexContractMCPServer(t))
	if err != nil {
		t.Fatalf("build MCP command override: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	opts := []llmtypes.CallOption{
		WithInteractiveSessionID(ownerSessionID),
		WithPersistentInteractiveSession(true),
		WithProjectDirID(workDir),
		WithDisableShellTool(),
		WithApprovalPolicy("never"),
		WithReasoningEffort("low"),
		WithConfigOverrides([]string{mcpCommandOverride}),
		llmtypes.WithAttachedSkills([]*llmtypes.Skill{testcontracts.RuntimeSelfCheckSkill(c.SkillName, c.SkillToken)}),
	}

	bridgeResp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, testcontracts.RuntimeSelfCheckSystemPrompt(c)),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, testcontracts.RuntimeSelfCheckBridgePrompt(c,
			fmt.Sprintf("Call the api-bridge echo_contract MCP tool with token %s.", bridgeToken),
		)),
	}, opts...)
	if err != nil {
		t.Fatalf("GenerateContent runtime self-check bridge error = %v", err)
	}
	testcontracts.AssertRuntimeSelfCheckBridgeResponse(t, c, testcontracts.FirstChoiceContent(t, bridgeResp))

	systemResp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, testcontracts.RuntimeSelfCheckSystemPrompt(c)),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, testcontracts.RuntimeSelfCheckSystemPromptPrompt(c)),
	}, opts...)
	if err != nil {
		t.Fatalf("GenerateContent runtime self-check system prompt error = %v", err)
	}
	testcontracts.AssertRuntimeSelfCheckSystemPromptResponse(t, c, testcontracts.FirstChoiceContent(t, systemResp))

	skillResp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, testcontracts.RuntimeSelfCheckSystemPrompt(c)),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, testcontracts.RuntimeSelfCheckSkillPrompt(c)),
	}, opts...)
	if err != nil {
		t.Fatalf("GenerateContent runtime self-check skill error = %v", err)
	}
	testcontracts.AssertRuntimeSelfCheckSkillResponse(t, c, testcontracts.FirstChoiceContent(t, skillResp))
}
