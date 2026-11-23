package utils

import "testing"

func TestGetAllModelMetadataExcludesRemovedGeminiCLI(t *testing.T) {
	for _, model := range GetAllModelMetadata() {
		if model != nil && model.Provider == "gemini-cli" {
			t.Fatalf("GetAllModelMetadata() includes removed Gemini CLI model %q", model.ModelID)
		}
	}
}
