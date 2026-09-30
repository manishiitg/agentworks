package services

import (
	"context"
	"testing"
)

// The deployment's Google app is registered as the reserved "platform" named client and kept in
// step with the stored app: unchanged when nothing changed, a rotated secret keeps the same
// client id (so logins survive), and an empty app is refused.
func TestEnsurePlatformOAuthClientTracksTheStoredApp(t *testing.T) {
	t.Setenv("GMAIL_OAUTH_CLIENTS_DIR", t.TempDir())
	isolateGogClientStore(t)
	ctx := context.Background()

	if _, err := EnsurePlatformOAuthClient(ctx, "", ""); err == nil {
		t.Fatal("an empty Google app was accepted")
	}
	first, err := EnsurePlatformOAuthClient(ctx, "cid-1.apps.googleusercontent.com", "secret-1")
	if err != nil {
		t.Fatal(err)
	}
	if first.Name != PlatformGoogleClientName || first.ClientID != "cid-1.apps.googleusercontent.com" {
		t.Fatalf("client = %+v", first)
	}
	id, secret, err := GetOAuthClientSecret(PlatformGoogleClientName)
	if err != nil || id != "cid-1.apps.googleusercontent.com" || secret != "secret-1" {
		t.Fatalf("stored = %q %q %v", id, secret, err)
	}
	// Nothing changed: same entry back, no rewrite.
	again, err := EnsurePlatformOAuthClient(ctx, "cid-1.apps.googleusercontent.com", "secret-1")
	if err != nil || !again.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("unchanged app rewrote the client: %+v %v", again, err)
	}
	// A rotated secret is picked up.
	if _, err := EnsurePlatformOAuthClient(ctx, "cid-1.apps.googleusercontent.com", "secret-2"); err != nil {
		t.Fatal(err)
	}
	if _, secret, _ := GetOAuthClientSecret(PlatformGoogleClientName); secret != "secret-2" {
		t.Fatalf("rotated secret not stored: %q", secret)
	}
}

// A Gmail sign-in in progress is recognised by its state, so a shared callback can hand it over.
func TestHasPendingGmailOAuthState(t *testing.T) {
	t.Setenv("GMAIL_OAUTH_CLIENTS_DIR", t.TempDir())
	isolateGogClientStore(t)
	if HasPendingGmailOAuthState("nope") || HasPendingGmailOAuthState("") {
		t.Fatal("an unknown state was reported pending")
	}
	if _, err := EnsurePlatformOAuthClient(context.Background(), "cid.apps.googleusercontent.com", "s"); err != nil {
		t.Fatal(err)
	}
	authURL, err := BeginGmailOAuth("conn-1", PlatformGoogleClientName, "https://app.example/api/oauth/callback", true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	state := authURLState(t, authURL)
	if !HasPendingGmailOAuthState(state) {
		t.Fatalf("a started sign-in (state %q) was not reported pending", state)
	}
	if want := "redirect_uri=https%3A%2F%2Fapp.example%2Fapi%2Foauth%2Fcallback"; !contains(authURL, want) {
		t.Fatalf("auth url does not return through the shared callback: %s", authURL)
	}
}

func authURLState(t *testing.T, authURL string) string {
	t.Helper()
	const key = "state="
	i := indexOf(authURL, key)
	if i < 0 {
		t.Fatalf("no state in %s", authURL)
	}
	rest := authURL[i+len(key):]
	if j := indexOf(rest, "&"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

func contains(s, sub string) bool { return indexOf(s, sub) >= 0 }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
