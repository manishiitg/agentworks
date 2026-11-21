package main

import (
	"testing"

	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// The parts must add up to the real total, and must never be shown when they would exceed it.
func TestChatContextPartsAddUpToTheReportedTotal(t *testing.T) {
	sizes := mcpagent.PromptPartSizes{Instructions: 4000, Added: 800, Runtime: 1200, Skills: 400, Tools: 8000}
	parts := chatContextParts(sizes, 20000)
	sum := 0
	for _, part := range parts {
		sum += part.Tokens
	}
	if sum != 20000 || parts[0].Name != "Instructions" || parts[0].Tokens != 1500 || parts[len(parts)-1].Name != "Conversation" {
		t.Fatalf("parts = %#v (sum %d), want instructions 1500 and a total of 20000", parts, sum)
	}
	if got := chatContextParts(sizes, 3000); got != nil {
		t.Fatalf("parts bigger than the total must be hidden, got %#v", got)
	}
	if got := chatContextParts(mcpagent.PromptPartSizes{}, 5000); got != nil {
		t.Fatalf("unknown sizes must show nothing, got %#v", got)
	}
}
