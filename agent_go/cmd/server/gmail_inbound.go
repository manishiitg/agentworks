package server

import (
	"context"
	"fmt"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
	"log"
	"net/mail"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func userInboxClient(owner string) (services.GmailInboundClient, string, error) {
	svc := services.GetGmailService()
	if svc == nil {
		return services.GmailInboundClient{}, "", fmt.Errorf("Connect Gmail in My Vault and choose Use for Crew inboxes")
	}
	conn, err := svc.AgentInboxConnection(owner)
	if err != nil {
		return services.GmailInboundClient{}, "", err
	}
	return services.GmailInboundClient{ConnectionID: conn.ID, OwnerID: owner}, conn.Email, nil
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
	store, err := gmailinbound.Open(filepath.Join(root, "gmail-inbound", "user-agent-email.db"))
	if err != nil {
		log.Printf("[AGENT-INBOX] store unavailable: %v", err)
		return func() {}
	}
	service := &gmailinbound.Service{Store: store, Client: func(ctx context.Context, m gmailinbound.Mailbox) (gmailinbound.Client, error) {
		client, email, err := userInboxClient(m.OwnerID)
		if err == nil && (client.ConnectionID != m.ConnectionID || email != m.Email) {
			err = fmt.Errorf("Inbox account changed; delivery stopped")
		}
		return client, err
	},
		Inboxes: func(ctx context.Context) ([]gmailinbound.Inbox, error) {
			return store.Inboxes(ctx)
		},
		Stored: func(ctx context.Context, i gmailinbound.Inbox) error { return api.wakeAgentInboxWatchers(ctx, i) }}
	api.gmailInbound = service
	router.HandleFunc("/api/agent-email/inbox", api.agentEmailInboxHTTP).Methods("GET")
	ctx, cancel := context.WithCancel(context.Background())
	// Reconcile Crew receiving settings before workers can use persisted inbox rows.
	if err := api.syncAgentEmailInboxes(ctx); err != nil {
		log.Printf("[AGENT-INBOX] inventory needs attention: %v", err)
	}
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
		once.Do(func() { cancel(); wg.Wait(); service.Wait(); _ = store.Close() })
	}
}
func inboundAddress(account, id string) (string, error) {
	a, e := mail.ParseAddress(account)
	if e != nil {
		return "", fmt.Errorf("Gmail account has no verified email")
	}
	parts := strings.SplitN(strings.ToLower(a.Address), "@", 2)
	base := strings.SplitN(parts[0], "+", 2)[0]
	// All Crew addresses use the same short, stable suffix, including long Gmail usernames.
	suffix := strings.ReplaceAll(id, "-", "")[:16]
	local := base + "+agent-" + suffix
	if len(local) > 64 {
		return "", fmt.Errorf("Choose a shorter Gmail address in My Vault for Crew inboxes")
	}
	return local + "@" + parts[1], nil
}
