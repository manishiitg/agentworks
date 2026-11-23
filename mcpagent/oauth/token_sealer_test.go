package oauth

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

type xorSealer struct{ dir string }

func (s xorSealer) Handles(path string) bool { return strings.HasPrefix(path, s.dir) }
func (xorSealer) Seal(_ string, b []byte) ([]byte, error) {
	out := append([]byte("SEALED:"), b...)
	for i := 7; i < len(out); i++ {
		out[i] ^= 0x5a
	}
	return out, nil
}
func (xorSealer) Open(_ string, b []byte) ([]byte, error) {
	out := append([]byte(nil), b[7:]...)
	for i := range out {
		out[i] ^= 0x5a
	}
	return out, nil
}

// A sealer claims only its own paths: those are never plaintext on disk and
// still load; every other token file keeps its format.
func TestTokenSealerAppliesOnlyToItsPaths(t *testing.T) {
	sealed, plain := t.TempDir(), t.TempDir()
	SetTokenSealer(xorSealer{dir: sealed})
	t.Cleanup(func() { SetTokenSealer(nil) })
	token := &oauth2.Token{AccessToken: "secret-access", RefreshToken: "secret-refresh"}
	for _, dir := range []string{sealed, plain} {
		store := NewTokenStore(filepath.Join(dir, "srv.json"))
		if err := store.Save(token); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(filepath.Join(dir, "srv.json")) //nolint:gosec // test temp dir
		if dir == sealed && bytes.Contains(raw, []byte("secret-access")) {
			t.Fatal("sealed token stored in plaintext")
		}
		if dir == plain && !bytes.Contains(raw, []byte("secret-access")) {
			t.Fatal("an unclaimed token file changed format")
		}
		loaded, err := store.Load()
		if err != nil || loaded.AccessToken != "secret-access" {
			t.Fatalf("%s: load = %+v %v", dir, loaded, err)
		}
	}
}

func TestExtraAuthParamsReachTheAuthorizationURL(t *testing.T) {
	cfg := &OAuthConfig{ClientID: "c", AuthURL: "https://accounts.example.com/auth"}
	cfg.TokenURL = "https://accounts.example.com/token"
	cfg.RedirectURL = "https://app.example.com/cb"
	cfg.ExtraAuthParams = map[string]string{"access_type": "offline", "prompt": "consent", "state": "evil", "Redirect_URI": "https://evil.example.com"}
	m := NewManager(cfg, nil)
	state, authURL, err := m.GenerateAuthURL()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(authURL, "access_type=offline") || !strings.Contains(authURL, "prompt=consent") || strings.Contains(authURL, "state=evil") || strings.Contains(authURL, "evil.example.com") || !strings.Contains(authURL, "state="+state) {
		t.Fatalf("auth URL = %s", authURL)
	}
}

// A config holding only a reference to a credential file gets its client
// secret from that file, sealed or not.
func TestClientSecretFileIsReadWhenTheConfigHoldsNoSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.client.json")
	if err := WriteTokenFile(path, []byte(`{"client_id":"cid","client_secret":"from-file"}`)); err != nil {
		t.Fatal(err)
	}
	cfg := &OAuthConfig{ClientID: "cid", AuthURL: "https://a.example.com/auth"}
	cfg.TokenURL = "https://a.example.com/token"
	cfg.ClientSecretFile = path
	if got := NewManager(cfg, nil).oauth2Config.ClientSecret; got != "from-file" {
		t.Fatalf("client secret = %q", got)
	}
	cfg.ClientSecret = "inline"
	if got := NewManager(cfg, nil).oauth2Config.ClientSecret; got != "inline" {
		t.Fatalf("inline secret should win, got %q", got)
	}
}
