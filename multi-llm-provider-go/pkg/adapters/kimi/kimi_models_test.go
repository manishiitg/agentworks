package kimi

import "testing"

func TestGetDefaultVisibleKimiModelIDs(t *testing.T) {
	models := GetDefaultVisibleKimiModelIDs()
	want := []string{ModelKimiK26, ModelKimiK27Code}
	if len(models) != len(want) {
		t.Fatalf("len(models) = %d, want %d", len(models), len(want))
	}
	for i, id := range want {
		if models[i] != id {
			t.Fatalf("models[%d] = %q, want %q", i, models[i], id)
		}
	}
}

func TestGetKimiModelMetadataK27Code(t *testing.T) {
	meta, err := GetKimiModelMetadata(ModelKimiK27Code)
	if err != nil {
		t.Fatalf("GetKimiModelMetadata returned error: %v", err)
	}
	if meta.ModelID != ModelKimiK27Code {
		t.Fatalf("ModelID = %q, want %q", meta.ModelID, ModelKimiK27Code)
	}
	if !meta.SupportsToolCalls {
		t.Fatal("SupportsToolCalls = false, want true")
	}
}

func TestGetKimiModelMetadataVision(t *testing.T) {
	meta, err := GetKimiModelMetadata(ModelKimiK26)
	if err != nil {
		t.Fatalf("GetKimiModelMetadata returned error: %v", err)
	}
	if meta.Provider != "kimi" {
		t.Fatalf("Provider = %q, want kimi", meta.Provider)
	}
	if meta.ModelID != ModelKimiK26 {
		t.Fatalf("ModelID = %q, want %q", meta.ModelID, ModelKimiK26)
	}
	if !meta.SupportsJSONMode {
		t.Fatal("SupportsJSONMode = false, want true")
	}
}

func TestGetKimiModelMetadataDefaultsToVision(t *testing.T) {
	meta, err := GetKimiModelMetadata("")
	if err != nil {
		t.Fatalf("GetKimiModelMetadata returned error: %v", err)
	}
	if meta.ModelID != ModelKimiK26 {
		t.Fatalf("ModelID = %q, want %q", meta.ModelID, ModelKimiK26)
	}
}

func TestGetKimiModelMetadataRejectsKimiCode(t *testing.T) {
	_, err := GetKimiModelMetadata(ModelKimiCode)
	if err == nil {
		t.Fatal("GetKimiModelMetadata returned nil error for removed kimi-code model")
	}
}
