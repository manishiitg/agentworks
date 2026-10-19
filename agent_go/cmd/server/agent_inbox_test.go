package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/mark3labs/mcp-go/mcp"
)

// Actual authenticated Streamable HTTP MCP and CLI REST share durable inboxes.
// OAuth clients here are regression fixtures, not real hosted-app acceptance.
func TestHostedAgentInboxOverMCP(t *testing.T) {
	e := newTriggerLinkEnv(t)
	e.api.agentProfiles = e.svc.registry
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "hosted-inbox-test-signing-secret")
	mux := http.NewServeMux()
	mux.HandleFunc(externalMCPPath, e.api.handleExternalMCP)
	mux.HandleFunc("/api/external/v1/call", e.api.handleExternalCall)
	srv := httptest.NewServer(AuthMiddleware(mux))
	defer srv.Close()
	t.Setenv("PUBLIC_URL", srv.URL)
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	issue := func(user string, cli bool, workflows []string) string {
		t.Helper()
		id, resource := cliOAuthClientID, srv.URL+"/api/external/v1"
		if !cli {
			c, err := store.RegisterClient(ctx, "Example hosted app", []string{"https://client.example/callback"})
			if err != nil {
				t.Fatal(err)
			}
			id, resource = c.ID, srv.URL+externalMCPPath
		}
		g := mcpOAuthGrant{FamilyID: uuid.NewString(), ClientID: id, Resource: resource, UserID: user, Username: user, Scopes: []string{"workflows:read", "crews:run", "runs:execute"}, WorkflowIDs: workflows}
		tx, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		raw, _, err := issueMCPOAuthPair(ctx, tx, &g)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	hosted, other, cliRaw := issue("owner", false, nil), issue("other", false, nil), issue("owner", true, nil)
	cli, err := agentworksclient.New(srv.URL, cliRaw)
	if err != nil {
		t.Fatal(err)
	}
	cliCall := func(args map[string]any) map[string]any {
		t.Helper()
		raw, err := cli.Call(ctx, "messages", args)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err = json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	sessions := map[string]string{}
	rpc := func(token, method string, params any) *mcp.CallToolResult {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		r, err := http.NewRequest(http.MethodPost, srv.URL+externalMCPPath, strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		if id := sessions[token]; id != "" {
			r.Header.Set("Mcp-Session-Id", id)
		}
		res, err := (&http.Client{Timeout: 15 * time.Second}).Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 200 {
			t.Fatalf("MCP HTTP%d: %s", res.StatusCode, raw)
		}
		if id := res.Header.Get("Mcp-Session-Id"); id != "" {
			sessions[token] = id
		}
		if !strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
			for _, line := range strings.Split(string(raw), "\n") {
				if strings.HasPrefix(line, "data: ") {
					raw = []byte(strings.TrimPrefix(line, "data: "))
				}
			}
		}
		var envelope struct {
			Result json.RawMessage `json:"result"`
			Error  any             `json:"error"`
		}
		if err = json.Unmarshal(raw, &envelope); err != nil || envelope.Error != nil {
			t.Fatalf("MCP response: %s (%v)", raw, err)
		}
		var result mcp.CallToolResult
		if method != "initialize" {
			if err = json.Unmarshal(envelope.Result, &result); err != nil {
				t.Fatal(err)
			}
		}
		return &result
	}
	init := func(token string) {
		rpc(token, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "Example app", "version": "1"}})
	}
	call := func(token string, args map[string]any, ok bool) map[string]any {
		t.Helper()
		result := rpc(token, "tools/call", map[string]any{"name": externalMCPToolCall, "arguments": map[string]any{"name": "messages", "arguments": args}})
		if !ok {
			if !result.IsError {
				t.Fatalf("unauthorized inbox call succeeded: %v", args)
			}
			return nil
		}
		requireRemoteSuccess(t, result, "inbox operation")
		var out map[string]any
		if err := json.Unmarshal([]byte(marshalStructured(t, result)), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	init(hosted)
	init(other)
	registered := call(hosted, map[string]any{"action": "register", "agent_name": "browser-helper"}, true)
	if registered["presence"] != "Inbox" || registered["delivery_mode"] != "inbox" || !strings.Contains(registered["message"].(string), "Nothing is pushed") {
		t.Fatalf("inbox explanation: %v", registered)
	}
	cliCall(map[string]any{"action": "register", "agent_name": "terminal-helper"})
	// CLI credentials are refused by authentication before MCP initializes.
	cliMCP, err := http.NewRequest(http.MethodPost, srv.URL+externalMCPPath, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"Example app","version":"1"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	cliMCP.Header.Set("Authorization", "Bearer "+cliRaw)
	cliMCP.Header.Set("Content-Type", "application/json")
	rejected, err := http.DefaultClient.Do(cliMCP)
	if err != nil {
		t.Fatal(err)
	}
	rejectedBody, err := io.ReadAll(rejected.Body)
	_ = rejected.Body.Close()
	if err != nil || rejected.StatusCode != http.StatusForbidden || !strings.Contains(string(rejectedBody), "invalid_target") {
		t.Fatalf("CLI credentials reached MCP: status%d %s (%v)", rejected.StatusCode, rejectedBody, err)
	}

	call(other, map[string]any{"action": "read", "agent_name": "browser-helper"}, false)
	send := map[string]any{"action": "send", "agent_name": "browser-helper", "target": "agent:terminal-helper", "message": "Inbox check", "submission_id": "hosted-send"}
	receipt := call(hosted, send, true)
	if receipt["delivery_mode"] != "inbox" || !strings.Contains(receipt["state_text"].(string), "Saved in terminal-helper's inbox") {
		t.Fatalf("plain inbox receipt: %v", receipt)
	}
	retry := call(hosted, send, true)
	if retry["message_id"] != receipt["message_id"] || retry["state_text"] != receipt["state_text"] || retry["delivery_mode"] != receipt["delivery_mode"] {
		t.Fatalf("retry receipt lost route or duplicated message: first=%v retry=%v", receipt, retry)
	}
	page := cliCall(map[string]any{"action": "read", "agent_name": "terminal-helper"})
	if len(page["messages"].([]any)) != 1 {
		t.Fatalf("CLI inbox: %v", page)
	}
	reply := cliCall(map[string]any{"action": "send", "agent_name": "terminal-helper", "inbox_id": receipt["inbox_id"], "message": "Received your check", "submission_id": "cli-reply"})
	page = call(hosted, map[string]any{"action": "read", "agent_name": "browser-helper"}, true)
	items := page["messages"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["delivery_mode"] != "inbox" {
		t.Fatalf("hosted inbox: %v", page)
	}
	call(other, map[string]any{"action": "ack", "agent_name": "browser-helper", "message_id": reply["message_id"], "delivery": "handled"}, false)
	call(hosted, map[string]any{"action": "ack", "agent_name": "browser-helper", "message_id": receipt["message_id"], "delivery": "handled"}, false)
	call(hosted, map[string]any{"action": "ack", "agent_name": "browser-helper", "message_id": reply["message_id"], "delivery": "read"}, true)
	call(hosted, map[string]any{"action": "ack", "agent_name": "browser-helper", "message_id": reply["message_id"], "delivery": "handled"}, true)
	call(hosted, map[string]any{"action": "attach", "agent_name": "browser-helper", "native_session_id": "another-chat"}, false)
	saved, err := readAgentMessageStore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range saved.Agents {
		if a.Name == "browser-helper" && a.Attachment != nil {
			t.Fatal("mailbox acknowledgement attached a chat")
		}
	}
	for _, c := range saved.Conversations {
		for _, m := range c.Messages {
			if m.ID == reply["message_id"] && (m.Delivery != "processed" || m.LiveTransport != "mailbox" || m.LiveSession != "" || m.LiveAttachment != "") {
				t.Fatalf("inbox falsely claimed live delivery: %+v", m)
			}
		}
	}
	t.Run("file submission retries keep one message and one file", func(t *testing.T) {
		fileArgs := map[string]any{"action": "send", "agent_name": "browser-helper", "target": "agent:terminal-helper", "message": "Review this file", "submission_id": "hosted-file", "file_name": "report.txt", "file_base64": base64.StdEncoding.EncodeToString([]byte("example report"))}
		first := call(hosted, fileArgs, true)
		if again := call(hosted, fileArgs, true); again["message_id"] != first["message_id"] || again["inbox_id"] != first["inbox_id"] {
			t.Fatalf("file retry duplicated the message: first=%v retry=%v", first, again)
		}
		// The explicit conversation path uses the same submission check.
		fileArgs["inbox_id"] = first["inbox_id"]
		if again := call(hosted, fileArgs, true); again["message_id"] != first["message_id"] {
			t.Fatalf("file retry inside the inbox duplicated the message: %v", again)
		}
		for key, changed := range map[string]any{"file_base64": base64.StdEncoding.EncodeToString([]byte("changed report")), "file_name": "different.txt", "message": "Different request"} {
			original := fileArgs[key]
			fileArgs[key] = changed
			call(hosted, fileArgs, false)
			fileArgs[key] = original
		}
		withoutFile := map[string]any{"action": "send", "agent_name": "browser-helper", "inbox_id": first["inbox_id"], "message": "Review this file", "submission_id": "hosted-file"}
		call(hosted, withoutFile, false)
		// Concurrent CLI retries cannot create orphan files either.
		replyFile := map[string]any{"action": "send", "agent_name": "terminal-helper", "inbox_id": first["inbox_id"], "submission_id": "cli-file-reply", "file_name": "reply.txt", "file_base64": base64.StdEncoding.EncodeToString([]byte("example reply"))}
		results := make(chan error, 6)
		ids := make(chan string, 6)
		for range 6 {
			go func() {
				raw, err := cli.Call(ctx, "messages", replyFile)
				var out map[string]any
				if err == nil {
					err = json.Unmarshal(raw, &out)
				}
				if err == nil {
					ids <- fmt.Sprint(out["message_id"])
				}
				results <- err
			}()
		}
		for range 6 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		firstReply := <-ids
		for range 5 {
			if id := <-ids; id != firstReply {
				t.Fatalf("concurrent retry created another reply: %s != %s", id, firstReply)
			}
		}
		st, err := readAgentMessageStore(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range st.Conversations {
			if c.ID != first["inbox_id"] {
				continue
			}
			if len(c.Messages) != 2 {
				t.Fatalf("file retries stored %d messages, want 2", len(c.Messages))
			}
			for _, m := range c.Messages {
				fileID := regexp.MustCompile(`file-[0-9a-f]{32}`).FindString(m.Text)
				if fileID == "" {
					t.Fatal("file message lost its attachment")
				}
				got := call(hosted, map[string]any{"action": "get_file", "agent_name": "browser-helper", "inbox_id": c.ID, "file_id": fileID}, true)
				if got["file_name"] == "" || got["content_base64"] == "" {
					t.Fatalf("retried file is unavailable: %v", got)
				}
			}
		}
		e.mock.mu.Lock()
		defer e.mock.mu.Unlock()
		files := 0
		for path := range e.mock.files {
			if strings.HasPrefix(path, agentMessageFilesDir) {
				files++
			}
		}
		if files != 2 {
			t.Fatalf("file retries or refused changes left %d files, want 2", files)
		}
	})
	// Product-scoped OAuth and PAT display names cannot claim hosted provenance.
	scoped := issue("owner", false, []string{"wf_reports"})
	init(scoped)
	call(scoped, map[string]any{"action": "register", "agent_name": "scoped"}, false)
	patStore, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer patStore.Close()
	_, pat, err := patStore.Issue(ctx, accesstokens.Token{Name: "MCP OAuth", UserID: "owner", Username: "owner", Scopes: []string{"workflows:read", "runs:execute"}, AllWorkflows: true, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	init(pat)
	call(pat, map[string]any{"action": "register", "agent_name": "spoofed"}, false)
}
