package codexcli

import "testing"

func TestCodexCatalogOmitsOlderModelsButPreservesSavedSessionMetadata(t *testing.T) {
	models := GetAllCodexCLIModels()
	for _, removed := range []string{"gpt-5.5", "gpt-5.4"} {
		for _, model := range models {
			if model.ModelID == removed {
				t.Fatalf("selectable catalog includes removed model %s", removed)
			}
		}
		meta, err := (&CodexCLIAdapter{}).GetModelMetadata(removed)
		if err != nil || meta == nil {
			t.Fatalf("saved session metadata for %s is unavailable: %v", removed, err)
		}
	}
}
