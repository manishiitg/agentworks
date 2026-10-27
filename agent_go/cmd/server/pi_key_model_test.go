package server

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// server B 2026-10-08 (PLAT-720): a model picked on a person's own NVIDIA key was refused with
// "model is not offered for engine pi-cli", so the key account's model could never change.
func TestPiAcceptsServiceModelsFromAPersonsKey(t *testing.T) {
	pi := agentprofiles.ProviderOption{ID: "pi-cli", Provider: "pi-cli", Models: []string{"google/gemini-3.8-flash"}}
	for _, id := range []string{"nvidia/z-ai/glm-5.3-flash", "openrouter/nvidia/nemotron-3-super-120b-a12b:free", "google/gemini-3.8-flash"} {
		if !providerOptionOffersModel(pi, id) {
			t.Fatalf("pi refused %q", id)
		}
	}
	for _, id := range []string{"glm-5.3-flash", "/z-ai/glm", "Nvidia/x", "nvidia/a b", "nvidia/$(id)"} {
		if providerOptionOffersModel(pi, id) {
			t.Fatalf("pi accepted malformed %q", id)
		}
	}
	// PLAT-872: the picker offered the account's Auto and DeepSeek, then the server refused them (422).
	cursor := agentprofiles.ProviderOption{ID: "cursor-cli", Provider: "cursor-cli"}
	for _, id := range []string{"auto", "deepseek-v41-flash-1017-max"} {
		if !providerOptionOffersModel(cursor, id) {
			t.Fatalf("cursor refused account model %q", id)
		}
	}
	if providerOptionOffersModel(cursor, "auto $(id)") {
		t.Fatal("cursor accepted a malformed model id")
	}
	other := agentprofiles.ProviderOption{ID: "codex-cli", Provider: "codex-cli", Models: []string{"gpt-6-luna"}}
	if providerOptionOffersModel(other, "nvidia/z-ai/glm-5.3-flash") {
		t.Fatal("a non-Pi engine accepted a model it does not list")
	}
}
