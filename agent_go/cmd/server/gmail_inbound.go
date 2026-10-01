package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

const gmailInboundEventPath = "/api/hooks/gmail/events"

var inboundTopicPattern = regexp.MustCompile(`^projects/[a-z0-9][a-z0-9-]+/topics/[A-Za-z][A-Za-z0-9._~%+-]*$`)

type gmailInboundConfig struct {
	Topics    map[string]string
	Audience  string
	PushEmail string
}

func readGmailInboundConfig() (gmailInboundConfig, error) {
	c := gmailInboundConfig{Topics: map[string]string{}, Audience: strings.TrimSpace(os.Getenv("GMAIL_INBOUND_AUDIENCE")), PushEmail: strings.TrimSpace(os.Getenv("GMAIL_INBOUND_PUSH_EMAIL"))}
	raw := strings.TrimSpace(os.Getenv("GMAIL_INBOUND_TOPICS"))
	if raw == "" {
		return c, nil
	}
	if e := json.Unmarshal([]byte(raw), &c.Topics); e != nil {
		return c, fmt.Errorf("GMAIL_INBOUND_TOPICS must map OAuth client names to Pub/Sub topic names")
	}
	if c.Audience == "" || c.PushEmail == "" {
		return c, fmt.Errorf("GMAIL_INBOUND_AUDIENCE and GMAIL_INBOUND_PUSH_EMAIL are required")
	}
	u, e := url.Parse(c.Audience)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.Path != gmailInboundEventPath || u.RawQuery != "" || u.Fragment != "" {
		return c, fmt.Errorf("GMAIL_INBOUND_AUDIENCE must be the public HTTPS Gmail event URL")
	}
	for client, topic := range c.Topics {
		if client == "" || !inboundTopicPattern.MatchString(topic) {
			return c, fmt.Errorf("invalid Gmail inbound topic configuration")
		}
	}
	return c, nil
}

// initGmailInbound installs a single ingress and fixed pools for all accounts.
// A missing configuration leaves management readable but intake disabled.
func (api *StreamingAPI) initGmailInbound(router *mux.Router) func() {
	c, e := readGmailInboundConfig()
	if e != nil {
		log.Printf("[GMAIL-INBOUND] disabled: %v", e)
		c.Topics = nil
	}
	if len(c.Topics) > 0 {
		stateRoot, stateErr := workflowCLIStateRoot()
		if stateErr != nil {
			log.Printf("[GMAIL-INBOUND] cannot resolve private state: %v", stateErr)
			return func() {}
		}
		path := filepath.Join(stateRoot, "gmail-inbound", "email.db")
		store, e := gmailinbound.Open(path)
		if e != nil {
			log.Printf("[GMAIL-INBOUND] cannot open queue: %v", e)
		} else {
			v := &gmailinbound.PushVerifier{Audience: c.Audience, Email: c.PushEmail}
			s := &gmailinbound.Service{Store: store, Verify: v.Verify, Authorize: api.authorizeInboundEmail, Dispatch: api.dispatchInboundEmail, Reply: api.replyInboundEmail}
			s.Client = func(ctx context.Context, m gmailinbound.Mailbox) (gmailinbound.Client, error) {
				return inboundClientFor(m.ConnectionID, c)
			}
			api.gmailInbound = s
		}
	}
	router.HandleFunc(gmailInboundEventPath, func(w http.ResponseWriter, r *http.Request) {
		if api.gmailInbound == nil {
			http.Error(w, "Gmail intake is not configured", 503)
			return
		}
		api.gmailInbound.Receive(w, r)
	}).Methods("POST")
	router.HandleFunc("/api/gmail-inbound/route", api.gmailInboundRoute(c)).Methods("GET", "POST")
	if api.gmailInbound == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	api.gmailInbound.Start(ctx)
	var once sync.Once
	return func() { once.Do(func() { cancel(); api.gmailInbound.Wait(); _ = api.gmailInbound.Store.Close() }) }
}
func inboundClientFor(id string, c gmailInboundConfig) (services.GmailInboundClient, error) {
	svc := services.GetGmailService()
	if svc == nil {
		return services.GmailInboundClient{}, fmt.Errorf("Gmail unavailable")
	}
	conn, ok := svc.GetConnection(id)
	if !ok || !conn.Enabled {
		return services.GmailInboundClient{}, fmt.Errorf("Gmail connection unavailable")
	}
	topic := c.Topics[conn.ClientName]
	if topic == "" {
		return services.GmailInboundClient{}, fmt.Errorf("Pub/Sub is not configured for this account's OAuth client")
	}
	scope := services.GmailUseScope{}
	if conn.IsPrivate() {
		scope = services.GmailUseScope{CodeWorkspace: conn.ScopeWorkspace, UserID: conn.OwnerID}
	}
	return services.GmailInboundClient{ConnectionID: id, Topic: topic, Scope: scope}, nil
}
func inboundAddress(account, id string) (string, error) {
	a, e := mail.ParseAddress(account)
	if e != nil {
		return "", fmt.Errorf("Gmail account has no verified email")
	}
	parts := strings.SplitN(strings.ToLower(a.Address), "@", 2)
	base := strings.SplitN(parts[0], "+", 2)[0]
	local := base + "+agent-" + strings.ReplaceAll(id, "-", "")
	if len(local) > 64 {
		return "", fmt.Errorf("Gmail account name is too long for a project address")
	}
	return local + "@" + parts[1], nil
}

func (api *StreamingAPI) inboundTarget(ctx context.Context, owner, path string) (gmailinbound.Route, error) {
	r := gmailinbound.Route{OwnerID: owner, WorkspacePath: canonicalCrewWorkspaceRoot(path)}
	if r.WorkspacePath == "" || strings.Contains(r.WorkspacePath, "..") {
		return r, fmt.Errorf("invalid project workspace")
	}
	if isProjectWorkspacePath(r.WorkspacePath) {
		if !crewProjectOwnedByCaller(owner, r.WorkspacePath) {
			return r, fmt.Errorf("only the project owner can configure incoming email")
		}
		r.ProfileID = "work"
		if common.CodeProjectRoot(owner, r.WorkspacePath) != "" {
			r.ProfileID = "code"
		}
		if api.productSchedules == nil {
			return r, fmt.Errorf("project service unavailable")
		}
		metadataRaw, found, e := api.productSchedules.readFile(ctx, filepath.ToSlash(filepath.Join(agentProfileRuntimeWorkspace(owner, r.WorkspacePath), "product.json")))
		var metadata productProjectManifest
		if e != nil || !found || json.Unmarshal([]byte(metadataRaw), &metadata) != nil || metadata.ID == "" {
			return r, fmt.Errorf("project manifest not found")
		}
		profile, binding, manifest, e := api.productSchedules.projectManifest(ctx, owner, r.ProfileID, metadata.ID)
		if e != nil {
			return r, e
		}
		if normalizeConversationWorkspace(binding.WorkspacePath) != normalizeConversationWorkspace(r.WorkspacePath) {
			return r, fmt.Errorf("project workspace does not match")
		}
		if !userAllowedProduct(GetUserFromContext(internalBotRequestContext(ctx, owner)), profile.Product) {
			return r, fmt.Errorf("project access unavailable")
		}
		r.ProjectID = manifest.ID
		r.WorkspacePath = agentProfileRuntimeWorkspace(owner, binding.WorkspacePath)
		return r, nil
	}
	claims := GetUserFromContext(internalBotRequestContext(ctx, owner))
	level, m := workflowAccessForWorkspacePath(ctx, claims, r.WorkspacePath)
	if m == nil || m.ID == "" || m.Kind == "relay" || (level != WorkflowAccessOwner && level != WorkflowAccessWrite) {
		return r, fmt.Errorf("workflow access unavailable")
	}
	if m.hasOwnershipRecord() && !containsID(m.effectiveOwners(), owner) {
		return r, fmt.Errorf("only a workflow owner can configure incoming email")
	}
	r.ProjectID = m.ID
	return r, nil
}
func (api *StreamingAPI) gmailInboundRoute(config gmailInboundConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		owner := productWorkspaceUserID(r.Context())
		claims := GetUserFromContext(r.Context())
		if claims != nil && (claims.Provider == "bot_route" || claims.AccessToken != nil) {
			http.Error(w, "interactive sign-in required", 403)
			return
		}
		path := r.URL.Query().Get("workspace_path")
		var input struct {
			WorkspacePath string `json:"workspace_path"`
			ConnectionID  string `json:"connection_id"`
			Enabled       bool   `json:"enabled"`
			Reply         bool   `json:"reply"`
		}
		if r.Method == "POST" {
			decoder := json.NewDecoder(io.LimitReader(r.Body, 8192))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&input) != nil {
				http.Error(w, "invalid email route", 400)
				return
			}
			path = input.WorkspacePath
		}
		target, e := api.inboundTarget(r.Context(), owner, path)
		if e != nil {
			http.Error(w, e.Error(), 403)
			return
		}
		var existing *gmailinbound.Route
		if api.gmailInbound != nil {
			routes, e := api.gmailInbound.Store.Routes(r.Context())
			if e != nil {
				http.Error(w, "cannot read email routes", 500)
				return
			}
			for _, route := range routes {
				if route.OwnerID == owner && route.WorkspacePath == target.WorkspacePath {
					copy := route
					existing = &copy
					break
				}
			}
		}
		if r.Method == "POST" {
			if api.gmailInbound == nil {
				http.Error(w, "An administrator must configure Gmail Pub/Sub on this server first", 409)
				return
			}
			// Disabling never needs the mailbox credential to still exist.
			if !input.Enabled && existing != nil {
				paused := *existing
				paused.Enabled = false
				paused.Reply = input.Reply
				box, e := api.gmailInbound.Store.MailboxStatus(r.Context(), paused.ConnectionID)
				if e != nil || api.gmailInbound.Store.SaveRoute(r.Context(), paused, box.Email) != nil {
					http.Error(w, "cannot disable email route", 500)
					return
				}
				writeAgentProfileJSON(w, 200, map[string]interface{}{"configured": true, "route": paused, "deliveries": []gmailinbound.DeliveryStatus{}})
				return
			}
			id := uuid.NewString()
			if existing != nil {
				id = existing.ID
			}
			target.ID = id
			target.ConnectionID = input.ConnectionID
			target.Enabled = input.Enabled
			if existing != nil {
				target.EnabledAt = existing.EnabledAt
			}
			if input.Enabled && (existing == nil || !existing.Enabled || existing.ConnectionID != input.ConnectionID) {
				target.EnabledAt = time.Now().UnixMilli()
			}
			target.Reply = input.Reply
			svc := services.GetGmailService()
			if svc == nil {
				http.Error(w, "Gmail unavailable", 409)
				return
			}
			conn, ok := svc.GetConnection(input.ConnectionID)
			if !ok {
				http.Error(w, "Gmail connection unavailable", 404)
				return
			}
			scope, e := gmailRequestScope(r, target.WorkspacePath)
			if e != nil || !conn.UsableFrom(scope) {
				http.Error(w, "Gmail connection unavailable", 404)
				return
			}
			// Shared credentials remain administrator-managed. A non-admin may
			// attach only their recorded or Google-discovered own mailbox.
			if !conn.IsPrivate() && !currentUserIsAdmin(r) && !canRemoveGmailConnection(r, conn) {
				http.Error(w, "only the mailbox owner can enable incoming email", 403)
				return
			}
			if input.Enabled {
				if _, e = inboundClientFor(conn.ID, config); e != nil {
					http.Error(w, e.Error(), 409)
					return
				}
				if !conn.AllowReadAccess {
					http.Error(w, "Enable Gmail read access and reconnect this account first", 409)
					return
				}
				status, _ := svc.AuthStatusForConnectionBlocking(r.Context(), conn.ID)
				if !services.GoogleScopesGrant(status.Scopes, services.GmailReadonlyScope) {
					http.Error(w, "Gmail read access has not been granted; reconnect the account", 409)
					return
				}
			}
			target.Address, e = inboundAddress(conn.Email, id)
			if e != nil {
				http.Error(w, e.Error(), 409)
				return
			}
			if e = api.gmailInbound.Store.SaveRoute(r.Context(), target, strings.ToLower(conn.Email)); e != nil {
				http.Error(w, "cannot save email route", 500)
				return
			}
			existing = &target
		}
		response := map[string]interface{}{"configured": api.gmailInbound != nil, "route": existing, "deliveries": []gmailinbound.DeliveryStatus{}}
		if existing != nil {
			if m, e := api.gmailInbound.Store.MailboxStatus(r.Context(), existing.ConnectionID); e == nil {
				response["watch_ready"] = m.Cursor != ""
				response["error"] = m.LastError
			}
			history, _ := api.gmailInbound.Store.History(r.Context(), existing.ID)
			response["deliveries"] = history
		}
		writeAgentProfileJSON(w, 200, response)
	}
}

func (api *StreamingAPI) authorizeInboundEmail(ctx context.Context, r gmailinbound.Route, m gmailinbound.Message) error {
	if r.EnabledAt > 0 && m.ReceivedAt < r.EnabledAt {
		return fmt.Errorf("email predates activation of this address")
	}
	matched := false
	for _, to := range m.Recipients {
		if strings.EqualFold(to, r.Address) {
			matched = true
		}
	}
	if !matched {
		return fmt.Errorf("email receiving address changed")
	}
	if !m.Authenticated || m.Automatic {
		return fmt.Errorf("sender could not be authenticated")
	}
	user := directoryUserFor(r.OwnerID, "", "")
	if user != nil && user.Disabled {
		return fmt.Errorf("account disabled")
	}
	svc := services.GetGmailService()
	if svc == nil {
		return fmt.Errorf("Gmail unavailable")
	}
	conn, ok := svc.GetConnection(r.ConnectionID)
	if !ok || !conn.Enabled || !conn.AllowReadAccess {
		return fmt.Errorf("Gmail connection unavailable")
	}
	address, e := inboundAddress(conn.Email, r.ID)
	if e != nil || address != r.Address {
		return fmt.Errorf("Gmail receiving identity changed")
	}
	// v1 accepts the project owner only. An alias never grants another user
	// the owner's private Code, tools, credentials, or budget.
	if IsMultiUserMode() {
		sender, ok := slackDMUserForEmail(m.From)
		if !ok || sender != r.OwnerID {
			return fmt.Errorf("only the project owner can email this address")
		}
	} else if !strings.EqualFold(m.From, conn.Email) && (user == nil || !strings.EqualFold(m.From, user.Email)) {
		return fmt.Errorf("sender is not the project owner")
	}
	if conn.IsPrivate() && (conn.OwnerID != sanitizeUserIDForPath(r.OwnerID) || common.CodeProjectRoot(r.OwnerID, r.WorkspacePath) != conn.ScopeWorkspace) {
		return fmt.Errorf("private Gmail connection scope changed")
	}
	target, e := api.inboundTarget(ctx, r.OwnerID, r.WorkspacePath)
	if e != nil {
		return e
	}
	if target.ProjectID != r.ProjectID || target.ProfileID != r.ProfileID {
		return fmt.Errorf("email target changed")
	}
	return nil
}

func emailConversationID(r gmailinbound.Route, m gmailinbound.Message) string {
	h := sha256.Sum256([]byte(r.ID + "\x00" + m.ThreadID + "\x00" + m.From))
	return "email-" + hex.EncodeToString(h[:16])
}
func (api *StreamingAPI) dispatchInboundEmail(ctx context.Context, d *gmailinbound.Delivery) error {
	if e := api.authorizeInboundEmail(ctx, d.Route, d.Message); e != nil {
		return e
	}
	r := d.Route
	userCtx := internalBotRequestContext(ctx, r.OwnerID)
	query := "Email subject: " + d.Message.Subject + "\n\n" + d.Message.Body
	if len(d.Message.Attachments) > 0 {
		config, e := readGmailInboundConfig()
		if e != nil {
			return e
		}
		client, e := inboundClientFor(r.ConnectionID, config)
		if e != nil {
			return e
		}
		url := os.Getenv("WORKSPACE_API_URL")
		if url == "" {
			url = "http://127.0.0.1:8081"
		}
		ws := workspace.NewClient(url)
		uploadCtx := context.WithValue(userCtx, common.UserIDKey, r.OwnerID)
		total := 0
		for i, a := range d.Message.Attachments {
			data, e := client.Attachment(ctx, d.Message.ID, a)
			if e != nil {
				return e
			}
			total += len(data)
			if total > 20*1024*1024 {
				return fmt.Errorf("email attachments exceed 20 MiB")
			}
			name := fmt.Sprintf("%02d-%s", i, filepath.Base(strings.ReplaceAll(a.Name, "\\", "/")))
			path, e := ws.UploadBinary(uploadCtx, filepath.ToSlash(filepath.Join(r.WorkspacePath, "uploads", "email", d.Message.ID)), name, data)
			if e != nil {
				return e
			}
			query += "\nAttached file: " + path
		}
	}
	key := emailConversationID(r, d.Message)
	var req map[string]interface{}
	if r.ProfileID != "" {
		profile, e := api.agentProfiles.Resolve(r.ProfileID, 0, r.OwnerID)
		if e != nil {
			return e
		}
		binding, e := resolveIsolatedProjectAutomationBinding(userCtx, r.OwnerID, profile, r.ProjectID, "trigger", key, d.Message.Subject)
		if e != nil {
			return e
		}
		conversation, e := defaultProductConversationRegistryStore().resolveOrCreate(userCtx, r.OwnerID, profile, binding, "")
		if e != nil {
			return e
		}
		d.SessionID = conversation.SessionID
		turn, e := prepareProductConversationTurn(userCtx, r.OwnerID, profile, AgentProfileChatRequest{Message: query}, conversation)
		if e != nil {
			return e
		}
		req, e = queryRequestToMap(turn)
		if e != nil {
			return e
		}
		if r.ProfileID == "work" {
			req["workshop_mode"] = "run"
			if options, ok := req["execution_options"].(map[string]interface{}); ok {
				options["workshop_mode"] = "run"
			}
		}
		if turn.resolvedResumeTarget != nil {
			req["_trusted_resume_target"] = turn.resolvedResumeTarget
		}
	} else {
		_, m := workflowAccessForWorkspacePath(userCtx, GetUserFromContext(userCtx), r.WorkspacePath)
		if m == nil || api.scheduler == nil {
			return fmt.Errorf("workflow unavailable")
		}
		d.SessionID = key
		req = api.scheduler.buildWorkshopRequest(userCtx, &ScheduleContext{WorkspacePath: r.WorkspacePath, WorkflowID: r.ProjectID, WorkflowLabel: m.Label, OwnerUserID: r.OwnerID, Capabilities: m.Capabilities, Schedule: WorkflowSchedule{Name: d.Message.Subject}, TriggerSource: "email"})
		req["query"] = "Run the saved workflow using this email as the request. Collect missing required inputs in this conversation.\n\n" + query
		req["workshop_mode"] = "run"
		req["execution_options"].(map[string]interface{})["workshop_mode"] = "run"
		delete(req, "disable_live_input_delivery")
	}
	deliveryHash := sha256.Sum256([]byte(d.ID))
	req["submission_id"] = "gmail-" + hex.EncodeToString(deliveryHash[:])
	req["triggered_by"] = "email"
	req["session_title"] = d.Message.Subject
	if e := api.gmailInbound.Store.Finish(ctx, *d, "running", nil); e != nil {
		return e
	}
	result, e := api.startSessionInternalWithResult(userCtx, req, d.SessionID, r.OwnerID, nil)
	d.Response = result.FinalResponse
	return e
}
func (api *StreamingAPI) replyInboundEmail(ctx context.Context, d gmailinbound.Delivery) error {
	if e := api.authorizeInboundEmail(ctx, d.Route, d.Message); e != nil {
		return e
	}
	config, e := readGmailInboundConfig()
	if e != nil {
		return e
	}
	c, e := inboundClientFor(d.Route.ConnectionID, config)
	if e != nil {
		return e
	}
	conn, _ := services.GetGmailService().GetConnection(d.Route.ConnectionID)
	return c.Reply(ctx, d, conn.Email)
}
