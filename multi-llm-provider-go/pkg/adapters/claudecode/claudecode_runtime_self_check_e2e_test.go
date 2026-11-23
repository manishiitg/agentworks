package claudecode

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/testcontracts"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestClaudeCodeTmuxRuntimeSelfCheckContract(t *testing.T) {
	skipClaudeInteractiveIntegration(t)
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	workDir := t.TempDir()
	bridgeToken := "CLAUDE_RUNTIME_BRIDGE_" + randomHex(4)

	c := testcontracts.RuntimeSelfCheckCase{
		Provider:     "claude-code",
		SystemToken:  "CLAUDE_SYSTEM_CANARY_" + randomHex(5),
		SkillName:    "runtime-self-check",
		SkillToken:   "CLAUDE_SKILL_CANARY_" + randomHex(5),
		BridgeToken:  bridgeToken,
		BridgeResult: "BRIDGE_TOOL_OK_" + bridgeToken,
	}
	mcpConfig := fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":%q}}}`, writeClaudeInteractiveContractMCPServer(t))

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	opts := []llmtypes.CallOption{
		WithWorkingDir(workDir),
		WithMCPConfig(mcpConfig),
		WithClaudeCodeTools(""),
		WithAllowedTools("mcp__api-bridge__echo_contract"),
		WithEffort("low"),
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
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, testcontracts.RuntimeSelfCheckSlashSkillPrompt(c)),
	}, opts...)
	if err != nil {
		t.Fatalf("GenerateContent runtime self-check skill error = %v", err)
	}
	testcontracts.AssertRuntimeSelfCheckSkillResponse(t, c, testcontracts.FirstChoiceContent(t, skillResp))
}
