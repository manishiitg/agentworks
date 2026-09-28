package store

import "testing"

func TestAPIKeyStoredOnlyAsHash(t *testing.T) {
	s := NewMemoryStore()
	token := "gwk_test_secret"
	s.AddAPIKey(APIKey{ID: "key-1", WorkspaceID: "w1", GroupID: "g1", Token: token})
	if _, raw := s.apiKeys[token]; raw {
		t.Fatal("raw token used as storage key")
	}
	for _, key := range s.apiKeys {
		if key.Token != "" {
			t.Fatal("raw token retained in stored record")
		}
	}
	if key, ok := s.APIKeyByToken(token); !ok || key.ID != "key-1" {
		t.Fatalf("lookup failed: %+v, %v", key, ok)
	}
	if key, ok := s.APIKeyByToken("wrong"); ok {
		t.Fatalf("wrong token found: %+v", key)
	}
}
