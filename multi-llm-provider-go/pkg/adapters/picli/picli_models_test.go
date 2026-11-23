package picli

import "testing"

func TestGetAllPiCLIModelsIncludesCuratedChineseModels(t *testing.T) {
	models := GetAllPiCLIModels()
	byID := make(map[string]string, len(models))
	for _, model := range models {
		byID[model.ModelID] = model.ModelName
		if !model.SupportsReasoningEffort {
			t.Fatalf("model %q must expose Pi thinking levels", model.ModelID)
		}
	}

	for id, wantName := range map[string]string{
		ModelGLM53:      "Pi CLI (GLM 5.3)",
		ModelGLM53Flash: "Pi CLI (GLM 5.3 Flash)",
		ModelKimiK3:     "Pi CLI (Kimi K3)",
	} {
		if got := byID[id]; got != wantName {
			t.Fatalf("model %q name = %q, want %q; models = %#v", id, got, wantName, byID)
		}
	}
}
