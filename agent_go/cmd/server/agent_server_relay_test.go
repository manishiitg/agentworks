package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

type relayHTTPFixture struct {
	Server string
	JWT    string
	Parent string
	Child  string
	Lease  agentworksclient.AgentAttachment
}

func newRelayHTTPFixture(t *testing.T, name string) (triggerLinkEnv, *httptest.Server, relayHTTPFixture) {
	t.Helper()
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "server-relay-http-fixture-secret")
	mux := http.NewServeMux()
	mux.HandleFunc(mcpOAuthRegisterPath, env.api.handleMCPOAuthRegister)
	mux.HandleFunc(mcpOAuthAuthorizePath, env.api.handleMCPOAuthAuthorize)
	mux.HandleFunc(mcpOAuthConsentPath, env.api.handleMCPOAuthConsent)
	mux.HandleFunc(mcpOAuthTokenPath, env.api.handleMCPOAuthToken)
	mux.HandleFunc("/api/agentbridge/servers/callback", env.api.handleAgentServerLinkCallback)
	mux.HandleFunc("/api/agentbridge/servers", env.api.handleAgentServerLinks)
	mux.HandleFunc("/api/external/v1/call", env.api.handleExternalCall)
	mux.HandleFunc("/api/external/v1/tools", env.api.handleExternalTools)
	mux.HandleFunc("/api/external/v1/agents/attachment", env.api.handleAgentAttachment)
	mux.HandleFunc("/api/external/v1/agents/link", env.api.handleAgentServerLinkGrant)
	mux.HandleFunc("/api/external/v1/agents/events", env.api.handleAgentEvents)
	srv := httptest.NewServer(AuthMiddleware(mux))
	t.Setenv("PUBLIC_URL", srv.URL)
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	device, verification, err := store.CreateCLIDevice(context.Background(), "crews:run", "runs:execute")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.DecideCLIDevice(context.Background(), verification, &UserClaims{UserID: "owner", Username: "owner"}, true); err != nil {
		t.Fatal(err)
	}
	_, parent, _, err := store.PollCLIDevice(context.Background(), device, srv.URL+"/api/external/v1")
	if err != nil {
		t.Fatal(err)
	}
	client, _ := agentworksclient.New(srv.URL, parent)
	if _, err = client.Call(context.Background(), "messages", map[string]any{"action": "register", "agent_name": name}); err != nil {
		t.Fatal(err)
	}
	cfg, err := client.CreateAgentConnection(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	child, _ := agentworksclient.New(srv.URL, cfg.Token)
	lease, err := child.AttachAgent(context.Background(), agentworksclient.AgentAttachmentOptions{AgentName: name, Provider: "codex", SessionID: name + "-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	jwt, err := GenerateJWT("owner", "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	return env, srv, relayHTTPFixture{Server: srv.URL, JWT: jwt, Parent: parent, Child: cfg.Token, Lease: lease}
}

// A separate process gives the other server its own account directory, OAuth
// DB, workspace, public origin and event streams. No production server is used.
func TestAgentServerRelayRemoteProcess(t *testing.T) {
	if os.Getenv("AGENT_RELAY_HTTP_FIXTURE") != "1" {
		t.Skip("subprocess fixture")
	}
	_, srv, fixture := newRelayHTTPFixture(t, "reviewer")
	defer srv.Close()
	raw, _ := json.Marshal(fixture)
	if err := os.WriteFile(os.Getenv("AGENT_RELAY_HTTP_INFO"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestAgentServerRelayTwoServersAndDeliveredBoundary(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "100.100.100.200", "::1", "fd00:ec2::254", "::ffff:127.0.0.1"} {
		if agentServerLinkAddressAllowed(netip.MustParseAddr(raw), false) {
			t.Fatal("private or metadata address allowed", raw)
		}
	}
	if !agentServerLinkAddressAllowed(netip.MustParseAddr("127.0.0.1"), true) {
		t.Fatal("explicit loopback development is unavailable")
	}
	info := filepath.Join(t.TempDir(), "remote.json")
	cmd := exec.Command(os.Args[0], "-test.run=^TestAgentServerRelayRemoteProcess$", "-test.timeout=120s")
	cmd.Env = append(os.Environ(), "AGENT_RELAY_HTTP_FIXTURE=1", "AGENT_RELAY_HTTP_INFO="+info)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		input.Close()
		if err := cmd.Wait(); err != nil {
			t.Errorf("remote fixture failed: %v; output %s", err, output.String())
		}
	}()
	var remote relayHTTPFixture
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		raw, e := os.ReadFile(info)
		if e == nil && json.Unmarshal(raw, &remote) == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if remote.Server == "" {
		t.Fatal("remote fixture did not start")
	}
	env, homeServer, home := newRelayHTTPFixture(t, "worker")
	defer homeServer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() {
		if s := env.api.serverRelay; s != nil {
			s.mu.Lock()
			for _, w := range s.workers {
				w.cancel()
			}
			s.mu.Unlock()
		}
	}()
	homeChild, _ := agentworksclient.New(home.Server, home.Child)
	remoteChild, _ := agentworksclient.New(remote.Server, remote.Child)
	startStream := func(c *agentworksclient.Client, lease agentworksclient.AgentAttachment) context.CancelFunc {
		t.Helper()
		streamCtx, stop := context.WithCancel(ctx)
		ready := make(chan struct{}, 1)
		go c.StreamAgentMessages(streamCtx, lease, func(agentworksclient.AgentLiveMessage) error { return nil }, func() { ready <- struct{}{} })
		select {
		case <-ready:
		case <-time.After(10 * time.Second):
			stop()
			t.Fatal("native fixture did not connect")
		}
		return stop
	}
	stopHome := startStream(homeChild, home.Lease)
	defer func() { stopHome() }()
	stopRemote := startStream(remoteChild, remote.Lease)
	defer stopRemote()
	request := func(method, address, body, bearer string) (int, []byte, http.Header) {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, method, address, strings.NewReader(body))
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		raw, e := io.ReadAll(response.Body)
		if e != nil {
			t.Fatal(e)
		}
		return response.StatusCode, raw, response.Header
	}
	status, raw, _ := request("POST", home.Server+"/api/agentbridge/servers", `{"action":"connect","name":"team","server":"`+remote.Server+`"}`, home.JWT)
	var start struct {
		URL string `json:"approval_url"`
	}
	if status != 200 || json.Unmarshal(raw, &start) != nil || start.URL == "" {
		t.Fatal("start linked-server approval", status, string(raw))
	}
	status, _, headers := request("GET", start.URL, "", "")
	if status != 303 {
		t.Fatal("remote OAuth authorize", status)
	}
	consent := remote.Server + strings.Replace(headers.Get("Location"), "/oauth/consent", mcpOAuthConsentPath, 1)
	status, raw, _ = request("POST", consent, `{"decision":"approve"}`, remote.JWT)
	var approved struct {
		URL string `json:"redirect_url"`
	}
	if status != 200 || json.Unmarshal(raw, &approved) != nil || approved.URL == "" {
		t.Fatal("remote consent", status)
	}
	destination, _ := url.Parse(approved.URL)
	callbackBody, _ := json.Marshal(map[string]string{"state": destination.Query().Get("state"), "code": destination.Query().Get("code")})
	otherJWT, e := GenerateJWT("other", "other", "")
	if e != nil {
		t.Fatal(e)
	}
	status, _, _ = request("POST", home.Server+"/api/agentbridge/servers/callback", string(callbackBody), otherJWT)
	if status != 403 {
		t.Fatal("wrong home account completed link", status)
	}
	before, e := readAgentServerLinks(ctx, "owner")
	if e != nil || len(before) != 0 {
		t.Fatal("wrong account saved another person's grant")
	}
	status, _, _ = request("POST", home.Server+"/api/agentbridge/servers/callback", string(callbackBody), "")
	if status != 401 {
		t.Fatal("unauthenticated callback accepted", status)
	}
	status, _, _ = request("POST", home.Server+"/api/agentbridge/servers/callback", string(callbackBody), home.JWT)
	if status != 200 {
		t.Fatal("home callback", status)
	}
	status, _, _ = request("POST", home.Server+"/api/agentbridge/servers/callback", string(callbackBody), home.JWT)
	if status != 400 {
		t.Fatal("callback replay accepted", status)
	}
	links, err := readAgentServerLinks(ctx, "owner")
	if err != nil || len(links) != 1 {
		t.Fatal("saved link", err)
	}
	public, _ := json.Marshal(links)
	if bytes.Contains(public, []byte("credentials")) || bytes.Contains(public, []byte("access_token")) || bytes.Contains(public, []byte("refresh_token")) {
		t.Fatal("link response leaked credentials")
	}
	if _, _, err := resolveLinkedAgentTarget(ctx, "other", "reviewer@team"); err == nil {
		t.Fatal("another person borrowed the owner's link")
	}
	if _, err = homeChild.Call(ctx, "agent_servers", map[string]any{"action": "list"}); err == nil {
		t.Fatal("agent child reached account-link controls")
	}
	call := func(c *agentworksclient.Client, args map[string]any) json.RawMessage {
		t.Helper()
		raw, e := c.Call(ctx, "messages", args)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	wait := func(check func() bool) {
		t.Helper()
		end := time.Now().Add(15 * time.Second)
		for time.Now().Before(end) {
			if check() {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("relay acceptance did not complete")
	}
	incoming := func(c *agentworksclient.Client, lease agentworksclient.AgentAttachment) []agentworksclient.AgentLiveMessage {
		t.Helper()
		raw, e := c.AgentAttachmentCall(ctx, map[string]any{"action": "inbox", "agent_name": lease.AgentName, "attachment_id": lease.ID, "after": 0, "limit": 20})
		if e != nil {
			t.Fatal(e)
		}
		var page struct {
			Messages []agentworksclient.AgentLiveMessage `json:"messages"`
		}
		if json.Unmarshal(raw, &page) != nil {
			t.Fatal("invalid inbox page")
		}
		return page.Messages
	}
	receipt := func(c *agentworksclient.Client, name, inbox, id string) map[string]any {
		raw := call(c, map[string]any{"action": "read", "agent_name": name, "inbox_id": inbox, "message_id": id})
		var result struct {
			Sent []map[string]any `json:"sent"`
		}
		if json.Unmarshal(raw, &result) != nil || len(result.Sent) != 1 {
			t.Fatal("missing exact sent receipt")
		}
		if bytes.Contains(raw, []byte("relay request")) || bytes.Contains(raw, []byte("relay reply")) {
			t.Fatal("receipt lookup returned a body")
		}
		return result.Sent[0]
	}
	ack := func(c *agentworksclient.Client, lease agentworksclient.AgentAttachment, id string) {
		call(c, map[string]any{"action": "ack", "agent_name": lease.AgentName, "attachment_id": lease.ID, "message_id": id, "delivery": "delivered", "transport": "mailbox"})
	}
	sent := call(homeChild, map[string]any{"action": "send", "agent_name": "worker", "target": "agent:reviewer@team", "message": "relay request", "submission_id": "relay-request-fixture"})
	var outgoing struct {
		Inbox string `json:"inbox_id"`
		ID    string `json:"message_id"`
	}
	if json.Unmarshal(sent, &outgoing) != nil {
		t.Fatal("invalid send receipt")
	}
	var remoteMessage agentworksclient.AgentLiveMessage
	wait(func() bool {
		rows := incoming(remoteChild, remote.Lease)
		if len(rows) == 0 {
			return false
		}
		remoteMessage = rows[0]
		return remoteMessage.Message == "relay request"
	})
	if receipt(homeChild, "worker", outgoing.Inbox, outgoing.ID)["state"] == "delivered" {
		t.Fatal("storage was reported delivered")
	}
	if _, e := remoteChild.Call(ctx, "messages", map[string]any{"action": "send", "agent_name": "reviewer", "inbox_id": remoteMessage.InboxID, "message": "file", "file_name": "note.txt", "file_base64": "aGVsbG8=", "submission_id": "relay-file-refusal"}); e == nil {
		t.Fatal("file queued without cross-server attachment support")
	}
	ack(remoteChild, remote.Lease, remoteMessage.MessageID)
	wait(func() bool { return receipt(homeChild, "worker", outgoing.Inbox, outgoing.ID)["state"] == "delivered" })

	reader, err := env.api.agentServerRelay().reader(links[0].ID, home.Lease.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	reader.mu.Lock()
	oldLease, remoteAgentID := reader.lease.ID, reader.lease.AgentID
	reader.mu.Unlock()
	reader.cancel()
	env.api.agentServerRelay().changed()
	wait(func() bool {
		current, e := env.api.agentServerRelay().reader(links[0].ID, home.Lease.AgentID)
		if e != nil {
			return false
		}
		current.mu.Lock()
		defer current.mu.Unlock()
		return current.lease.ID != "" && current.lease.ID != oldLease && current.lease.AgentID == remoteAgentID
	})
	repeated := call(homeChild, map[string]any{"action": "send", "agent_name": "worker", "target": "agent:reviewer@team", "message": "relay request", "submission_id": "relay-request-fixture"})
	var repeatedReceipt struct {
		Inbox string `json:"inbox_id"`
		ID    string `json:"message_id"`
	}
	json.Unmarshal(repeated, &repeatedReceipt)
	if repeatedReceipt.Inbox != outgoing.Inbox || repeatedReceipt.ID != outgoing.ID {
		t.Fatal("uncertain repeated send changed identity or thread")
	}
	remoteParent, _ := agentworksclient.New(remote.Server, remote.Parent)
	projectedOnline := func(want bool) bool {
		raw := call(remoteParent, map[string]any{"action": "list_agents"})
		var listing struct {
			Agents []struct {
				ID     string `json:"agent_id"`
				Online bool   `json:"online"`
			} `json:"agents"`
		}
		if json.Unmarshal(raw, &listing) != nil {
			t.Fatal("invalid remote presence")
		}
		for _, a := range listing.Agents {
			if a.ID == remoteAgentID {
				return a.Online == want
			}
		}
		return false
	}
	wait(func() bool { return projectedOnline(true) })
	reply := call(remoteChild, map[string]any{"action": "send", "agent_name": "reviewer", "inbox_id": remoteMessage.InboxID, "message": "relay reply", "submission_id": "relay-reply-fixture"})
	var replyReceipt struct {
		ID string `json:"message_id"`
	}
	json.Unmarshal(reply, &replyReceipt)
	var homeMessage agentworksclient.AgentLiveMessage
	wait(func() bool {
		rows := incoming(homeChild, home.Lease)
		if len(rows) == 0 {
			return false
		}
		homeMessage = rows[0]
		return homeMessage.Message == "relay reply"
	})
	if homeMessage.InboxID != outgoing.Inbox || !strings.HasSuffix(homeMessage.Sender, "@team") {
		t.Fatal("reply did not retain its home thread and remote identity")
	}
	if receipt(remoteChild, "reviewer", remoteMessage.InboxID, replyReceipt.ID)["state"] == "delivered" {
		t.Fatal("import produced a delivered receipt")
	}
	worker, err := env.api.agentServerRelay().reader(links[0].ID, home.Lease.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	position := 0
	if err = worker.importPending(&position); err != nil {
		t.Fatal(err)
	}
	if rows := incoming(homeChild, home.Lease); len(rows) != 1 {
		t.Fatal("repeated remote import duplicated a message", len(rows))
	}
	ack(homeChild, home.Lease, homeMessage.MessageID)
	wait(func() bool {
		return receipt(remoteChild, "reviewer", remoteMessage.InboxID, replyReceipt.ID)["state"] == "delivered"
	})
	// A disconnected native stream remains offline; the server reader alone
	// cannot manufacture online status or a native delivery receipt.
	stopHome()
	wait(func() bool {
		a, e := namedAgentByID(ctx, "owner", home.Lease.AgentID)
		return e == nil && !namedAgentOnline(a)
	})
	wait(func() bool { return projectedOnline(false) })
	call(remoteChild, map[string]any{"action": "send", "agent_name": "reviewer", "inbox_id": remoteMessage.InboxID, "message": "waiting after disconnect", "submission_id": "relay-unlink-fixture"})
	wait(func() bool { return len(incoming(homeChild, home.Lease)) == 1 })
	status, _, _ = request("POST", home.Server+"/api/agentbridge/servers", `{"action":"disconnect","name":"team"}`, home.JWT)
	if status != 200 {
		t.Fatal("unlink", status)
	}
	if rows := incoming(homeChild, home.Lease); len(rows) != 0 {
		t.Fatal("unlink released an imported body")
	}
	if _, err = resolveNamedMessageAgent(ctx, "owner", "worker", ""); err != nil {
		t.Fatal("unlink removed home identity", err)
	}
}
