package store

import "testing"

func TestConnectorIdentityCannotOverwriteAnotherProvider(t *testing.T) {
	s := NewMemoryStore()
	original := Connector{ID: "same", WorkspaceID: "w", Provider: "notion", Label: "Engineering", OAuthCredentialID: "same"}
	if !s.AddConnectorUnique(original) {
		t.Fatal("initial insert failed")
	}
	if s.AddConnectorUnique(Connector{ID: "same", WorkspaceID: "w", Provider: "sentry", Label: "different"}) {
		t.Fatal("connector identity reused by a different provider")
	}
	got, _ := s.GetConnector("same")
	if got != original {
		t.Fatal("original credentials/metadata were replaced")
	}
}
