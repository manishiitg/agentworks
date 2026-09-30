package services

import (
	"os"
	"testing"
)

func TestSecretsKeyCacheSurvivesEnvClearing(t *testing.T) {
	prev := cachedSecretsKey
	defer func() { cachedSecretsKey = prev }()
	t.Setenv("AUTH_SECRET", "cache-test-secret")
	CacheSecretsKey()
	want := deriveSecretsKey()
	if want == nil {
		t.Fatal("deriveSecretsKey() = nil with AUTH_SECRET set")
	}
	if err := os.Unsetenv("AUTH_SECRET"); err != nil {
		t.Fatal(err)
	}
	if got := deriveSecretsKey(); string(got) != string(want) {
		t.Fatal("deriveSecretsKey() changed after clearing AUTH_SECRET")
	}
}

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
