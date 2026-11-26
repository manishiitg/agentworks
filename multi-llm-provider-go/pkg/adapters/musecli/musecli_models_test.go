package musecli

import (
	"strings"
	"testing"
)

func TestMuseWonderModelsPublishedWithEfforts(t *testing.T) {
	models := GetAllMuseCLIModels()
	if len(models) < 2 {
		t.Fatalf("models = %d, want Wonder entries", len(models))
	}
	if models[0].ModelID != "muse-wonder-1.0" || models[0].ModelName != "Muse Wonder" {
		t.Fatalf("first model = %q/%q, want Muse Wonder", models[0].ModelID, models[0].ModelName)
	}
	if models[1].ModelID != "muse-wonder-full-1.0" || models[1].ModelName != "Muse Wonder Full (Muse Code subscribers)" {
		t.Fatalf("second model = %q/%q, want Muse Wonder Full label", models[1].ModelID, models[1].ModelName)
	}
	// Muse Spark 1.2 is retired: it must not be listed or priced.
	for _, model := range models {
		if strings.HasPrefix(model.ModelID, "muse-spark-1.2") {
			t.Fatalf("retired model still listed: %q", model.ModelID)
		}
	}
	wantEfforts := []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}
	for _, model := range models[:2] {
		if len(model.ReasoningEffortLevels) != len(wantEfforts) {
			t.Fatalf("%s effort levels = %v", model.ModelID, model.ReasoningEffortLevels)
		}
		for i, want := range wantEfforts {
			if model.ReasoningEffortLevels[i] != want {
				t.Fatalf("%s effort[%d] = %q, want %q", model.ModelID, i, model.ReasoningEffortLevels[i], want)
			}
		}
	}
}

// Ember is listed with the catalog's context window, its efforts and Meta's
// published standard price.
func TestMuseEmberModelListed(t *testing.T) {
	var found bool
	for _, model := range GetAllMuseCLIModels() {
		if model.ModelID != "muse-ember-1.0" {
			continue
		}
		found = true
		if model.ContextWindow != 1007997 || model.InputCostPer1MTokens != 0.10 || model.OutputCostPer1MTokens != 0.50 || len(model.ReasoningEffortLevels) == 0 {
			t.Fatalf("ember metadata = %+v", model)
		}
	}
	if !found {
		t.Fatal("muse-ember-1.0 is not listed")
	}
}
