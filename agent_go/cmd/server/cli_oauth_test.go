package server

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

func TestCLIDeviceOAuthBrowserApprovalAndRestrictions(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	t.Setenv("WORKSPACE_DOCS_PATH", filepath.Join(t.TempDir(), "docs"))
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AUTH_SECRET", "cli-oauth-test-signing-secret")
	t.Setenv("PUBLIC_URL", "https://agentworks.example.com")
	api := &StreamingAPI{}
	router := mux.NewRouter()
	router.HandleFunc(cliOAuthDevicePath, api.handleCLIOAuthDevice).Methods("POST")
	router.HandleFunc(cliOAuthConsentPath, api.handleCLIOAuthConsent).Methods("GET", "POST")
	router.HandleFunc(cliOAuthTokenPath, api.handleCLIOAuthToken).Methods("POST")
	router.HandleFunc(cliOAuthRevokePath, api.handleCLIOAuthRevoke).Methods("POST")
	router.HandleFunc("/api/external/v1/tools", api.handleExternalTools).Methods("GET")
	router.HandleFunc("/api/auth/access-tokens", api.handleAccessTokens).Methods("GET")
	handler := AuthMiddleware(router)
	request := func(method, path, body, token, contentType string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	created := request("POST", cliOAuthDevicePath, "", "", "")
	if created.Code != 200 {
		t.Fatal(created.Code, created.Body)
	}
	var device struct {
		Code     string `json:"device_code"`
		URL      string `json:"verification_uri_complete"`
		UserCode string `json:"user_code"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &device); err != nil {
		t.Fatal(err)
	}
	link, err := url.Parse(device.URL)
	if err != nil {
		t.Fatal(err)
	}
	if link.Host != "agentworks.example.com" || link.Path != cliOAuthBrowserPath {
		t.Fatal(device.URL)
	}
	verify := link.Query().Get("code")
	if len(device.UserCode) != 8 || device.UserCode != strings.ToUpper(verify[len("cli_verify_"):len("cli_verify_")+8]) {
		t.Fatal("verification code mismatch")
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "client_id": {cliOAuthClientID}, "resource": {"https://agentworks.example.com/api/external/v1"}, "device_code": {device.Code}}
	pending := request("POST", cliOAuthTokenPath, form.Encode(), "", "application/x-www-form-urlencoded")
	if pending.Code != 400 || !strings.Contains(pending.Body.String(), "authorization_pending") {
		t.Fatal(pending.Code, pending.Body)
	}
	consentPath := cliOAuthConsentPath + "?code=" + verify
	if unauth := request("GET", consentPath, "", "", ""); unauth.Code != 401 {
		t.Fatal("anonymous consent", unauth.Code)
	}
	jwt, err := GenerateJWT(GetDefaultUserID(), "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	consent := request("GET", consentPath, "", jwt, "")
	if consent.Code != 200 || !strings.Contains(consent.Body.String(), "AgentWorks CLI") || !strings.Contains(consent.Body.String(), device.UserCode) {
		t.Fatal(consent.Code, consent.Body)
	}
	approved := request("POST", consentPath, `{"decision":"approve"}`, jwt, "application/json")
	if approved.Code != 200 {
		t.Fatal(approved.Code, approved.Body)
	}
	issued := request("POST", cliOAuthTokenPath, form.Encode(), "", "application/x-www-form-urlencoded")
	if issued.Code != 200 {
		t.Fatal(issued.Code, issued.Body)
	}
	var pair struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	if err := json.Unmarshal(issued.Body.Bytes(), &pair); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pair.Access, cliOAuthAccessPrefix) || !strings.HasPrefix(pair.Refresh, cliOAuthRefreshPrefix) {
		t.Fatal("wrong token type")
	}
	if replay := request("POST", cliOAuthTokenPath, form.Encode(), "", "application/x-www-form-urlencoded"); replay.Code != 400 {
		t.Fatal("device replay", replay.Code)
	}
	tools := request("GET", "/api/external/v1/tools", "", pair.Access, "")
	if tools.Code != 200 || !strings.Contains(tools.Body.String(), "list_workflows") {
		t.Fatal("CLI access failed", tools.Code, tools.Body)
	}
	if forbidden := request("GET", "/api/auth/access-tokens", "", pair.Access, ""); forbidden.Code != 403 {
		t.Fatal("CLI token escaped external API", forbidden.Code)
	}
	if query := request("GET", "/api/external/v1/tools?token="+pair.Access, "", "", ""); query.Code != 403 {
		t.Fatal("CLI query token accepted", query.Code)
	}
	refreshForm := url.Values{"grant_type": {"refresh_token"}, "client_id": {cliOAuthClientID}, "resource": {"https://agentworks.example.com/api/external/v1"}, "refresh_token": {pair.Refresh}}
	rotated := request("POST", cliOAuthTokenPath, refreshForm.Encode(), "", "application/x-www-form-urlencoded")
	if rotated.Code != 200 {
		t.Fatal(rotated.Code, rotated.Body)
	}
	var pair2 struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	if err := json.Unmarshal(rotated.Body.Bytes(), &pair2); err != nil {
		t.Fatal(err)
	}
	if pair2.Access == pair.Access || pair2.Refresh == pair.Refresh {
		t.Fatal("refresh did not rotate")
	}
	revoked := request("POST", cliOAuthRevokePath, url.Values{"token": {pair2.Refresh}}.Encode(), "", "application/x-www-form-urlencoded")
	if revoked.Code != 200 {
		t.Fatal(revoked.Code, revoked.Body)
	}
	if after := request("GET", "/api/external/v1/tools", "", pair2.Access, ""); after.Code != 401 {
		t.Fatal("revoked CLI token accepted", after.Code)
	}
	second := request("POST", cliOAuthDevicePath, "", "", "")
	if second.Code != 200 {
		t.Fatal("second device", second.Code)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &device); err != nil {
		t.Fatal(err)
	}
	link, err = url.Parse(device.URL)
	if err != nil {
		t.Fatal(err)
	}
	denied := request("POST", cliOAuthConsentPath+"?code="+link.Query().Get("code"), `{"decision":"deny"}`, jwt, "application/json")
	if denied.Code != 200 {
		t.Fatal("denial", denied.Code)
	}
	form.Set("device_code", device.Code)
	if polled := request("POST", cliOAuthTokenPath, form.Encode(), "", "application/x-www-form-urlencoded"); polled.Code != 400 || !strings.Contains(polled.Body.String(), "access_denied") {
		t.Fatal("denial not enforced", polled.Code, polled.Body)
	}
}

func TestCLIDeviceOAuthLocalOrigin(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	t.Setenv("AUTH_SECRET", "cli-oauth-local-test-secret")
	t.Setenv("PUBLIC_URL", "")
	api := &StreamingAPI{}
	local := httptest.NewRequest("POST", "http://127.0.0.1:3000"+cliOAuthDevicePath, nil)
	w := httptest.NewRecorder()
	api.handleCLIOAuthDevice(w, local)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "http://127.0.0.1:3000/oauth/cli") {
		t.Fatal("loopback browser sign-in unavailable", w.Code, w.Body)
	}
	public := httptest.NewRequest("POST", "http://public.example"+cliOAuthDevicePath, nil)
	w = httptest.NewRecorder()
	api.handleCLIOAuthDevice(w, public)
	if w.Code != 503 {
		t.Fatal("unconfigured public origin accepted", w.Code)
	}
	t.Setenv("PUBLIC_URL", "http://localhost:3000")
	w = httptest.NewRecorder()
	api.handleCLIOAuthDevice(w, local)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "http://localhost:3000/oauth/cli") {
		t.Fatal("configured local origin rejected", w.Code, w.Body)
	}
	t.Setenv("AGENTWORKS_CLI_BROWSER_URL", "http://127.0.0.1:51733")
	w = httptest.NewRecorder()
	api.handleCLIOAuthDevice(w, local)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "http://127.0.0.1:51733/oauth/cli") {
		t.Fatal("local frontend approval URL unavailable", w.Code, w.Body)
	}
	t.Setenv("PUBLIC_URL", "https://agentworks.example.com")
	w = httptest.NewRecorder()
	api.handleCLIOAuthDevice(w, local)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "https://agentworks.example.com/oauth/cli") {
		t.Fatal("public approval URL was overridden by local frontend", w.Code, w.Body)
	}
}

func TestCLIExecutorScopesAreExplicitAndIndependent(t *testing.T) {
	for _, test := range []struct {
		scopes []string
		valid  bool
	}{{[]string{"devices:connect"}, true}, {[]string{"workflows:read", "files:read", "files:write", "devices:connect"}, false}, {[]string{"devices:connect", "devices:connect"}, false}, {[]string{"files:write", "devices:connect"}, false}, {[]string{"plan:write", "devices:connect"}, false}} {
		_, valid := validCLIOAuthScopes(test.scopes)
		if valid != test.valid {
			t.Fatalf("scopes %v: %v", test.scopes, valid)
		}
	}
	if scopes, valid := validMCPOAuthScopes(""); !valid || slices.Contains(scopes, "devices:connect") || slices.Contains(scopes, "files:write") {
		t.Fatalf("default grants expanded: %v", scopes)
	}
	tokenTestSetup(t)
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	device, verify, err := store.CreateCLIDevice(t.Context(), "devices:connect")
	if err != nil {
		t.Fatal(err)
	}
	requested, err := store.CLIDeviceRequest(t.Context(), verify)
	if err != nil || len(requested) != 1 || requested[0] != "devices:connect" {
		t.Fatalf("consent scopes %v %v", requested, err)
	}
	owner := &UserClaims{UserID: GetDefaultUserID(), Username: "owner"}
	if err = store.DecideCLIDevice(t.Context(), verify, owner, true); err != nil {
		t.Fatal(err)
	}
	grant, access, _, err := store.PollCLIDevice(t.Context(), device, "http://127.0.0.1/api/external/v1")
	if err != nil || len(grant.Scopes) != 1 || grant.Scopes[0] != "devices:connect" {
		t.Fatalf("issued scopes %v %v", grant.Scopes, err)
	}
	parsed, err := store.Authenticate(t.Context(), access)
	if err != nil || !mcpOAuthTokenForGrant(parsed).Allows("devices:connect") {
		t.Fatalf("grant auth %v", err)
	}
	if !cliOAuthAllowedPath("GET", "/api/external/v1/devices/connect") || cliOAuthAllowedPath("POST", "/api/devices/other/files") {
		t.Fatal("executor credentials escaped their transport")
	}
}
