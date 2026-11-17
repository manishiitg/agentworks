package codingagentmodels

import "testing"

func TestListPiIncludesCurrentCuratedModelsAndDynamicHint(t *testing.T) {
	catalog, err := List("pi-cli")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !catalog.AcceptsCustomID || catalog.LiveListCommand != "pi --list-models" {
		t.Fatalf("catalog = %#v", catalog)
	}
	want := map[string]bool{
		"google/gemini-3.8-flash":       false,
		"google/gemini-3.5-flash-lite":  false,
		"google/gemini-3.1-pro-preview": false,
		"minimax/MiniMax-M3":            false,
		"zai/glm-5.3":                   false,
		"moonshotai/kimi-k3":            false,
	}
	for _, model := range catalog.Models {
		if _, ok := want[model.ID]; ok {
			want[model.ID] = true
		}
	}
	for model, found := range want {
		if !found {
			t.Errorf("Pi catalog is missing %q", model)
		}
	}
}

func TestListRejectsUnknownProvider(t *testing.T) {
	if _, err := List("missing"); err == nil {
		t.Fatal("List() error = nil")
	}
}

func TestListIncludesNewCodingAgentModels(t *testing.T) {
	for provider, wantIDs := range map[string][]string{
		"codex-cli":   {"gpt-6.2-sol", "gpt-6.2-astra", "gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna"},
		"claude-code": {"claude-opus-5-5"},
		"cursor-cli":  {"grok-4.7"},
	} {
		catalog, err := List(provider)
		if err != nil {
			t.Fatalf("List(%q): %v", provider, err)
		}
		for _, wantID := range wantIDs {
			found := false
			for _, model := range catalog.Models {
				if model.ID == wantID {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s catalog is missing %q", provider, wantID)
			}
		}
	}
}

func TestListOmitsRetiredCodingAgentModels(t *testing.T) {
	for provider, retired := range map[string][]string{
		"codex-cli":   {"gpt-5.6-sol", "gpt-5.6-luna"},
		"claude-code": {"claude-opus-5"},
		"cursor-cli":  {"grok-4.6"},
		"pi-cli":      {"xai/grok-4.6"},
	} {
		catalog, err := List(provider)
		if err != nil {
			t.Fatalf("List(%q): %v", provider, err)
		}
		for _, model := range catalog.Models {
			for _, id := range retired {
				if model.ID == id {
					t.Errorf("%s catalog still lists retired model %q", provider, id)
				}
			}
		}
	}
}
