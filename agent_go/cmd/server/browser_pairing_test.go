package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
)

// PLAT-746-282: only the authenticated account issues a grant; a public one-use
// exchange returns only its own relay credential and exposes no browser CORS access.
func TestBrowserPairingHandoffAccountAndPublicExchange(t *testing.T) {
	f := newMultiUserFixture(t, sharedIdentityLayout())
	previous := browserrelay.Default
	browserrelay.Default = browserrelay.New()
	defer func() { browserrelay.Default.Close(); browserrelay.Default = previous }()
	noAuth := httptest.NewRecorder()
	f.API.handleAccountBrowsers(noAuth, httptest.NewRequest(http.MethodPost, "/api/browser/extension", strings.NewReader(`{"action":"pair"}`)))
	if noAuth.Code != 403 {
		t.Fatal("anonymous issued account grant")
	}
	issue := httptest.NewRecorder()
	f.API.handleAccountBrowsers(issue, httptest.NewRequest(http.MethodPost, "/api/browser/extension", strings.NewReader(`{"action":"pair"}`)).WithContext(f.Ctx(fixtureUserA)))
	var link struct{ Handoff string }
	if issue.Code != 200 || json.Unmarshal(issue.Body.Bytes(), &link) != nil || link.Handoff == "" || strings.Contains(issue.Body.String(), `"token"`) {
		t.Fatal("issuance exposed relay credential or failed")
	}
	call := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"handoff": link.Handoff})
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
		r.Header.Set("Origin", "https://unrelated.example.com")
		w := httptest.NewRecorder()
		f.API.corsMiddleware(http.HandlerFunc(f.API.handleBrowserPairingExchange)).ServeHTTP(w, r)
		return w
	}
	peek := call(browserPairingPeekPath)
	var identity struct {
		AccountID string `json:"account_id"`
	}
	if peek.Code != 200 || json.Unmarshal(peek.Body.Bytes(), &identity) != nil || identity.AccountID != fixtureUserA || strings.Contains(peek.Body.String(), `"token"`) || peek.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("preview lost identity or exposed credential/CORS")
	}
	redeemed := call(browserPairingRedeemPath)
	var paired struct{ Token string }
	expected, _ := browserrelay.Default.PairAccount(fixtureUserA, false)
	other, _ := browserrelay.Default.PairAccount(fixtureUserB, false)
	if redeemed.Code != 200 || json.Unmarshal(redeemed.Body.Bytes(), &paired) != nil || paired.Token != expected || paired.Token == other {
		t.Fatal("exchange returned wrong account credential")
	}
	if call(browserPairingRedeemPath).Code != 410 {
		t.Fatal("replay accepted")
	}
	if !shouldSkipAuth(browserPairingSetupPath) || !shouldSkipAuth(browserPairingPeekPath) || !shouldSkipAuth(browserPairingRedeemPath) || shouldSkipAuth(browserPairingRedeemPath+"/other") {
		t.Fatal("public pairing path fence incorrect")
	}
	setup := httptest.NewRecorder()
	f.API.handleBrowserPairingSetup(setup, httptest.NewRequest(http.MethodGet, browserPairingSetupPath, nil))
	if setup.Code != 200 || setup.Header().Get("Referrer-Policy") != "no-referrer" || strings.Contains(setup.Body.String(), link.Handoff) {
		t.Fatal("setup page leaked handoff")
	}
}
