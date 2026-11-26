package server

import (
	"encoding/json"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// The Steps page prints what a run would use (python_agent.go): a call's model beats
// its tier, no setting means the High tier, a call's reasoning replaces the saved one,
// and anything unresolvable shows nothing rather than a guess.
func TestOutlineAgentModel(t *testing.T) {
	tier := func(model, effort string) *workflowtypes.AgentLLMConfig {
		return &workflowtypes.AgentLLMConfig{Provider: "claude-code", ModelID: model, Options: map[string]interface{}{"reasoning_effort": effort}}
	}
	tiers := &workflowtypes.TieredLLMConfig{Tier1: tier("big", "high"), Tier2: tier("mid", "medium"), Tier3: nil}
	cases := []struct {
		name                   string
		model, tier, reasoning string
		want                   outlineAgent
		unresolved             bool
	}{
		{name: "default is High", want: outlineAgent{"Claude", "big", "high", "default"}},
		{name: "tier", tier: "medium", want: outlineAgent{"Claude", "mid", "medium", "call"}},
		{name: "reasoning only", reasoning: "low", want: outlineAgent{"Claude", "big", "low", "call"}},
		{name: "model", model: "cursor-cli/auto", want: outlineAgent{"Cursor", "auto", "", "call"}},
		{name: "tier with no model", tier: "low", unresolved: true},
		{name: "unknown tier", tier: "huge", unresolved: true},
		{name: "model without provider", model: "auto", unresolved: true},
	}
	for _, c := range cases {
		got := resolveOutlineAgent(tiers, c.model, c.tier, c.reasoning)
		if c.unresolved != (got == nil) || got != nil && *got != c.want {
			t.Errorf("%s: got %+v, want %+v (unresolved=%v)", c.name, got, c.want, c.unresolved)
		}
	}
	if resolveOutlineAgent(nil, "", "", "") != nil {
		t.Error("no saved model settings must resolve to nothing")
	}
}

// Codex1730 P2: null records an override that execution resolves at run time;
// only an absent setting may inherit the saved High tier.
func TestOutlineDynamicOverrideDoesNotInventDefaultModel(t *testing.T) {
	config := &workflowtypes.PresetLLMConfig{Mode: workflowtypes.LLMConfigModeExplicit, TieredConfig: &workflowtypes.TieredLLMConfig{Tier1: &workflowtypes.AgentLLMConfig{Provider: "example", ModelID: "default"}}}
	raw, err := addOutlineModels(json.RawMessage(`{"nodes":[{"type":"agent","settings":{"model":null}},{"type":"agent","settings":{"tier":null}},{"type":"agent"}]}`), config)
	if err != nil {
		t.Fatal(err)
	}
	var graph struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err = json.Unmarshal(raw, &graph); err != nil {
		t.Fatal(err)
	}
	for _, node := range graph.Nodes[:2] {
		if node["agent"] != nil {
			t.Fatalf("dynamic override became a default: %s", raw)
		}
	}
	if graph.Nodes[2]["agent"] == nil {
		t.Fatalf("absent override lost the default: %s", raw)
	}
}
