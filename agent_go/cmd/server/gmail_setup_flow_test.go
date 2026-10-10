package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailsetup"
	"golang.org/x/oauth2"
)

func gmailSetupFixture(t *testing.T) (*StreamingAPI, context.Context) {
	t.Helper()
	api, _, _ := gmailTriggerFixture(t)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("GMAIL_INBOUND_TOPICS", "")
	t.Setenv("GMAIL_INBOUND_AUDIENCE", "")
	t.Setenv("GMAIL_INBOUND_PUSH_EMAIL", "")
	t.Setenv("PUBLIC_URL", "https://app.example.com")
	t.Setenv("GMAIL_OAUTH_CLIENTS_DIR", t.TempDir())
	// Storing the OAuth client runs `gog auth credentials set`; a stand-in on PATH keeps this test off the real binary,
	// which a build machine does not have.
	fakeBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeBin, "gog"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := services.CreateOAuthClient(context.Background(), "app-project", []byte(`{"web":{"client_id":"123456-abcdef.apps.googleusercontent.com","client_secret":"SETUP-SECRET","project_id":"sample-project"}}`), false); err != nil {
		t.Fatal(err)
	}
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","username":"admin","email":"admin@example.com","admin":true},{"id":"owner","email":"owner@example.com"}]}`)
	ctx := sharedSecretsRequest("GET", "/", "admin", nil).Context()
	t.Cleanup(api.gmailSetup.stop)
	return api, ctx
}

type setupRoundTripper func(*http.Request) (*http.Response, error)

func (f setupRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGmailSetupConsentCompletesProvisioningAndOnlyActivatesAfterVerification(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "permission-failure"}[fail], func(t *testing.T) {
			api, ctx := gmailSetupFixture(t)
			job, err := api.prepareGmailSetup(ctx, "app-project", "")
			if err != nil {
				t.Fatal(err)
			}
			consent := httptest.NewRecorder()
			api.gmailSetupReview(consent, httptest.NewRequest("POST", job.ReviewURL, nil))
			var sub map[string]any
			googleClient := &http.Client{Transport: setupRoundTripper(func(r *http.Request) (*http.Response, error) {
				status := 200
				data := any(map[string]any{})
				if r.URL.Host == "oauth2.googleapis.com" {
					_ = r.ParseForm()
					if r.Form.Get("code_verifier") == "" || r.Form.Get("code") != "human-code" {
						t.Error("exchange lacks plan-bound PKCE")
					}
					data = map[string]any{"access_token": "EPHEMERAL-CLOUD-TOKEN", "refresh_token": "NEVER-STORE-CLOUD-REFRESH", "expires_in": 3600, "token_type": "Bearer"}
				} else {
					if r.Header.Get("Authorization") != "Bearer EPHEMERAL-CLOUD-TOKEN" {
						t.Error("missing Cloud authorization")
					}
					switch {
					case strings.HasSuffix(r.URL.Path, "/services/serviceusage.googleapis.com"):
						data = map[string]string{"name": "projects/123456/services/serviceusage.googleapis.com", "parent": "projects/123456", "state": "ENABLED"}
					case strings.HasSuffix(r.URL.Path, ":batchEnable") || strings.HasSuffix(r.URL.Path, ":generateServiceIdentity"):
						data = map[string]any{"done": true}
					case strings.Contains(r.URL.Path, "/subscriptions/"):
						if r.Method == "PUT" {
							_ = json.NewDecoder(r.Body).Decode(&sub)
							data = sub
						} else if sub == nil {
							status = 404
						} else {
							data = sub
						}
					case strings.HasSuffix(r.URL.Path, ":getIamPolicy"):
						if fail {
							status = 403
							data = map[string]string{"error": "SECRET"}
						} else {
							data = map[string]any{"etag": "preserve", "bindings": []any{}}
						}
					}
				}
				raw, _ := json.Marshal(data)
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(raw))), Header: make(http.Header), Request: r}, nil
			})}
			api.gmailSetup.mu.Lock()
			api.gmailSetup.ctx = context.WithValue(api.gmailSetup.ctx, oauth2.HTTPClient, googleClient)
			api.gmailSetup.mu.Unlock()
			req := httptest.NewRequest("GET", "https://app.example.com"+gmailOAuthCallbackPath+"?state="+job.ID+"&code=human-code", nil).WithContext(context.WithValue(ctx, oauth2.HTTPClient, googleClient))
			callback := httptest.NewRecorder()
			gmailOAuthCallbackHandler(api)(callback, req)
			if !strings.Contains(callback.Body.String(), "setup started") {
				t.Fatalf("callback: %s", callback.Body.String())
			}
			api.gmailSetup.wg.Wait()
			c, err := readGmailInboundConfig()
			if err != nil {
				t.Fatal(err)
			}
			status, _ := json.Marshal(api.gmailSetupStatus(ctx))
			if strings.Contains(string(status), "CLOUD-TOKEN") || strings.Contains(string(status), "CLOUD-REFRESH") || strings.Contains(string(status), "SETUP-SECRET") {
				t.Fatal("setup status leaked credentials")
			}
			if fail {
				if len(c.Topics) != 0 || !strings.Contains(string(status), "HTTP 403") {
					t.Fatalf("failed Cloud setup activated: %+v %s", c, status)
				}
			} else {
				if c.Topics["app-project"] != job.Plan.Topic || !strings.Contains(string(status), "Receiving infrastructure ready") {
					t.Fatalf("successful setup did not activate: %+v %s", c, status)
				}
				path, _ := gmailSetupConfigPath()
				raw, _ := os.ReadFile(path)
				if strings.Contains(string(raw), "CLOUD-TOKEN") || strings.Contains(string(raw), "CLOUD-REFRESH") {
					t.Fatal("persisted Cloud credentials")
				}
			}
		})
	}
}

func TestGmailSetupRefusesAnOAuthAppChangedAfterReview(t *testing.T) {
	api, ctx := gmailSetupFixture(t)
	job, err := api.prepareGmailSetup(ctx, "app-project", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = services.DeleteOAuthClient("app-project")
	if err = gmailSetupPlanCurrent(job); err == nil {
		t.Fatal("stale OAuth app plan accepted")
	}
}

func TestGmailSetupToolRequiresInteractiveAdminAndDoesNotProvisionOnPrepare(t *testing.T) {
	api, ctx := gmailSetupFixture(t)
	reg := &recordingRegistrar{}
	if err := api.registerGmailTriggerTools(reg, "human", "Workflow/mail"); err != nil {
		t.Fatal(err)
	}
	tool := reg.tools["setup_gmail_inbound"].exec
	out, err := tool(ctx, map[string]interface{}{"action": "prepare", "client_name": "app-project"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "SETUP-SECRET") || strings.Contains(out, "code_verifier") || strings.Contains(out, "client_secret") || strings.Contains(out, "access_token") {
		t.Fatalf("credentials leaked: %s", out)
	}
	var job gmailSetupJob
	if json.Unmarshal([]byte(out), &job) != nil || job.Plan.ProjectID != "sample-project" || job.ReviewURL == "" {
		t.Fatalf("bad plan: %s", out)
	}
	if c, e := readGmailInboundConfig(); e != nil || len(c.Topics) != 0 {
		t.Fatalf("prepare enabled intake: %+v %v", c, e)
	}
	for name, claims := range map[string]*UserClaims{
		"non-admin":        {UserID: "owner", Email: "owner@example.com"},
		"email-bot":        {UserID: "admin", Provider: "bot_route"},
		"external-builder": {UserID: "admin", ExternalBuilderOperationID: "operation"},
		"scoped-token":     {UserID: "admin", Scope: "report-preview"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := tool(context.WithValue(ctx, UserContextKey, claims), map[string]interface{}{"action": "prepare", "client_name": "app-project"}); err == nil {
				t.Fatal("unauthorized infrastructure setup")
			}
		})
	}
	status, _ := json.Marshal(api.gmailSetupStatus(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "owner"})))
	if strings.Contains(string(status), job.ID) || strings.Contains(string(status), "sample-project") {
		t.Fatalf("another user's pending review leaked: %s", status)
	}
}

func TestGmailSetupReviewRequiresHumanGoogleConsentUsesExistingCallbackAndPKCE(t *testing.T) {
	api, ctx := gmailSetupFixture(t)
	job, err := api.prepareGmailSetup(ctx, "app-project", "")
	if err != nil {
		t.Fatal(err)
	}
	review := httptest.NewRecorder()
	api.gmailSetupReview(review, httptest.NewRequest("GET", job.ReviewURL, nil))
	if review.Code != 200 || !strings.Contains(review.Body.String(), job.Plan.Subscription) || strings.Contains(review.Body.String(), "SETUP-SECRET") {
		t.Fatalf("review: %d %s", review.Code, review.Body.String())
	}
	if review.Header().Get("Referrer-Policy") != "same-origin" || !strings.Contains(review.Header().Get("Content-Security-Policy"), "form-action 'self' https://accounts.google.com;") {
		t.Fatal("review headers block its browser POST or Google redirect")
	}
	post := httptest.NewRequest("POST", job.ReviewURL, nil)
	for _, origin := range []string{"https://attacker.example", "null"} {
		post.Header.Set("Origin", origin)
		denied := httptest.NewRecorder()
		api.gmailSetupReview(denied, post)
		if denied.Code != 403 {
			t.Fatal("accepted cross-origin or opaque review submission")
		}
	}
	post.Header.Set("Origin", "https://app.example.com")
	consent := httptest.NewRecorder()
	api.gmailSetupReview(consent, post)
	u, _ := url.Parse(consent.Header().Get("Location"))
	if consent.Code != 303 || u.Host != "accounts.google.com" || u.Query().Get("code_challenge_method") != "S256" || u.Query().Get("redirect_uri") != "https://app.example.com"+gmailOAuthCallbackPath || u.Query().Get("scope") != "https://www.googleapis.com/auth/cloud-platform" || u.Query().Get("access_type") == "offline" {
		t.Fatalf("incorrect consent: %d %s", consent.Code, u)
	}
	if !shouldSkipAuth(gmailSetupStartPath) || shouldSkipAuth(gmailSetupStartPath+"/extra") {
		t.Fatal("review authentication bypass is not exact")
	}
	callback := httptest.NewRecorder()
	api.gmailSetupCallback(callback, httptest.NewRequest("GET", "https://app.example.com"+gmailOAuthCallbackPath+"?state="+job.ID+"&error=access_denied", nil))
	api.gmailSetup.mu.Lock()
	used := api.gmailSetup.jobs[job.ID]
	secretCleared := used.oauth.ClientSecret == "" && used.verifier == ""
	api.gmailSetup.mu.Unlock()
	if !secretCleared || used.Stage != "Setup cancelled" {
		t.Fatal("denied consent retained sensitive setup state")
	}
	second := httptest.NewRecorder()
	api.gmailSetupCallback(second, httptest.NewRequest("GET", "https://app.example.com"+gmailOAuthCallbackPath+"?state="+job.ID+"&code=second", nil))
	if !strings.Contains(second.Body.String(), "expired") {
		t.Fatal("reused consumed OAuth state")
	}
}

func TestGmailSetupReviewExpiryAndAdminRevocation(t *testing.T) {
	api, ctx := gmailSetupFixture(t)
	job, err := api.prepareGmailSetup(ctx, "app-project", "")
	if err != nil {
		t.Fatal(err)
	}
	api.gmailSetup.mu.Lock()
	api.gmailSetup.jobs[job.ID].Expires = time.Now().Add(-time.Minute)
	api.gmailSetup.mu.Unlock()
	expired := httptest.NewRecorder()
	api.gmailSetupReview(expired, httptest.NewRequest("GET", job.ReviewURL, nil))
	if expired.Code != 410 {
		t.Fatal("expired plan remained usable")
	}
	job, err = api.prepareGmailSetup(ctx, "app-project", "")
	if err != nil {
		t.Fatal(err)
	}
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","username":"admin","email":"admin@example.com","admin":false}]}`)
	denied := httptest.NewRecorder()
	api.gmailSetupReview(denied, httptest.NewRequest("GET", job.ReviewURL, nil))
	if denied.Code != 403 {
		t.Fatal("revoked administrator retained setup authority")
	}
}

func TestGmailSetupPrivateConfigSurvivesRestartAndRejectsEnvironmentConflicts(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("GMAIL_INBOUND_TOPICS", "")
	t.Setenv("GMAIL_INBOUND_AUDIENCE", "")
	t.Setenv("GMAIL_INBOUND_PUSH_EMAIL", "")
	p, err := gmailsetup.NewPlan("app-project", "123456-abcdef.apps.googleusercontent.com", "sample-project", "https://app.example.com/api/hooks/gmail/events", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = saveGmailSetupConfig(p); err != nil {
		t.Fatal(err)
	}
	path, _ := gmailSetupConfigPath()
	stat, _ := os.Stat(path)
	if stat.Mode().Perm() != 0600 {
		t.Fatalf("config permissions: %v", stat.Mode())
	}
	c, err := readGmailInboundConfig()
	if err != nil || c.Topics[p.ClientName] != p.Topic || c.Audience != p.Endpoint {
		t.Fatalf("persisted config: %+v %v", c, err)
	}
	second := p
	second.ClientName = "second-app"
	if err = saveGmailSetupConfig(second); err != nil {
		t.Fatal(err)
	}
	c, _ = readGmailInboundConfig()
	if len(c.Topics) != 2 {
		t.Fatal("new OAuth client dropped previous mapping")
	}
	t.Setenv("GMAIL_INBOUND_TOPICS", `{"other-app":"projects/other-project/topics/mail"}`)
	t.Setenv("GMAIL_INBOUND_AUDIENCE", "https://another.example/api/hooks/gmail/events")
	t.Setenv("GMAIL_INBOUND_PUSH_EMAIL", "other-push@other-project.iam.gserviceaccount.com")
	if _, err = readGmailInboundConfig(); err == nil {
		t.Fatal("environment identity silently replaced saved identity")
	}
	if err = saveGmailSetupConfig(p); err == nil {
		t.Fatal("setup overwrote conflicting operator configuration")
	}
}

func TestGmailSetupActivatesExistingServiceWithoutRestartButIngressStillRequiresOIDC(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("GMAIL_INBOUND_TOPICS", "")
	t.Setenv("GMAIL_INBOUND_AUDIENCE", "")
	t.Setenv("GMAIL_INBOUND_PUSH_EMAIL", "")
	api := &StreamingAPI{}
	router := mux.NewRouter()
	stop := api.initGmailInbound(router)
	defer stop()
	if api.gmailInbound == nil || api.gmailInbound.Enabled(context.Background()) {
		t.Fatal("unconfigured intake is enabled or cannot later be activated")
	}
	request := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("POST", gmailInboundEventPath, strings.NewReader(`{}`)))
		return w
	}
	if request().Code != 503 {
		t.Fatal("accepted ingress before setup")
	}
	p, err := gmailsetup.NewPlan("app-project", "123456-abcdef.apps.googleusercontent.com", "sample-project", "https://app.example.com/api/hooks/gmail/events", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = saveGmailSetupConfig(p); err != nil {
		t.Fatal(err)
	}
	if !api.gmailInbound.Enabled(context.Background()) {
		t.Fatal("saved setup requires a restart")
	}
	if request().Code != 401 {
		t.Fatal("activated ingress accepted an unsigned event")
	}
	// The queue path stays outside project storage and uses the deployment state root.
	path, _ := gmailSetupConfigPath()
	if !strings.HasPrefix(path, filepath.Clean(os.Getenv("AGENTWORKS_STATE_ROOT"))+string(os.PathSeparator)) {
		t.Fatal("configuration escaped private state")
	}
}
