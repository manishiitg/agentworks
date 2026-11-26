package picli

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/testcontracts"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestPiCLIRealRuntimeSelfCheckContract(t *testing.T) {
	requireRealPiCLIContractE2E(t)
	t.Cleanup(func() { _ = CleanupPiCLIInteractiveSessions(context.Background()) })

	adapter := newRealPiCLIAdapter(t)
	sessionPrefix := "pi-runtime-self-check-" + piRandomHex(4)
	bridgeToken := "PI_RUNTIME_BRIDGE_" + piRandomHex(4)

	c := testcontracts.RuntimeSelfCheckCase{
		Provider:     "pi-cli",
		SystemToken:  "PI_SYSTEM_CANARY_" + piRandomHex(5),
		SkillName:    "runtime-self-check",
		SkillToken:   "PI_SKILL_CANARY_" + piRandomHex(5),
		BridgeToken:  bridgeToken,
		BridgeResult: "PI_RUNTIME_OK_" + bridgeToken,
	}
	mcpConfig := fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":"node","args":[%q]}}}`, writePiEchoMCPServer(t, "PI_RUNTIME_OK"))

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	bridgeOpts := []llmtypes.CallOption{
		WithInteractiveSessionID(sessionPrefix + "-bridge"),
		WithPersistentInteractiveSession(true),
		WithWorkingDir(piLiveWorkDir(t)),
		WithMCPConfig(mcpConfig),
		WithBridgeOnlyTools(true),
	}
	bridgeResp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, testcontracts.RuntimeSelfCheckSystemPrompt(c)),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, testcontracts.RuntimeSelfCheckBridgePrompt(c,
			fmt.Sprintf("Call the api-bridge MCP tool echo_contract with token %s.", bridgeToken),
		)),
	}, bridgeOpts...)
	if err != nil {
		t.Fatalf("GenerateContent runtime self-check bridge error = %v", err)
	}
	testcontracts.AssertRuntimeSelfCheckBridgeResponse(t, c, testcontracts.FirstChoiceContent(t, bridgeResp))

	systemOpts := []llmtypes.CallOption{
		WithInteractiveSessionID(sessionPrefix + "-system"),
		WithPersistentInteractiveSession(true),
		WithWorkingDir(piLiveWorkDir(t)),
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
		WithWorkingDir(piLiveWorkDir(t)),
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
