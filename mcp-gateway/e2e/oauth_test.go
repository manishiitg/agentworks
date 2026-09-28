package e2e

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// fetchAccessToken runs the real gateway OAuth flow: dynamic registration,
// authorize, human consent, code exchange. No test backdoors.
func fetchAccessToken(t *testing.T, baseURL, humanToken string) string {
	t.Helper()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resource := baseURL + "/mcp"

	reg, err := http.Post(baseURL+"/api/oauth/mcp/register", "application/json",
		strings.NewReader(`{"client_name":"e2e","redirect_uris":["https://client.example/callback"]}`))
	if err != nil {
		t.Fatalf("oauth register: %v", err)
	}
	defer reg.Body.Close()
	if reg.StatusCode != 201 {
		body, _ := io.ReadAll(reg.Body)
		t.Fatalf("oauth register status = %d: %s", reg.StatusCode, body)
	}
	var client struct {
		ID string `json:"client_id"`
	}
	if err := json.NewDecoder(reg.Body).Decode(&client); err != nil {
		t.Fatalf("oauth register decode: %v", err)
	}

	verifier := strings.Repeat("e", 43)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	authz := url.Values{
		"response_type": {"code"}, "client_id": {client.ID},
		"redirect_uri": {"https://client.example/callback"}, "scope": {"mcp"},
		"state": {"e2e"}, "code_challenge": {challenge},
		"code_challenge_method": {"S256"}, "resource": {resource},
	}
	started, err := noRedirect.Get(baseURL + "/api/oauth/mcp/authorize?" + authz.Encode())
	if err != nil {
		t.Fatalf("oauth authorize: %v", err)
	}
	defer started.Body.Close()
	if started.StatusCode != 303 {
		body, _ := io.ReadAll(started.Body)
		t.Fatalf("oauth authorize status = %d: %s", started.StatusCode, body)
	}
	loc, err := url.Parse(started.Header.Get("Location"))
	if err != nil {
		t.Fatalf("oauth authorize location: %v", err)
	}
	requestID := loc.Query().Get("request")
	if requestID == "" {
		t.Fatalf("oauth authorize: no request id in %q", loc.String())
	}

	consentURL := baseURL + "/api/oauth/mcp/consent?request=" + url.QueryEscape(requestID)
	consentReq, _ := http.NewRequest("GET", consentURL, nil)
	consentReq.Header.Set("Authorization", "Bearer "+humanToken)
	consent, err := http.DefaultClient.Do(consentReq)
	if err != nil {
		t.Fatalf("oauth consent: %v", err)
	}
	defer consent.Body.Close()
	if consent.StatusCode != 200 {
		body, _ := io.ReadAll(consent.Body)
		t.Fatalf("oauth consent status = %d: %s", consent.StatusCode, body)
	}

	decideReq, _ := http.NewRequest("POST", consentURL, strings.NewReader(`{"decision":"approve"}`))
	decideReq.Header.Set("Authorization", "Bearer "+humanToken)
	decideReq.Header.Set("Content-Type", "application/json")
	decided, err := http.DefaultClient.Do(decideReq)
	if err != nil {
		t.Fatalf("oauth decide: %v", err)
	}
	defer decided.Body.Close()
	var redirect struct {
		URL string `json:"redirect_url"`
	}
	if err := json.NewDecoder(decided.Body).Decode(&redirect); err != nil {
		t.Fatalf("oauth decide decode: %v", err)
	}
	callback, err := url.Parse(redirect.URL)
	if err != nil {
		t.Fatalf("oauth redirect parse: %v", err)
	}
	code := callback.Query().Get("code")
	if code == "" || callback.Query().Get("state") != "e2e" {
		t.Fatalf("oauth redirect missing code/state: %s", redirect.URL)
	}

	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {"https://client.example/callback"}, "client_id": {client.ID},
		"resource": {resource}, "code_verifier": {verifier},
	}
	issued, err := http.Post(baseURL+"/api/oauth/mcp/token", "application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("oauth token: %v", err)
	}
	defer issued.Body.Close()
	if issued.StatusCode != 200 {
		body, _ := io.ReadAll(issued.Body)
		t.Fatalf("oauth token status = %d: %s", issued.StatusCode, body)
	}
	var pair struct {
		Access string `json:"access_token"`
	}
	if err := json.NewDecoder(issued.Body).Decode(&pair); err != nil {
		t.Fatalf("oauth token decode: %v", err)
	}
	if pair.Access == "" {
		t.Fatalf("oauth token empty")
	}
	return pair.Access
}
