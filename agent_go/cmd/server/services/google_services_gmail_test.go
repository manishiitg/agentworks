package services

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// PLAT-746: a long Sheets run lost its grants when gog reported a refresh
// timeout as valid=false. Exercise the real account/cache/CLI dispatch path;
// only the external gog/Google process is a fixture.
func TestGoogleProbeFailureKeepsOnlyRecentVerifiedAccountGrants(t *testing.T) {
	t.Setenv("WORKSPACE_API_URL", "http://127.0.0.1:1")
	t.Setenv("GOG_HOME", t.TempDir())
	directory := t.TempDir()
	response, failed := filepath.Join(directory, "status.json"), filepath.Join(directory, "command-fails")
	binary := filepath.Join(directory, "gog")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\ncase \" $* \" in\n*\" auth list \"*) if test -f " + quote(failed) + "; then exit 1; fi; cat " + quote(response) + ";;\n*) printf 'sheets-result\\n';;\nesac\n"
	if err := os.WriteFile(binary, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	const sheetsScope = "https://www.googleapis.com/auth/spreadsheets"
	connection := GmailConnection{ID: "google-test", Email: "worker@example.com", ClientName: "primary", AuthBackend: "gog", Enabled: true, Scopes: []string{sheetsScope}, Services: []GoogleServiceGrant{{Service: "sheets", Write: true}}}
	newService := func() *GmailService {
		return &GmailService{config: &GmailConfig{Connections: []GmailConnection{connection.Clone()}, DefaultConnectionID: connection.ID, GogPath: binary}, gogPath: binary}
	}
	service := newService()
	previous := GetGmailService()
	SetGmailService(service)
	t.Cleanup(func() { SetGmailService(previous) })
	writeStatus := func(t *testing.T, valid bool, detail string, scopes []string) {
		t.Helper()
		data, err := json.Marshal(map[string]interface{}{"accounts": []gogStoredAccount{{Email: connection.Email, Client: connection.ClientName, Valid: valid, Error: detail, Scopes: scopes}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(response, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	callSheets := func(t *testing.T) {
		t.Helper()
		output, err := RunGoogleCLI(t.Context(), connection.ID, []string{"sheets", "get", "fixture-sheet", "A1:I10", "--json"})
		if err != nil || !strings.Contains(output, "sheets-result") {
			t.Fatalf("verified Sheets call failed: output=%q err=%v", output, err)
		}
	}
	expire := func() {
		service.mu.Lock()
		service.authCaches[connection.ID].cachedAt = time.Now().Add(-2 * gmailAuthCacheTTL)
		service.mu.Unlock()
	}
	writeStatus(t, true, "", connection.Scopes)
	callSheets(t)
	verified := service.authCaches[connection.ID].verifiedAt
	for _, mode := range []string{"command error", "refresh timeout"} {
		expire()
		if mode == "command error" {
			if err := os.WriteFile(failed, nil, 0600); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.Remove(failed); err != nil {
				t.Fatal(err)
			}
			writeStatus(t, false, "refresh access token: context deadline exceeded", connection.Scopes)
		}
		callSheets(t)
		if !service.authCaches[connection.ID].verifiedAt.Equal(verified) {
			t.Fatal("a failed probe extended the verified-grant age")
		}
	}
	// The settings UI refresh uses the same retention rule as a tool call.
	expire()
	if status, found := service.AuthStatusForConnection(connection.ID); !found || !status.Authenticated {
		t.Fatal("background refresh discarded the existing verified account")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		service.mu.RLock()
		refreshing := service.authCaches[connection.ID].refreshing
		service.mu.RUnlock()
		if !refreshing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background probe did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	callSheets(t)
	for _, refusal := range []string{"expired evidence", "invalid grant", "missing account", "reduced scopes", "cold cache", "another account"} {
		t.Run(refusal, func(t *testing.T) {
			// Restore a real successful probe before each independent refusal.
			service = newService()
			SetGmailService(service)
			writeStatus(t, true, "", connection.Scopes)
			callSheets(t)
			expire()
			selected := connection.ID
			switch refusal {
			case "expired evidence":
				service.authCaches[connection.ID].verifiedAt = time.Now().Add(-gmailAuthVerifiedMaxAge - time.Second)
				writeStatus(t, false, "refresh access token: context deadline exceeded", connection.Scopes)
			case "invalid grant":
				writeStatus(t, false, "invalid_grant: token revoked (not a timeout)", nil)
			case "missing account":
				if err := os.WriteFile(response, []byte(`{"accounts":[]}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "reduced scopes":
				// Stored requested Sheets write stays enabled; Google's actual
				// unrelated scope must replace the old grant immediately.
				writeStatus(t, true, "", []string{GmailReadonlyScope})
			case "another account":
				other := connection.Clone()
				other.ID, other.Email = "another-account", "other@example.com"
				service.config.Connections = append(service.config.Connections, other)
				selected = other.ID
			case "cold cache":
				service.authCaches = nil
				writeStatus(t, false, "refresh access token: context deadline exceeded", connection.Scopes)
			}
			output, err := RunGoogleCLI(t.Context(), selected, []string{"sheets", "get", "fixture-sheet", "A1:I10", "--json"})
			if err == nil || output != "" {
				t.Fatalf("%s retained an unverified grant: %q %v", refusal, output, err)
			}
		})
	}
}

func TestGoogleCLIAccessIncludesGmailOnlyAfterObservedReadGrant(t *testing.T) {
	connection := GmailConnection{
		ID:              "gmail_001",
		DisplayName:     "Finance",
		Email:           "finance@example.com",
		ClientName:      "finance",
		AuthBackend:     "gog",
		Enabled:         true,
		AllowReadAccess: true,
	}
	service := &GmailService{config: &GmailConfig{
		Connections:         []GmailConnection{connection},
		DefaultConnectionID: connection.ID,
	}}
	service.gogPath = fakeCheckedGog(t, []gogStoredAccount{{
		Email: connection.Email, Client: connection.ClientName, Valid: true,
		Scopes: []string{GmailReadonlyScope},
	}})

	access, err := service.GoogleCLIAccessForConnection(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if writable, ok := access.Grants["gmail"]; !ok || writable {
		t.Fatalf("gmail grant = %v, want present and read-only", access.Grants)
	}

	// The connection still records the read-access opt-in, but Google's
	// observed grant no longer includes it (e.g. downgraded, or the account
	// was never actually reconnected with that scope) — must fail closed on
	// the LIVE check, not on whatever the connection happens to have cached.
	service.InvalidateConnectionAuthCache(connection.ID)
	service.gogPath = fakeCheckedGog(t, []gogStoredAccount{{
		Email: connection.Email, Client: connection.ClientName, Valid: true,
		Scopes: []string{"https://www.googleapis.com/auth/gmail.send"},
	}})
	if _, err := service.GoogleCLIAccessForConnection(context.Background(), ""); err == nil {
		t.Fatal("stored Gmail read request without Google's observed grant must fail closed")
	}
}

func TestGoogleCLIAccessRequiresOptInAndLiveComposeScopeForAgentWrites(t *testing.T) {
	connection := GmailConnection{
		ID:                    "gmail_003",
		DisplayName:           "Support",
		Email:                 "support@example.com",
		ClientName:            "support",
		AuthBackend:           "gog",
		Enabled:               true,
		AllowReadAccess:       true,
		AllowAgentWriteAccess: true,
	}
	service := &GmailService{config: &GmailConfig{
		Connections:         []GmailConnection{connection},
		DefaultConnectionID: connection.ID,
	}}
	service.gogPath = fakeCheckedGog(t, []gogStoredAccount{{
		Email: connection.Email, Client: connection.ClientName, Valid: true,
		Scopes: []string{GmailReadonlyScope, GmailComposeScope},
	}})

	access, err := service.GoogleCLIAccessForConnection(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if writable, ok := access.Grants["gmail"]; !ok || !writable {
		t.Fatalf("gmail grant = %v, want writable", access.Grants)
	}

	// A stored opt-in is not authority by itself. If the live compose scope is
	// absent, the same connection must fall back to read-only access.
	service.InvalidateConnectionAuthCache(connection.ID)
	service.gogPath = fakeCheckedGog(t, []gogStoredAccount{{
		Email: connection.Email, Client: connection.ClientName, Valid: true,
		Scopes: []string{GmailReadonlyScope},
	}})
	access, err = service.GoogleCLIAccessForConnection(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if writable, ok := access.Grants["gmail"]; !ok || writable {
		t.Fatalf("gmail grant = %v, want read-only without live compose scope", access.Grants)
	}

	// Conversely, a token carrying compose does not authorize agents unless
	// the operator explicitly enabled the per-connection permission.
	connection.AllowAgentWriteAccess = false
	service.config.Connections[0] = connection
	service.InvalidateConnectionAuthCache(connection.ID)
	service.gogPath = fakeCheckedGog(t, []gogStoredAccount{{
		Email: connection.Email, Client: connection.ClientName, Valid: true,
		Scopes: []string{GmailReadonlyScope, GmailComposeScope},
	}})
	access, err = service.GoogleCLIAccessForConnection(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if writable := access.Grants["gmail"]; writable {
		t.Fatalf("gmail grant = %v, compose scope bypassed stored opt-in", access.Grants)
	}
}

func TestRunGoogleCLIRemovesGmailSendGuardsOnlyForResolvedWriteGrant(t *testing.T) {
	t.Setenv("GOG_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "gog")
	script := `#!/bin/sh
case " $* " in
  *" auth list "*) printf '%s\n' '{"accounts":[{"email":"support@example.com","client":"support","valid":true,"scopes":["https://www.googleapis.com/auth/gmail.readonly","https://www.googleapis.com/auth/gmail.compose"]}]}' ;;
  *) printf '%s\n' "$@" ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	connection := GmailConnection{
		ID: "gmail_004", DisplayName: "Support", Email: "support@example.com",
		ClientName: "support", AuthBackend: "gog", Enabled: true,
		AllowReadAccess: true,
	}
	service := &GmailService{config: &GmailConfig{
		Connections: []GmailConnection{connection}, DefaultConnectionID: connection.ID, GogPath: path,
	}}
	service.gogPath = path
	previous := GetGmailService()
	SetGmailService(service)
	defer SetGmailService(previous)

	output, err := RunGoogleCLI(context.Background(), connection.ID, []string{"gmail", "search", "subject:test"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "--readonly") || !strings.Contains(output, "--gmail-no-send") {
		t.Fatalf("read-only Gmail invocation lost its safety guards:\n%s", output)
	}

	service.mu.Lock()
	service.config.Connections[0].AllowAgentWriteAccess = true
	service.mu.Unlock()
	service.InvalidateConnectionAuthCache(connection.ID)
	output, err = RunGoogleCLI(context.Background(), connection.ID, []string{"gmail", "drafts", "create"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "--readonly") || strings.Contains(output, "--gmail-no-send") {
		t.Fatalf("opted-in Gmail write invocation still carried safety guards:\n%s", output)
	}
}

func TestGoogleCLIAccessCrossChecksServiceGrantsAgainstLiveScopes(t *testing.T) {
	connection := GmailConnection{
		ID:          "gmail_002",
		DisplayName: "Ops",
		Email:       "ops@example.com",
		ClientName:  "ops",
		AuthBackend: "gog",
		Enabled:     true,
		Services: []GoogleServiceGrant{
			{Service: "drive", Write: true},     // live grant will only cover read
			{Service: "calendar", Write: false}, // live grant will not cover this at all
		},
	}
	service := &GmailService{config: &GmailConfig{
		Connections:         []GmailConnection{connection},
		DefaultConnectionID: connection.ID,
	}}
	// drive.readonly only: no calendar scope, and calendar isn't part of the
	// Sheets/Docs/Slides "broader Drive scope also counts" carve-out in
	// GoogleScopesGrant, so this is a clean case of an unrelated service.
	service.gogPath = fakeCheckedGog(t, []gogStoredAccount{{
		Email: connection.Email, Client: connection.ClientName, Valid: true,
		Scopes: []string{"https://www.googleapis.com/auth/drive.readonly"},
	}})

	access, err := service.GoogleCLIAccessForConnection(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	writable, ok := access.Grants["drive"]
	if !ok {
		t.Fatal("drive grant missing despite a live read scope being granted")
	}
	if writable {
		t.Fatal("drive grant reported writable, but Google only granted the read scope — requested write must not be trusted over the live check")
	}
	if _, ok := access.Grants["calendar"]; ok {
		t.Fatal("calendar grant present despite no matching live scope — stored request alone must not authorize a service")
	}
}
