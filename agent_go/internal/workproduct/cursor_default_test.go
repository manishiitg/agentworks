package workproduct

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/knowledgebaseproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// Crew and Brain chats switched to Cursor start on Auto, the model every Cursor plan has. Composer stays
// selectable, but a free account refuses it, so a fork or new tab on Cursor failed before its first reply
// (PLAT-872).
func TestBuiltinProductsStartCursorOnAuto(t *testing.T) {
	for name, profile := range map[string]agentprofiles.Profile{"crew": BuiltinAgentProfile(), "brain": knowledgebaseproduct.BuiltinAgentProfile()} {
		found := false
		for _, option := range profile.Runtime.ProviderOptions {
			if option.Provider != "cursor-cli" {
				continue
			}
			found = true
			if option.ModelID != "auto" {
				t.Fatalf("%s: Cursor starts on %q, want auto", name, option.ModelID)
			}
		}
		if !found {
			t.Fatalf("%s: no Cursor option", name)
		}
	}
}
