package sparkquillproduct

import "testing"

// Every provider option must declare a reasoning_effort — deliberate, not
// left to whatever a provider defaults to — and one of its own declared
// reasoning_efforts choices, so the composer's picker always starts on a
// value it can actually select. The specific defaults are chosen per
// profile and provider (the child uses high effort on Codex, while the parent
// uses medium; Claude Code uses high for the parent and medium for the child),
// not a single fixed value across the board.
func TestEveryProviderOptionDeclaresAnOwnReasoningEffort(t *testing.T) {
	profiles := BuiltinAgentProfiles()
	if len(profiles) == 0 {
		t.Fatal("no built-in profiles")
	}
	for _, p := range profiles {
		if len(p.Runtime.ProviderOptions) == 0 {
			t.Fatalf("profile %s declares no provider options", p.ID)
		}
		for _, o := range p.Runtime.ProviderOptions {
			effort, _ := o.Options["reasoning_effort"].(string)
			if effort == "" {
				t.Fatalf("profile %s option %s: no reasoning_effort declared", p.ID, o.ID)
			}
			found := false
			for _, allowed := range o.ReasoningEfforts {
				if allowed == effort {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("profile %s option %s: reasoning_effort %q is not one of its own declared reasoning_efforts %v", p.ID, o.ID, effort, o.ReasoningEfforts)
			}
		}
	}
}

// The parent defaults to GPT-6.1 Sol and the child to GPT-6 Luna on Codex.
// Claude Code remains a
// selectable engine with Sonnet 5.5 as its default and Opus 5.5 as an option.
func TestSparkQuillDefaultModelsAndReasoningEfforts(t *testing.T) {
	profiles := BuiltinAgentProfiles()
	find := func(profileID, optionID string) (modelID, effort string) {
		for _, p := range profiles {
			if p.ID != profileID {
				continue
			}
			for _, o := range p.Runtime.ProviderOptions {
				if o.ID == optionID {
					e, _ := o.Options["reasoning_effort"].(string)
					return o.ModelID, e
				}
			}
		}
		t.Fatalf("no provider option %q on profile %q", optionID, profileID)
		return "", ""
	}
	cases := []struct {
		profileID, optionID, wantModel, wantEffort string
	}{
		{"sparkquill", "claude-code", "claude-sonnet-5-5", "high"},
		{"sparkquill", "codex-cli", "gpt-6.1-sol", "medium"},
		{"sparkquill", "agy-cli", "gemini-3.8-flash-high", "high"},
		{"sparkquill-child", "claude-code", "claude-sonnet-5-5", "medium"},
		{"sparkquill-child", "codex-cli", "gpt-6-luna", "high"},
		{"sparkquill-child", "agy-cli", "gemini-3.8-flash-high", "high"},
	}
	for _, c := range cases {
		gotModel, gotEffort := find(c.profileID, c.optionID)
		if gotModel != c.wantModel || gotEffort != c.wantEffort {
			t.Fatalf("%s/%s: model=%q effort=%q, want model=%q effort=%q", c.profileID, c.optionID, gotModel, gotEffort, c.wantModel, c.wantEffort)
		}
	}
	for _, p := range profiles {
		for _, o := range p.Runtime.ProviderOptions {
			if o.Default != (o.ID == "codex-cli") {
				t.Errorf("%s/%s: default=%t, want Codex as the only default", p.ID, o.ID, o.Default)
			}
			if len(o.Models) != 0 {
				t.Errorf("%s/%s narrows the shared model catalog: %v", p.ID, o.ID, o.Models)
			}
		}
	}
}
