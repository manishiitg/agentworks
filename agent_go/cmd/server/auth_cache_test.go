package server

import (
	"os"
	"testing"
)

// After startup caching + clearing, auth keeps working from memory while the
// env var stays empty for child processes.
func TestAuthSecretCacheSurvivesEnvClearing(t *testing.T) {
	prev := startupAuthSecret
	defer func() { startupAuthSecret = prev }()
	t.Setenv("AUTH_SECRET", "  cache-test-secret  ")
	InitAuthSecretCache()
	ClearAuthSecretFromEnv()
	if got := string(GetAuthSecret()); got != "cache-test-secret" {
		t.Fatalf("GetAuthSecret() = %q, want trimmed cached secret", got)
	}
	if err := ValidateConfiguredAuthSecret(); err != nil {
		t.Fatalf("ValidateConfiguredAuthSecret() = %v, want nil", err)
	}
	if _, ok := os.LookupEnv("AUTH_SECRET"); ok {
		t.Fatal("AUTH_SECRET still set after clearing")
	}
}

// A live env var wins over the startup cache (tests, CLI commands).
func TestAuthSecretPrefersLiveEnv(t *testing.T) {
	prev := startupAuthSecret
	defer func() { startupAuthSecret = prev }()
	t.Setenv("AUTH_SECRET", "first-secret")
	InitAuthSecretCache()
	t.Setenv("AUTH_SECRET", "second-secret")
	if got := string(GetAuthSecret()); got != "second-secret" {
		t.Fatalf("GetAuthSecret() = %q, want live env value", got)
	}
}
