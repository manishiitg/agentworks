package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailsetup"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const gmailSetupStartPath = "/api/gmail-inbound/setup/start"
const gmailSetupStatePrefix = "gmail-setup-"

type gmailSetupJob struct {
	ID            string          `json:"id"`
	Plan          gmailsetup.Plan `json:"plan"`
	Stage         string          `json:"stage"`
	Error         string          `json:"error,omitempty"`
	Expires       time.Time       `json:"expires_at"`
	ReviewURL     string          `json:"review_url,omitempty"`
	owner         UserClaims
	clientID      string
	oauth         oauth2.Config
	verifier      string
	started, used bool
}

type gmailSetupManager struct {
	mu              sync.Mutex
	jobs            map[string]*gmailSetupJob
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	running, closed bool
}

func (m *gmailSetupManager) stop() {
	m.mu.Lock()
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
}

func gmailSetupAdmin(ctx context.Context) bool {
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.UserID == "" || (claims.Provider == "bot_route" || claims.Provider == "bot_owner" || claims.Provider == slackDMProvider) || claims.ExecutionPrincipal != nil || claims.AccessToken != nil || claims.Scope != "" || claims.BotRouteGrant != "" || claims.ExternalBuilderOperationID != "" {
		return false
	}
	r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	return !directoryUserIsUnknown(claims) && !directoryUserIsDisabled(claims) && currentUserIsAdmin(r)
}

func gmailSetupClients() []services.GmailOAuthClient {
	clients, _ := services.ListOAuthClients()
	if id, _, ok := platformGoogleApp(); ok {
		// The company app is resolved from its authoritative admin registration.
		filtered := clients[:0]
		for _, c := range clients {
			if c.Name != services.PlatformGoogleClientName {
				filtered = append(filtered, c)
			}
		}
		clients = filtered
		clients = append(clients, services.GmailOAuthClient{Name: services.PlatformGoogleClientName, ClientID: id})
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i].Name < clients[j].Name })
	return clients
}

func (api *StreamingAPI) gmailSetupStatus(ctx context.Context) map[string]any {
	admin := gmailSetupAdmin(ctx)
	result := map[string]any{"available": api.gmailInbound != nil, "can_prepare": admin, "oauth_clients": []services.GmailOAuthClient{}}
	if !admin {
		return result
	}
	result["oauth_clients"] = gmailSetupClients()
	api.gmailSetup.mu.Lock()
	defer api.gmailSetup.mu.Unlock()
	for _, job := range api.gmailSetup.jobs {
		if job.owner.UserID == GetUserFromContext(ctx).UserID {
			copy := *job
			if !job.used && time.Now().After(job.Expires) {
				copy.Stage = "Consent link expired"
				copy.ReviewURL = ""
			}
			result["job"] = &copy
		}
	}
	return result
}

func (api *StreamingAPI) registerGmailSetupTool(reg definitionToolRegistrar, session string) error {
	return reg.RegisterCustomTool("setup_gmail_inbound", "Prepare this deployment's one-time automatic Gmail setup, or inspect its progress. Only an interactive app administrator can use it; ordinary Gmail connections never provision cloud resources. Read get_gmail_trigger first. action=prepare takes a registered client_name (automatically selected if exactly one) and that OAuth client's Google Cloud project_id (use registered metadata when available; ask for the project ID if unknown, never guess). Return the reviewed plan and review_url. The administrator must OPEN the review URL themselves, review its Cloud and server changes and sign in/consent with Google project setup permissions. Never follow the URL or complete consent through agent tools. After that human step the server creates/verifies APIs, topic, narrow IAM grants, authenticated push subscription and private server configuration, activating intake without environment edits or a restart. No Cloud credentials/refresh tokens enter chat or gog. action=status reports progress; configured intake is separate from real mailbox delivery. Local needs an existing public HTTPS tunnel configured in PUBLIC_URL. Do not create a schedule or launch gog watchers as a substitute.", map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"action"}, "properties": map[string]any{
			"action":      map[string]any{"type": "string", "enum": []string{"prepare", "status"}},
			"client_name": map[string]any{"type": "string"}, "project_id": map[string]any{"type": "string"},
		},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		active, _ := api.getActiveSession(session)
		policy := resolveWorkflowChatPolicy(session, QueryRequest{}, active, false)
		if !gmailSetupAdmin(ctx) || policy.Origin != "interactive" || active != nil && active.TriggeredBy == "email" {
			return "", fmt.Errorf("Gmail infrastructure setup requires an interactive administrator conversation")
		}
		for key, value := range args {
			if key != "action" && key != "client_name" && key != "project_id" {
				return "", fmt.Errorf("unknown setup argument")
			}
			if _, ok := value.(string); !ok {
				return "", fmt.Errorf("setup arguments must be strings")
			}
		}
		action, _ := args["action"].(string)
		if action == "status" {
			raw, _ := json.Marshal(api.gmailSetupStatus(ctx))
			return string(raw), nil
		}
		if action != "prepare" {
			return "", fmt.Errorf("action must be prepare or status")
		}
		client, _ := args["client_name"].(string)
		project, _ := args["project_id"].(string)
		job, err := api.prepareGmailSetup(ctx, strings.TrimSpace(client), strings.TrimSpace(project))
		if err != nil {
			return "", err
		}
		raw, _ := json.Marshal(job)
		return string(raw), nil
	}, "gmail_connection_management")
}

func (api *StreamingAPI) prepareGmailSetup(ctx context.Context, client, project string) (*gmailSetupJob, error) {
	if !gmailSetupAdmin(ctx) {
		return nil, fmt.Errorf("administrator access is required")
	}
	if api.gmailInbound == nil {
		return nil, fmt.Errorf("the server's private Gmail state store is unavailable; an operator must repair it first")
	}
	config, err := readGmailInboundConfig()
	if err != nil {
		return nil, err
	}
	clients := gmailSetupClients()
	if client == "" && len(clients) == 1 {
		client = clients[0].Name
	}
	var selected *services.GmailOAuthClient
	for i := range clients {
		if clients[i].Name == client {
			selected = &clients[i]
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("choose an existing Google OAuth app from setup.provisioning.oauth_clients; configure a company app or upload your OAuth JSON if none exists")
	}
	if project == "" {
		project = selected.ProjectID
	}
	id, secret := "", ""
	if client == services.PlatformGoogleClientName {
		id, secret, _ = platformGoogleApp()
	} else {
		id, secret, err = services.GetOAuthClientSecret(client)
	}
	if err != nil || id == "" || secret == "" {
		return nil, fmt.Errorf("the selected Google app is unavailable")
	}
	endpoint, _ := gmailInboundAdminSetup(config)["push_endpoint"].(string)
	plan, err := gmailsetup.NewPlan(client, id, project, endpoint, config.Topics[client], config.PushEmail)
	if err != nil {
		return nil, err
	}
	// Resolve callbacks from the trusted deployment URL, never agent input.
	base := strings.TrimSuffix(endpoint, gmailInboundEventPath)
	callback := gmailOAuthCallbackPath
	if client == services.PlatformGoogleClientName {
		callback = "/api/oauth/callback"
	}
	job := &gmailSetupJob{ID: gmailSetupStatePrefix + uuid.NewString(), Plan: plan, Stage: "Waiting for administrator review and Google consent", Expires: time.Now().Add(15 * time.Minute), owner: *GetUserFromContext(ctx), clientID: id, verifier: oauth2.GenerateVerifier(), oauth: oauth2.Config{ClientID: id, ClientSecret: secret, RedirectURL: base + callback, Endpoint: google.Endpoint, Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"}}}
	job.ReviewURL = base + gmailSetupStartPath + "?plan_id=" + url.QueryEscape(job.ID)
	m := &api.gmailSetup
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.running {
		return nil, fmt.Errorf("Gmail setup is already running or the server is stopping; inspect status")
	}
	if m.jobs == nil {
		m.jobs = map[string]*gmailSetupJob{}
		m.ctx, m.cancel = context.WithCancel(context.Background())
	}
	for key, old := range m.jobs {
		if old.owner.UserID == job.owner.UserID || time.Now().After(old.Expires) {
			delete(m.jobs, key)
		}
	}
	if len(m.jobs) >= 16 {
		return nil, fmt.Errorf("too many pending setup reviews; retry after they expire")
	}
	m.jobs[job.ID] = job
	copy := *job
	return &copy, nil
}

var gmailSetupReviewTemplate = template.Must(template.New("gmail-setup").Parse(`<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"><title>Set up incoming Gmail</title><style>body{font:16px system-ui;background:#111719;color:#eef3f4;margin:0;padding:32px}main{max-width:680px;margin:auto}code{overflow-wrap:anywhere}li{margin:12px 0}button{padding:12px 18px;border:0;border-radius:8px;background:#3b8198;color:white;font:inherit;cursor:pointer}p{line-height:1.6;color:#bdc9cc}</style></head><body><main><h1>Set up incoming Gmail</h1><p>Review this one-time setup for your AgentWorks server. Continue with a Google account permitted to configure these projects.</p><ul><li>OAuth app: <code>{{.Plan.ClientName}}</code>; project: <code>{{.Plan.ProjectID}}</code></li><li>Gmail topic: <code>{{.Plan.Topic}}</code></li><li>Push subscription: <code>{{.Plan.Subscription}}</code></li><li>Push identity: <code>{{.Plan.PushEmail}}</code></li><li>This server's event URL and audience: <code>{{.Plan.Endpoint}}</code></li></ul><p>After Google consent, AgentWorks will enable the required APIs, create or reuse these resources, grant Gmail permission to publish to this topic and grant Pub/Sub permission to mint tokens for this push identity. It will save this server's private receiving configuration and activate it without a restart. Cloud resources may incur Google charges.</p><p>Your mailbox connections, email rules and sender approvals are configured separately through Builder. Cloud authorization is used only for this setup; no Cloud refresh token is stored or given to an agent.</p><form method="post"><input type="hidden" name="plan_id" value="{{.ID}}"><button type="submit">Continue with Google and set up receiving</button></form></main></body></html>`))

func (api *StreamingAPI) initGmailSetup(router *mux.Router) {
	router.HandleFunc(gmailSetupStartPath, api.gmailSetupReview).Methods("GET", "POST")
}

func (api *StreamingAPI) gmailSetupReview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	// Keep the same-origin POST origin while suppressing the review URL on Google navigation.
	w.Header().Set("Referrer-Policy", "same-origin")
	// Browsers also check form-action on the POST's redirect to Google consent.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self' https://accounts.google.com; frame-ancestors 'none'")
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid setup review", 400)
		return
	}
	m := &api.gmailSetup
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[r.FormValue("plan_id")]
	if job == nil || job.used || time.Now().After(job.Expires) || m.closed {
		http.Error(w, "This setup review expired. Ask Builder to prepare a new one.", 410)
		return
	}
	ownerCtx := context.WithValue(r.Context(), UserContextKey, &job.owner)
	if !gmailSetupAdmin(ownerCtx) {
		http.Error(w, "Administrator access was revoked.", 403)
		return
	}
	if r.Method == "POST" {
		// The review is an expiring capability to begin Google consent only.
		// It cannot authorize provisioning without Google's one-use code + PKCE.
		if origin := r.Header.Get("Origin"); origin != "" {
			u, _ := url.Parse(job.ReviewURL)
			if u == nil || origin != u.Scheme+"://"+u.Host {
				http.Error(w, "Invalid review origin", 403)
				return
			}
		}
		job.started = true
		authURL := job.oauth.AuthCodeURL(job.ID, oauth2.S256ChallengeOption(job.verifier), oauth2.SetAuthURLParam("prompt", "consent select_account"), oauth2.SetAuthURLParam("include_granted_scopes", "false"))
		http.Redirect(w, r, authURL, http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = gmailSetupReviewTemplate.Execute(w, job)
}

func gmailSetupPlanCurrent(job *gmailSetupJob) error {
	config, err := readGmailInboundConfig()
	if err != nil {
		return err
	}
	endpoint, _ := gmailInboundAdminSetup(config)["push_endpoint"].(string)
	if endpoint != job.Plan.Endpoint {
		return fmt.Errorf("this deployment's public URL changed; prepare a new setup plan")
	}
	id := ""
	if job.Plan.ClientName == services.PlatformGoogleClientName {
		id, _, _ = platformGoogleApp()
	} else {
		id, _, err = services.GetOAuthClientSecret(job.Plan.ClientName)
	}
	if err != nil || id != job.clientID {
		return fmt.Errorf("the selected OAuth app changed or was removed; prepare a new setup plan")
	}
	_, err = mergeGmailInboundConfig(config, gmailInboundConfig{Topics: map[string]string{job.Plan.ClientName: job.Plan.Topic}, Audience: job.Plan.Endpoint, PushEmail: job.Plan.PushEmail})
	return err
}

func (api *StreamingAPI) gmailSetupCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	m := &api.gmailSetup
	m.mu.Lock()
	job := m.jobs[r.URL.Query().Get("state")]
	if job == nil || job.used || !job.started || time.Now().After(job.Expires) || m.closed {
		m.mu.Unlock()
		writeGmailOAuthPage(w, false, "Setup link expired", "Ask Builder to prepare a new setup link.")
		return
	}
	job.used = true
	job.ReviewURL = ""
	oauth, verifier := job.oauth, job.verifier
	job.oauth = oauth2.Config{}
	job.verifier = ""
	ownerCtx := context.WithValue(r.Context(), UserContextKey, &job.owner)
	if !gmailSetupAdmin(ownerCtx) || m.running || r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" {
		job.Stage = "Setup cancelled"
		m.mu.Unlock()
		writeGmailOAuthPage(w, false, "Setup cancelled", "No Cloud resources were changed. Ask Builder to prepare a new setup link when ready.")
		return
	}
	if err := gmailSetupPlanCurrent(job); err != nil {
		job.Stage = "Setup failed"
		job.Error = err.Error()
		m.mu.Unlock()
		writeGmailOAuthPage(w, false, "Setup changed", html.EscapeString(job.Error))
		return
	}
	m.running = true
	job.Stage = "Completing Google consent"
	m.wg.Add(1)
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	token, err := oauth.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(verifier))
	cancel()
	if err != nil {
		m.mu.Lock()
		m.running = false
		job.Stage = "Setup failed"
		job.Error = "Google consent could not be completed. Check the OAuth app's callback URL and Cloud consent policy, then prepare a new link."
		m.mu.Unlock()
		m.wg.Done()
		writeGmailOAuthPage(w, false, "Setup failed", html.EscapeString(job.Error))
		return
	}
	// Never request/store offline Cloud access, even if Google returns an existing
	// grant's refresh token. Gmail/gog's connection tokens are untouched.
	cloudToken := &oauth2.Token{AccessToken: token.AccessToken, TokenType: token.TokenType, Expiry: token.Expiry}
	go api.runGmailSetup(job, cloudToken)
	writeGmailOAuthPage(w, true, "Incoming Gmail setup started", "Return to AgentWorks. Ask Builder to check setup progress, then connect your mailbox and configure its incoming email rules. Resource verification is separate from testing real email delivery.")
}

func (api *StreamingAPI) runGmailSetup(job *gmailSetupJob, token *oauth2.Token) {
	m := &api.gmailSetup
	defer m.wg.Done()
	ctx, cancel := context.WithTimeout(m.ctx, 4*time.Minute)
	defer cancel()
	progress := func(stage string) { m.mu.Lock(); job.Stage = stage; m.mu.Unlock() }
	ownerCtx := context.WithValue(ctx, UserContextKey, &job.owner)
	var err error
	if !gmailSetupAdmin(ownerCtx) {
		err = fmt.Errorf("administrator access was revoked")
	} else if err = gmailSetupPlanCurrent(job); err != nil {
		// No cloud request is made with a stale deployment/app plan.
	} else {
		client := oauth2.NewClient(ctx, oauth2.StaticTokenSource(token))
		client.Timeout = 30 * time.Second
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client.Transport = gmailSetupAdminTransport{base: client.Transport, ctx: ownerCtx}
		err = (gmailsetup.Cloud{Client: client}).Provision(ctx, job.Plan, progress)
	}
	if err == nil && !gmailSetupAdmin(ownerCtx) {
		err = fmt.Errorf("administrator access was revoked; Cloud resources remain, but server receiving was not activated")
	}
	if err == nil {
		err = gmailSetupPlanCurrent(job)
	}
	if err == nil {
		progress("Saving and activating server configuration")
		err = saveGmailSetupConfig(job.Plan)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.running = false
	if err != nil {
		job.Stage = "Setup failed"
		job.Error = err.Error()
	} else {
		job.Stage = "Receiving infrastructure ready"
	}
}

// Revocation also stops subsequent Cloud operations during an active setup.
type gmailSetupAdminTransport struct {
	base http.RoundTripper
	ctx  context.Context
}

func (t gmailSetupAdminTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !gmailSetupAdmin(t.ctx) {
		return nil, fmt.Errorf("administrator access was revoked")
	}
	return t.base.RoundTrip(r)
}
