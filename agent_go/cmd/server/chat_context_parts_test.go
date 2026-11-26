package server

import (
	"testing"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

// The parts must add up to the real total, and must never be shown when they would exceed it.
func TestChatContextPartsAddUpToTheReportedTotal(t *testing.T) {
	sizes := mcpagent.PromptPartSizes{Instructions: 4000, Added: 800, Runtime: 1200, Skills: 400, Tools: 8000}
	parts := chatContextParts(sizes, 20000)
	sum := 0
	for _, part := range parts {
		sum += part.Tokens
	}
	if sum != 20000 || parts[0].Key != "instructions" || parts[0].Tokens != 1500 || parts[len(parts)-1].Key != "messages" {
		t.Fatalf("parts = %#v (sum %d), want instructions 1500 and a total of 20000", parts, sum)
	}
	if got := chatContextParts(sizes, 3000); got != nil {
		t.Fatalf("parts bigger than the total must be hidden, got %#v", got)
	}
	if got := chatContextParts(mcpagent.PromptPartSizes{}, 5000); got != nil {
		t.Fatalf("unknown sizes must show nothing, got %#v", got)
	}
}

// A Claude Code chat reports one total: its transcript's parts are scaled to that total, add up to it, and
// instructions sent again in messages are called out (real case: 30K of a 209K chat was two resent copies).
func TestNativeContextPartsAddUpAndCallOutResentInstructions(t *testing.T) {
	b := claudecode.ContextBreakdown{Total: 10000, Setup: 3000, Skills: 500, InstructionFiles: 500, Resent: 2000, ResentCount: 2, Conversation: 4000}
	parts := nativeContextParts(b, claudeCodeApp, 1000, 20000)
	sum, resent := 0, chatContextPart{}
	for _, part := range parts {
		sum += part.Tokens
		if part.Key == "resent" {
			resent = part
		}
	}
	if sum != 20000 || parts[0].Key != "instructions" || parts[0].Tokens != 2000 || resent.Tokens != 4000 || resent.Note == "" {
		t.Fatalf("parts = %#v (sum %d), want a total of 20000, our instructions first and the resent copies noted", parts, sum)
	}
}
