package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMergeStoredProviderKeyValuesIgnoresMaskedIncomingKeys(t *testing.T) {
	maskedIncoming := maskedProviderKeyPrefix + "cret"
	existing := &StoredProviderKeys{
		OpenAI: "openai-existing-secret",
		PiProviderKeys: map[string]string{
			"zai": "zai-existing-secret",
		},
		Azure: &StoredAzureConfig{
			Endpoint: "https://old.openai.azure.com",
			APIKey:   "azure-existing-secret",
		},
	}
	incoming := &StoredProviderKeys{
		OpenAI: maskedIncoming,
		PiProviderKeys: map[string]string{
			"zai": maskedIncoming,
		},
		Azure: &StoredAzureConfig{
			Endpoint: "https://new.openai.azure.com",
			APIKey:   maskedIncoming,
		},
	}

	merged := mergeStoredProviderKeyValues(existing, incoming)

	if merged.OpenAI != "openai-existing-secret" {
		t.Fatalf("OpenAI = %q, want existing secret", merged.OpenAI)
	}
	if merged.PiProviderKeys["zai"] != "zai-existing-secret" {
		t.Fatalf("PiProviderKeys[zai] = %q, want existing secret", merged.PiProviderKeys["zai"])
	}
	if merged.Azure.APIKey != "azure-existing-secret" {
		t.Fatalf("Azure APIKey = %q, want existing secret", merged.Azure.APIKey)
	}
	if merged.Azure.Endpoint != "https://new.openai.azure.com" {
		t.Fatalf("Azure Endpoint = %q, want updated endpoint", merged.Azure.Endpoint)
	}
}

func TestMaskStoredProviderKeysMasksSecretsWithLastFour(t *testing.T) {
	keys := &StoredProviderKeys{
		OpenAI: "sk-openai-123456",
		PiProviderKeys: map[string]string{
			"zai": "zai-secret-7890",
		},
		Azure: &StoredAzureConfig{
			Endpoint: "https://example.openai.azure.com",
			APIKey:   "azure-secret-3456",
		},
	}

	masked := maskStoredProviderKeys(keys)

	if masked.OpenAI != maskedProviderKeyPrefix+"3456" {
		t.Fatalf("masked OpenAI = %q", masked.OpenAI)
	}
	if masked.PiProviderKeys["zai"] != maskedProviderKeyPrefix+"7890" {
		t.Fatalf("masked Pi key = %q", masked.PiProviderKeys["zai"])
	}
	if masked.Azure.APIKey != maskedProviderKeyPrefix+"3456" {
		t.Fatalf("masked Azure key = %q", masked.Azure.APIKey)
	}
	if masked.Azure.Endpoint != keys.Azure.Endpoint {
		t.Fatalf("Azure endpoint changed: %q", masked.Azure.Endpoint)
	}
	if keys.OpenAI != "sk-openai-123456" {
		t.Fatalf("masking mutated original key: %q", keys.OpenAI)
	}
}

func TestSaveProviderKeysDeletesEmptyStore(t *testing.T) {
	var called bool
	var method string
	var requestPath string
	var confirm string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		method = r.Method
		requestPath = r.URL.Path
		confirm = r.URL.Query().Get("confirm")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	t.Setenv("WORKSPACE_API_URL", server.URL)

	if err := SaveProviderKeys(context.Background(), &StoredProviderKeys{}); err != nil {
		t.Fatalf("SaveProviderKeys() error = %v", err)
	}
	if !called {
		t.Fatal("expected empty provider key store to delete provider keys file")
	}
	if method != http.MethodDelete {
		t.Fatalf("method = %s, want DELETE", method)
	}
	if requestPath != "/api/documents/config/provider-api-keys.json" {
		t.Fatalf("path = %s, want /api/documents/config/provider-api-keys.json", requestPath)
	}
	if confirm != "true" {
		t.Fatalf("confirm = %q, want true", confirm)
	}
}
