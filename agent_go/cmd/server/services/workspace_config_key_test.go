package services

import "testing"

func TestDecryptNeedsAnAuthSecretAndHasNoPublicDefault(t *testing.T) {
	t.Setenv("AUTH_SECRET", "")
	if key := deriveSecretsKey(); key != nil {
		t.Fatalf("no AUTH_SECRET must give no key, got %d bytes", len(key))
	}
	if _, err := decryptWithAAD(make([]byte, 64), "aad"); err == nil {
		t.Fatal("decrypt must fail without AUTH_SECRET")
	}
	t.Setenv("AUTH_SECRET", "  a-real-secret  ")
	a := deriveSecretsKey()
	t.Setenv("AUTH_SECRET", "a-real-secret")
	if b := deriveSecretsKey(); a == nil || string(a) != string(b) {
		t.Fatal("the key must ignore surrounding whitespace, like the server's own reader")
	}
}
