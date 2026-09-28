package services

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubGoogleTokenInfo points googleTokenInfo at a local server for the
// duration of the test, instead of making a real call to Google — a live
// network dependency in this suite would be slow and break offline/CI runs.
func stubGoogleTokenInfo(t *testing.T, scope string) {
	t.Helper()
	stubGoogleTokenInfoWithEmail(t, scope, "")
}

// stubGoogleTokenInfoWithEmail also serves tokeninfo's `email` field, which
// Google includes when the token carries userinfo.email — the identity source
// a gmail.send-only token has, since getProfile is closed to it. An empty
// email omits the field, matching a token granted without userinfo.email.
func stubGoogleTokenInfoWithEmail(t *testing.T, scope, email string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if email == "" {
			fmt.Fprintf(w, `{"scope": %q}`, scope)
			return
		}
		fmt.Fprintf(w, `{"scope": %q, "email": %q}`, scope, email)
	}))
	t.Cleanup(server.Close)
	original := googleTokenInfoURL
	googleTokenInfoURL = server.URL
	t.Cleanup(func() { googleTokenInfoURL = original })
}

// unreachableGoogleTokenInfo makes the tokeninfo call fail fast and
// deterministically (unroutable address + the client's short timeout) so a
// test exercises the "tokeninfo unavailable" fallback without touching the
// network — and, when it wants a *getProfile* outcome, is not accidentally
// short-circuited by a real tokeninfo success.
func unreachableGoogleTokenInfo(t *testing.T) {
	t.Helper()
	original := googleTokenInfoURL
	googleTokenInfoURL = "http://127.0.0.1:1" // nothing listens here
	t.Cleanup(func() { googleTokenInfoURL = original })
}

func TestGogArgsForAuthPrefersAccessToken(t *testing.T) {
	cfg := &GmailConfig{Token: "tok-123", gogAccountEmail: "ignored@example.com", gogClientName: "ignored"}
	args, err := gogArgsForAuth(cfg)
	if err != nil {
		t.Fatalf("gogArgsForAuth: %v", err)
	}
	want := []string{"--access-token", "tok-123"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestGogArgsForAuthFallsBackToAccountAndClient(t *testing.T) {
	cfg := &GmailConfig{gogAccountEmail: "you@example.com", gogClientName: "primary"}
	args, err := gogArgsForAuth(cfg)
	if err != nil {
		t.Fatalf("gogArgsForAuth: %v", err)
	}
	want := []string{"--account", "you@example.com", "--client", "primary"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestGogArgsForAuthErrorsWithNeither(t *testing.T) {
	if _, err := gogArgsForAuth(&GmailConfig{}); err == nil {
		t.Fatal("expected an error when neither a token nor account/client is set")
	}
	if _, err := gogArgsForAuth(nil); err == nil {
		t.Fatal("expected an error for a nil config")
	}
}

// fakeGog writes an executable that answers `api call gmail v1
// gmail.users.getProfile` with profileJSON (or fails if empty), and echoes
// its full argv, one arg per line, to argvFile — every other test inspects
// that file rather than trying to parse shell quoting.
func fakeGog(t *testing.T, profileJSON string, argvFile string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gog")
	profile := "exit 1"
	if profileJSON != "" {
		profile = "cat <<'EOF'\n" + profileJSON + "\nEOF"
	}
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do echo \"$a\" >> " + shellQuote(argvFile) + "; done\n" +
		"case \" $* \" in\n" +
		"*\" getProfile \"*|*\"gmail.users.getProfile\"*) " + profile + "\n;;\n" +
		"*) echo '{\"id\":\"msg-fake-1\"}' ;;\n" +
		"esac\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func readArgvLines(t *testing.T, argvFile string) []string {
	t.Helper()
	data, err := os.ReadFile(argvFile)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

func TestComputeAuthStatusGogUsesAccessTokenAndReportsEmail(t *testing.T) {
	stubGoogleTokenInfo(t, "https://www.googleapis.com/auth/gmail.send https://www.googleapis.com/auth/gmail.readonly")
	argvFile := filepath.Join(t.TempDir(), "argv.log")
	gog := fakeGog(t, `{"emailAddress": "sender@example.com"}`, argvFile)

	cfg := &GmailConfig{Token: "tok-abc"}
	st := (&GmailService{}).computeAuthStatusGog(context.Background(), gog, cfg)

	if !st.GwsInstalled || !st.Authenticated || !st.HasGmailScope {
		t.Fatalf("expected an authenticated status, got %+v", st)
	}
	if st.Email != "sender@example.com" {
		t.Fatalf("Email = %q, want sender@example.com", st.Email)
	}

	argv := strings.Join(readArgvLines(t, argvFile), " ")
	if !strings.Contains(argv, "--access-token tok-abc") {
		t.Errorf("argv %q did not pass --access-token tok-abc", argv)
	}
	if !strings.Contains(argv, "gmail.users.getProfile") {
		t.Errorf("argv %q did not call gmail.users.getProfile", argv)
	}
}

// The whole point of the tokeninfo check: report what was actually granted,
// not what we last requested — a project's consent screen can silently drop
// a requested scope (the "College Khabar" gotcha), and a stale hardcoded
// Scopes value would hide that from the UI.
func TestComputeAuthStatusGogReportsActuallyGrantedScopesNotTheRequestedConstant(t *testing.T) {
	// Deliberately narrower than gmailOAuthScopes (which also includes
	// userinfo.email) — proves this isn't just echoing the constant back.
	stubGoogleTokenInfo(t, "https://www.googleapis.com/auth/gmail.readonly")
	argvFile := filepath.Join(t.TempDir(), "argv.log")
	gog := fakeGog(t, `{"emailAddress": "sender@example.com"}`, argvFile)

	cfg := &GmailConfig{Token: "tok-abc"}
	st := (&GmailService{}).computeAuthStatusGog(context.Background(), gog, cfg)

	if len(st.Scopes) != 1 || st.Scopes[0] != "https://www.googleapis.com/auth/gmail.readonly" {
		t.Fatalf("Scopes = %v, want exactly the narrower tokeninfo-reported set", st.Scopes)
	}
}

func TestComputeAuthStatusGogFallsBackToRequestedScopesWhenTokenInfoFails(t *testing.T) {
	// No stub installed: googleTokenInfoURL points at the real Google host,
	// which this test never lets it reach — a short client timeout combined
	// with an unroutable address makes the call fail fast instead of relying
	// on network absence, so the fallback path is exercised deterministically.
	original := googleTokenInfoURL
	googleTokenInfoURL = "http://127.0.0.1:1" // nothing listens here
	t.Cleanup(func() { googleTokenInfoURL = original })

	argvFile := filepath.Join(t.TempDir(), "argv.log")
	gog := fakeGog(t, `{"emailAddress": "sender@example.com"}`, argvFile)

	cfg := &GmailConfig{Token: "tok-abc"}
	st := (&GmailService{}).computeAuthStatusGog(context.Background(), gog, cfg)

	if !st.Authenticated {
		t.Fatalf("expected authentication to still succeed despite the scope check failing, got %+v", st)
	}
	if len(st.Scopes) == 0 {
		t.Fatal("expected a fallback scope list, got none")
	}
}

func TestComputeAuthStatusGogReportsNotAuthenticatedOnFailure(t *testing.T) {
	// tokeninfo unavailable AND getProfile failing is the only combination
	// that means "not authenticated": with tokeninfo reachable, Google's own
	// verdict on the token would decide instead of the getProfile probe.
	unreachableGoogleTokenInfo(t)
	argvFile := filepath.Join(t.TempDir(), "argv.log")
	gog := fakeGog(t, "", argvFile) // empty profileJSON -> the fake exits 1

	cfg := &GmailConfig{Token: "tok-abc"}
	st := (&GmailService{}).computeAuthStatusGog(context.Background(), gog, cfg)

	if st.Authenticated {
		t.Fatalf("expected Authenticated=false on a getProfile failure, got %+v", st)
	}
	if st.Detail == "" {
		t.Error("expected a non-empty Detail explaining the failure")
	}
}

// The send-only default (gmailOAuthScopesFor(false)): gmail.users.getProfile
// is closed to a gmail.send token, so if getProfile were still the liveness
// check every default-scoped connection would read as "reconnect" and Pulse
// would refuse to send through it. Google's tokeninfo verdict on the token,
// plus its email, is what makes such a connection authenticated and named.
func TestComputeAuthStatusGogSendOnlyTokenIsAuthenticatedAndNamedViaTokenInfo(t *testing.T) {
	stubGoogleTokenInfoWithEmail(t,
		"https://www.googleapis.com/auth/gmail.send https://www.googleapis.com/auth/userinfo.email",
		"sender@example.com")
	argvFile := filepath.Join(t.TempDir(), "argv.log")
	gog := fakeGog(t, "", argvFile) // getProfile exits 1, exactly as Google does for send-only

	cfg := &GmailConfig{Token: "tok-send-only"}
	st := (&GmailService{}).computeAuthStatusGog(context.Background(), gog, cfg)

	if !st.Authenticated || !st.HasGmailScope {
		t.Fatalf("send-only token must be authenticated with a Gmail send scope, got %+v", st)
	}
	if st.Email != "sender@example.com" {
		t.Fatalf("Email = %q, want the tokeninfo email (getProfile is unavailable to a send-only token)", st.Email)
	}
	if st.Detail != "" {
		t.Errorf("expected no Detail on a healthy send-only connection, got %q", st.Detail)
	}
	if strings.Contains(strings.Join(readArgvLines(t, argvFile), " "), "gmail.users.getProfile") {
		t.Error("getProfile must not be probed when tokeninfo already supplied the identity")
	}
}

// A token that can only read is authenticated but cannot send, and the UI
// must say so rather than leave a Pulse send to fail later.
func TestComputeAuthStatusGogReadOnlyTokenIsAuthenticatedButLacksSendScope(t *testing.T) {
	stubGoogleTokenInfoWithEmail(t, "https://www.googleapis.com/auth/gmail.readonly", "reader@example.com")
	argvFile := filepath.Join(t.TempDir(), "argv.log")
	gog := fakeGog(t, `{"emailAddress": "reader@example.com"}`, argvFile)

	st := (&GmailService{}).computeAuthStatusGog(context.Background(), gog, &GmailConfig{Token: "tok-read"})

	if !st.Authenticated {
		t.Fatalf("a live read-only token is still authenticated, got %+v", st)
	}
	if st.HasGmailScope {
		t.Fatalf("gmail.readonly alone must not count as a send scope, got %+v", st)
	}
	if st.Detail == "" {
		t.Error("expected a Detail explaining the missing send scope")
	}
}

func TestComputeAuthStatusGogReportsMissingBinary(t *testing.T) {
	st := (&GmailService{}).computeAuthStatusGog(context.Background(), filepath.Join(t.TempDir(), "definitely-not-gog"), &GmailConfig{Token: "x"})
	if st.GwsInstalled || st.Authenticated {
		t.Fatalf("expected an unavailable status for a missing binary, got %+v", st)
	}
}

func TestComputeAuthStatusGogErrorsWithoutAuthKnobs(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "argv.log")
	gog := fakeGog(t, `{"emailAddress": "x@example.com"}`, argvFile)

	// No Token and no gogAccountEmail/gogClientName: nothing to authenticate with.
	st := (&GmailService{}).computeAuthStatusGog(context.Background(), gog, &GmailConfig{})
	if st.Authenticated {
		t.Fatalf("expected Authenticated=false with no auth knobs, got %+v", st)
	}
}

func TestSendGogPassesExpectedArgs(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "argv.log")
	gog := fakeGog(t, "", argvFile)

	cfg := &GmailConfig{Token: "tok-send"}
	g := &GmailService{}
	msgID, err := g.sendGog(context.Background(), gog, cfg, "to@example.com", "Subject line", "body text")
	if err != nil {
		t.Fatalf("sendGog: %v", err)
	}
	if msgID != "msg-fake-1" {
		t.Fatalf("msgID = %q, want msg-fake-1", msgID)
	}

	argv := strings.Join(readArgvLines(t, argvFile), " ")
	for _, want := range []string{"gmail", "send", "--to", "to@example.com", "--body", "body text", "--access-token", "tok-send", "--json"} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv %q missing %q", argv, want)
		}
	}
}

func TestSendGogPropagatesAuthError(t *testing.T) {
	g := &GmailService{}
	if _, err := g.sendGog(context.Background(), "gog", &GmailConfig{}, "to@example.com", "s", "b"); err == nil {
		t.Fatal("expected an error when the connection has no usable auth knobs")
	}
}

func TestSendComposedGogSendsHTMLOnlyForRichEmail(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "argv.log")
	path := fakeGog(t, "", argvFile)
	attachment := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(attachment, []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &GmailConfig{Token: "tok-raw"}
	g := &GmailService{}
	msgID, err := g.sendComposedGog(context.Background(), path, cfg, "to@example.com", []string{"cc@example.com"}, "Subject", "plain body", "<b>html</b>", []string{attachment})
	if err != nil {
		t.Fatalf("sendComposedGog: %v", err)
	}
	if msgID != "msg-fake-1" {
		t.Fatalf("msgID = %q, want msg-fake-1", msgID)
	}

	argv := strings.Join(readArgvLines(t, argvFile), " ")
	for _, want := range []string{"--body-html <b>html</b>", "--cc cc@example.com", "--attach " + attachment} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv %q missing %q", argv, want)
		}
	}
	if strings.Contains(argv, "--body plain body") {
		t.Errorf("rich Gmail must not include a plain-text alternative: %q", argv)
	}
	if strings.Contains(argv, "--raw-file") {
		t.Errorf("rich Gmail must use gog's native composer: %q", argv)
	}
}

func TestSendComposedGogConvertsPlainBodyToHTMLOnly(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "argv.log")
	path := fakeGog(t, "", argvFile)

	g := &GmailService{}
	_, err := g.sendComposedGog(context.Background(), path, &GmailConfig{Token: "tok"}, "to@example.com", nil, "Subject", "hello <team>\nnext", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(readArgvLines(t, argvFile), " ")
	if !strings.Contains(argv, `--body-html <div style="white-space: pre-wrap;">hello &lt;team&gt;`) {
		t.Fatalf("plain content was not safely converted to HTML: %q", argv)
	}
	if strings.Contains(argv, "--body hello") {
		t.Fatalf("plain MIME body must not be sent: %q", argv)
	}
}

func TestImportRefreshTokenIntoGogRegistersClientBeforeToken(t *testing.T) {
	binDir := t.TempDir()
	argvFile := filepath.Join(t.TempDir(), "argv.log")
	gogPath := filepath.Join(binDir, "gog")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"---\" >> " + shellQuote(argvFile) + "\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\" >> " + shellQuote(argvFile) + "; done\n"
	if err := os.WriteFile(gogPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GOG_HOME", t.TempDir())
	clientsDir := t.TempDir()
	t.Setenv("GMAIL_OAUTH_CLIENTS_DIR", clientsDir)
	clientDir := filepath.Join(clientsDir, "primary")
	if err := os.MkdirAll(clientDir, 0o700); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(clientDir, "client_secret.json")
	if err := os.WriteFile(secretPath, []byte(`{"installed":{"client_id":"id","client_secret":"secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ImportRefreshTokenIntoGog(context.Background(), gogHomeDir(), "me@example.com", "primary", "refresh-token"); err != nil {
		t.Fatalf("ImportRefreshTokenIntoGog: %v", err)
	}
	argv := strings.Join(readArgvLines(t, argvFile), " ")
	credentials := "--client primary --insecure --no-input --force"
	if !strings.Contains(argv, "auth credentials set ") || !strings.Contains(argv, credentials) {
		t.Fatalf("argv %q missing credentials registration %q", argv, credentials)
	}
	if !strings.Contains(argv, "auth import --email me@example.com --client primary") || !strings.Contains(argv, "--no-input --force") {
		t.Fatalf("argv %q missing token import", argv)
	}
}

func TestImportRefreshTokenIntoGogReportsImportFailureDetail(t *testing.T) {
	binDir := t.TempDir()
	gogPath := filepath.Join(binDir, "gog")
	script := "#!/bin/sh\n" +
		"case \" $* \" in\n" +
		"  *\" auth import \"*) echo 'entry already exists; use --force' >&2; exit 2 ;;\n" +
		"esac\n"
	if err := os.WriteFile(gogPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOG_HOME", t.TempDir())
	clientsDir := t.TempDir()
	t.Setenv("GMAIL_OAUTH_CLIENTS_DIR", clientsDir)
	clientDir := filepath.Join(clientsDir, "primary")
	if err := os.MkdirAll(clientDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clientDir, "client_secret.json"), []byte(`{"installed":{"client_id":"id","client_secret":"secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	err := ImportRefreshTokenIntoGog(context.Background(), gogHomeDir(), "me@example.com", "primary", "refresh-token")
	if err == nil || !strings.Contains(err.Error(), "entry already exists; use --force") {
		t.Fatalf("ImportRefreshTokenIntoGog error = %v, want gog stderr detail", err)
	}
}
