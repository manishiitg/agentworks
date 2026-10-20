package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
	"log"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const gmailInboundEventPath = "/api/hooks/gmail/events"

var inboundTopicPattern = regexp.MustCompile(`^projects/[a-z0-9][a-z0-9-]+/topics/[A-Za-z][A-Za-z0-9._~%+-]*$`)

type gmailInboundConfig struct {
	Enabled      bool              `json:"enabled"`
	ConnectionID string            `json:"mailbox_connection_id"`
	Topics       map[string]string `json:"topics"`
	Audience     string            `json:"audience"`
	PushEmail    string            `json:"push_email"`
}

func readGmailInboundConfig() (gmailInboundConfig, error) {
	c := gmailInboundConfig{Enabled: os.Getenv("AGENT_INBOX_ENABLED") == "true", ConnectionID: strings.TrimSpace(os.Getenv("AGENT_INBOX_CONNECTION_ID")), Topics: map[string]string{}, Audience: strings.TrimSpace(os.Getenv("GMAIL_INBOUND_AUDIENCE")), PushEmail: strings.TrimSpace(os.Getenv("GMAIL_INBOUND_PUSH_EMAIL"))}
	raw := strings.TrimSpace(os.Getenv("GMAIL_INBOUND_TOPICS"))
	if raw == "" {
		return loadGmailSetupConfig(c)
	}
	if e := json.Unmarshal([]byte(raw), &c.Topics); e != nil {
		return c, fmt.Errorf("GMAIL_INBOUND_TOPICS must map OAuth client names to Pub/Sub topic names")
	}
	if err := validateGmailInboundConfig(c); err != nil {
		return c, err
	}
	return loadGmailSetupConfig(c)
}

func validateGmailInboundConfig(c gmailInboundConfig) error {
	if c.Audience == "" || c.PushEmail == "" {
		return fmt.Errorf("GMAIL_INBOUND_AUDIENCE and GMAIL_INBOUND_PUSH_EMAIL are required")
	}
	u, e := url.Parse(c.Audience)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != gmailInboundEventPath || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("GMAIL_INBOUND_AUDIENCE must be the public HTTPS Gmail event URL")
	}
	for client, topic := range c.Topics {
		if client == "" || !inboundTopicPattern.MatchString(topic) {
			return fmt.Errorf("invalid Gmail inbound topic configuration")
		}
	}
	return nil
}

// One company mailbox feeds all logical agent inboxes. Personal connections are never watched.
func companyInboxClient(c gmailInboundConfig) (services.GmailInboundClient, string, error) {
	svc := services.GetGmailService()
	if svc == nil {
		return services.GmailInboundClient{}, "", fmt.Errorf("Connect the company receiving mailbox in My Vault first")
	}
	conn, found := svc.GetConnection(c.ConnectionID)
	if !found || conn.IsPrivate() || !conn.Enabled || !conn.AllowReadAccess {
		return services.GmailInboundClient{}, "", fmt.Errorf("Choose a connected company Google account with Gmail read access")
	}
	topic := c.Topics[conn.ClientName]
	if topic == "" {
		return services.GmailInboundClient{}, "", fmt.Errorf("Set up receiving for the company mailbox's Google app")
	}
	return services.GmailInboundClient{ConnectionID: conn.ID, Topic: topic}, conn.Email, nil
}
func (api *StreamingAPI) initGmailInbound(router *mux.Router) func() {
	if IsLocalMode() {
		return func() {}
	}
	root, err := workflowCLIStateRoot()
	if err != nil {
		log.Printf("[AGENT-INBOX] state unavailable")
		return func() {}
	}
	store, err := gmailinbound.Open(filepath.Join(root, "gmail-inbound", "agent-email.db"))
	if err != nil {
		log.Printf("[AGENT-INBOX] store unavailable: %v", err)
		return func() {}
	}
	var mu sync.Mutex
	var verifier *gmailinbound.PushVerifier
	service := &gmailinbound.Service{Store: store, Enabled: func(ctx context.Context) bool {
		cfg, err := readGmailInboundConfig()
		if err != nil || !cfg.Enabled {
			return false
		}
		_, email, err := companyInboxClient(cfg)
		if err != nil {
			return false
		}
		return store.SetMailbox(ctx, cfg.ConnectionID, email) == nil
	}, Verify: func(ctx context.Context, token string) error {
		cfg, err := readGmailInboundConfig()
		if err != nil || !cfg.Enabled {
			return fmt.Errorf("Agent inboxes are not enabled")
		}
		mu.Lock()
		if verifier == nil || verifier.Audience != cfg.Audience || verifier.Email != cfg.PushEmail {
			verifier = &gmailinbound.PushVerifier{Audience: cfg.Audience, Email: cfg.PushEmail}
		}
		v := verifier
		mu.Unlock()
		return v.Verify(ctx, token)
	}, Client: func(ctx context.Context, m gmailinbound.Mailbox) (gmailinbound.Client, error) {
		cfg, err := readGmailInboundConfig()
		if err != nil {
			return nil, err
		}
		if !cfg.Enabled || m.ConnectionID != cfg.ConnectionID {
			return nil, fmt.Errorf("Company receiving mailbox changed")
		}
		client, _, err := companyInboxClient(cfg)
		return client, err
	},
		Inboxes: func(ctx context.Context) ([]gmailinbound.Inbox, error) {
			if err := api.syncAgentEmailInboxes(ctx); err != nil {
				return nil, err
			}
			return store.Inboxes(ctx)
		},
		Stored: func(ctx context.Context, i gmailinbound.Inbox) error { return api.wakeAgentInboxWatchers(ctx, i) }}
	api.gmailInbound = service
	router.HandleFunc(gmailInboundEventPath, service.Receive).Methods("POST")
	router.HandleFunc("/api/agent-email/inbox", api.agentEmailInboxHTTP).Methods("GET")
	router.HandleFunc("/api/agent-email/setup", api.agentEmailSetupHTTP).Methods("GET", "POST")
	api.initGmailSetup(router)
	ctx, cancel := context.WithCancel(context.Background())
	service.Start(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			if err := api.syncAgentEmailInboxes(ctx); err != nil && ctx.Err() == nil {
				log.Printf("[AGENT-INBOX] inventory needs attention: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { cancel(); api.gmailSetup.stop(); wg.Wait(); service.Wait(); _ = store.Close() })
	}
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
		return "", fmt.Errorf("Gmail account name is too long for a receiving address")
	}
	return local + "@" + parts[1], nil
}
