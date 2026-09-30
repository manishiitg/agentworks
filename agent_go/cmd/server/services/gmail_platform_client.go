package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PlatformGoogleClientName is the named OAuth client (gmail_oauth_clients.go) that holds the
// deployment's own Google app: the client an admin stored once for the whole server. People
// sign in to their own Google accounts through it, so nobody uploads a client_secret.json of
// their own. It is a real registry entry (gog reads its clients by name), kept in step with
// the stored app by EnsurePlatformOAuthClient, and the name is reserved for it.
const PlatformGoogleClientName = "platform"

// EnsurePlatformOAuthClient makes the "platform" named client match the deployment's Google
// app. It changes nothing when the stored client already has this id and secret. A different
// client id replaces it, which invalidates connections made through the old one (they were
// signed in under a different app); a rotated secret with the same id keeps every login.
func EnsurePlatformOAuthClient(ctx context.Context, clientID, clientSecret string) (GmailOAuthClient, error) {
	clientID, clientSecret = strings.TrimSpace(clientID), strings.TrimSpace(clientSecret)
	if clientID == "" || clientSecret == "" {
		return GmailOAuthClient{}, fmt.Errorf("the server has no Google app configured")
	}
	if meta, ok := loadGmailOAuthClientMeta(PlatformGoogleClientName); ok && OAuthClientExists(PlatformGoogleClientName) {
		if id, secret, err := GetOAuthClientSecret(PlatformGoogleClientName); err == nil && id == clientID && secret == clientSecret {
			return meta, nil
		}
	}
	raw, err := json.Marshal(map[string]interface{}{"web": map[string]interface{}{
		"client_id":     clientID,
		"client_secret": clientSecret,
		"auth_uri":      "https://accounts.google.com/o/oauth2/auth",
		"token_uri":     "https://oauth2.googleapis.com/token",
	}})
	if err != nil {
		return GmailOAuthClient{}, err
	}
	return CreateOAuthClient(ctx, PlatformGoogleClientName, raw, true)
}

// HasPendingGmailOAuthState reports whether state belongs to a Gmail sign-in in progress, so a
// shared callback URL can hand the redirect to the Gmail completion.
func HasPendingGmailOAuthState(state string) bool {
	gmailOAuthPendingMu.Lock()
	defer gmailOAuthPendingMu.Unlock()
	_, ok := gmailOAuthPending[strings.TrimSpace(state)]
	return ok
}

// SetGogClientStore replaces how a named client is handed to gog (by default it runs gog), and
// returns a function that restores it. It exists so tests in other packages can register a
// client without a gog binary; nothing else should call it.
func SetGogClientStore(store func(ctx context.Context, home, name string, raw []byte) error) (restore func()) {
	previous := storeGogClient
	storeGogClient = store
	return func() { storeGogClient = previous }
}

// StoreGogClientForTest is a store for SetGogClientStore that writes the client where
// GetOAuthClientSecret reads it, the way the real store does after gog accepts it.
func StoreGogClientForTest(ctx context.Context, _ string, name string, raw []byte) error {
	id, secret, err := parseGmailClientSecretJSON(raw)
	if err != nil {
		return err
	}
	path, err := gogClientCredentialsPath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"client_id": id, "client_secret": secret})
	return os.WriteFile(path, body, 0o600)
}
