package services

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
)

// GmailInboundClient is a narrow server-owned adapter. Watch registration and
// fixed replies do not opt agents into general Gmail writes. Every invocation
// re-resolves the scoped connection and its current read grant.
type GmailInboundClient struct {
	ConnectionID string
	Topic        string
	Scope        GmailUseScope
}

func (c GmailInboundClient) run(ctx context.Context, readOnly bool, args ...string) ([]byte, error) {
	svc := GetGmailService()
	if svc == nil {
		return nil, fmt.Errorf("Gmail is unavailable")
	}
	conn, e := svc.ConnectionForScope(c.ConnectionID, c.Scope)
	if e != nil {
		return nil, e
	}
	if !conn.AllowReadAccess {
		return nil, fmt.Errorf("enable Gmail read access and reconnect")
	}
	status, _ := svc.AuthStatusForConnectionBlocking(ctx, c.ConnectionID)
	if !GoogleScopesGrant(status.Scopes, GmailReadonlyScope) {
		return nil, fmt.Errorf("Gmail read permission has not been granted")
	}
	if len(args) > 1 && args[0] == "gmail" && args[1] == "send" && !(scopesGrantGmailSend(status.Scopes) || GoogleScopesGrant(status.Scopes, GmailComposeScope)) {
		return nil, fmt.Errorf("Gmail send permission has not been granted")
	}
	access, e := svc.GoogleCLIAccessForConnectionIn(ctx, c.ConnectionID, c.Scope)
	if e != nil {
		return nil, e
	}
	if _, ok := access.Grants["gmail"]; !ok {
		return nil, fmt.Errorf("Gmail read permission has not been granted")
	}
	final := gogBaseArgs(access.Home, nil)
	if access.Token != "" {
		final = append(final, "--access-token", access.Token)
	} else {
		final = append(final, "--account", access.Account, "--client", access.Client)
	}
	final = append(final, args...)
	final = append(final, "--json", "--no-input")
	if readOnly {
		final = append(final, "--readonly", "--gmail-no-send")
	} else if len(args) > 1 && args[1] == "watch" {
		final = append(final, "--gmail-no-send")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, access.GogPath, final...)
	var out limitedInboundOutput
	out.max = 30 * 1024 * 1024
	var stderr limitedInboundOutput
	stderr.max = 4096
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if e = cmd.Run(); e != nil {
		// Classify an expired history cursor without returning CLI output or
		// arguments (which can contain token or message text) to the caller.
		if strings.Contains(stderr.String(), "404") {
			return nil, errInboundHistoryExpired
		}
		return nil, fmt.Errorf("Gmail command failed; check connection authorization and Pub/Sub configuration")
	}
	if out.overflow {
		return nil, fmt.Errorf("Gmail response exceeds 30 MiB")
	}
	return out.Bytes(), nil
}

var errInboundHistoryExpired = gmailinbound.ErrHistoryExpired

type limitedInboundOutput struct {
	bytes.Buffer
	max      int
	overflow bool
}

func (b *limitedInboundOutput) Write(p []byte) (int, error) {
	n := len(p)
	left := b.max - b.Len()
	if len(p) > left {
		p = p[:left]
		b.overflow = true
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func (c GmailInboundClient) Watch(ctx context.Context) (string, int64, error) {
	b, e := c.run(ctx, false, "gmail", "watch", "start", "--topic", c.Topic)
	if e != nil {
		return "", 0, e
	}
	var result struct {
		HistoryID    string      `json:"historyId"`
		Expiration   json.Number `json:"expiration"`
		ExpirationMS json.Number `json:"expirationMs"`
	}
	var envelope struct {
		Watch json.RawMessage `json:"watch"`
	}
	_ = json.Unmarshal(b, &envelope)
	if len(envelope.Watch) > 0 {
		b = envelope.Watch
	}
	if e = json.Unmarshal(b, &result); e != nil {
		return "", 0, e
	}
	exp := result.Expiration
	if exp == "" {
		exp = result.ExpirationMS
	}
	n, _ := strconv.ParseInt(string(exp), 10, 64)
	return result.HistoryID, n / 1000, nil
}
func (c GmailInboundClient) History(ctx context.Context, since string) ([]string, string, error) {
	var ids []string
	page := ""
	var cursor string
	for i := 0; i < 100; i++ {
		args := []string{"gmail", "history", "--since", since, "--max", "100"}
		if page != "" {
			args = append(args, "--page", page)
		}
		b, e := c.run(ctx, true, args...)
		if e != nil {
			return nil, "", e
		}
		var r struct {
			HistoryID string   `json:"historyId"`
			Messages  []string `json:"messages"`
			Next      string   `json:"nextPageToken"`
		}
		if e = json.Unmarshal(b, &r); e != nil {
			return nil, "", e
		}
		ids = append(ids, r.Messages...)
		cursor = r.HistoryID
		page = r.Next
		if page == "" {
			return ids, cursor, nil
		}
	}
	return nil, "", fmt.Errorf("Gmail history backlog exceeds 100 pages; cursor retained")
}
func (c GmailInboundClient) Message(ctx context.Context, id string) (gmailinbound.RawMessage, error) {
	b, e := c.run(ctx, true, "gmail", "raw", id)
	var m gmailinbound.RawMessage
	if errors.Is(e, errInboundHistoryExpired) {
		return m, gmailinbound.ErrMessageGone
	}
	if e != nil {
		return m, e
	}
	e = json.Unmarshal(b, &m)
	if e == nil && (m.ID == "" || m.ThreadID == "") {
		e = fmt.Errorf("Gmail message has no identity")
	}
	return m, e
}

// Resync captures a new cursor before listing the overlap window. Deliveries
// already seen are deduplicated on disk; changes during the scan remain after
// this cursor and will be fetched by the next incremental sync.
func (c GmailInboundClient) Resync(ctx context.Context, since int64) ([]string, string, error) {
	cursor, _, e := c.Watch(ctx)
	if e != nil {
		return nil, "", e
	}
	var ids []string
	page := ""
	for i := 0; i < 100; i++ {
		params := map[string]interface{}{"userId": "me", "maxResults": 100, "q": fmt.Sprintf("after:%d", since-300)}
		if page != "" {
			params["pageToken"] = page
		}
		b, _ := json.Marshal(params)
		data, e := c.run(ctx, true, "api", "call", "gmail", "v1", "gmail.users.messages.list", "--params", string(b))
		if e != nil {
			return nil, "", e
		}
		var r struct {
			Messages []struct {
				ID string `json:"id"`
			} `json:"messages"`
			Next string `json:"nextPageToken"`
		}
		if e = json.Unmarshal(data, &r); e != nil {
			return nil, "", e
		}
		for _, m := range r.Messages {
			ids = append(ids, m.ID)
		}
		page = r.Next
		if page == "" {
			return ids, cursor, nil
		}
	}
	return nil, "", fmt.Errorf("Gmail recovery backlog exceeds 100 pages; cursor retained")
}
func (c GmailInboundClient) Attachment(ctx context.Context, messageID string, a gmailinbound.Attachment) ([]byte, error) {
	if a.Size > 20*1024*1024 {
		return nil, fmt.Errorf("attachment exceeds 20 MiB")
	}
	if a.Data != "" {
		return decodeInboundAttachment(a.Data)
	}
	params, _ := json.Marshal(map[string]string{"userId": "me", "messageId": messageID, "id": a.ID})
	b, e := c.run(ctx, true, "api", "call", "gmail", "v1", "gmail.users.messages.attachments.get", "--params", string(params))
	if e != nil {
		return nil, e
	}
	var r struct {
		Data string `json:"data"`
	}
	if e = json.Unmarshal(b, &r); e != nil {
		return nil, e
	}
	return decodeInboundAttachment(r.Data)
}

// Reply sends only the authorized originator, using a raw message with an
// Auto-Submitted marker. It never copies CC or trusts an incoming Reply-To.
func (c GmailInboundClient) Reply(ctx context.Context, d gmailinbound.Delivery, from string) error {
	svc := GetGmailService()
	if svc == nil {
		return fmt.Errorf("Gmail unavailable")
	}
	allowed, _ := svc.filterRecipients([]string{d.Message.From})
	if len(allowed) == 0 {
		return fmt.Errorf("email recipient is blocked")
	}
	for _, v := range []string{from, d.Message.From, d.Route.Address, d.Message.RFCMessageID} {
		if strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("invalid email header")
		}
	}
	if strings.TrimSpace(d.Message.RFCMessageID) == "" {
		return fmt.Errorf("incoming email has no Message-ID")
	}
	raw := inboundReplyMIME(d, from)
	f, e := os.CreateTemp("", "agentworks-email-reply-*")
	if e != nil {
		return e
	}
	path := f.Name()
	defer os.Remove(path)
	_, e = io.WriteString(f, raw)
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	_, e = c.run(ctx, false, "gmail", "send", "--raw-file", path, "--thread-id", d.Message.ThreadID)
	return e
}

func decodeInboundAttachment(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}
func inboundReplyMIME(d gmailinbound.Delivery, from string) string {
	subject := strings.ReplaceAll(strings.ReplaceAll(d.Message.Subject, "\r", " "), "\n", " ")
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(d.Response))
	var body strings.Builder
	for len(encoded) > 76 {
		body.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	body.WriteString(encoded + "\r\n")
	return "From: " + from + "\r\nTo: " + d.Message.From + "\r\nReply-To: " + d.Route.Address + "\r\nSubject: " + mime.QEncoding.Encode("UTF-8", subject) + "\r\nIn-Reply-To: " + d.Message.RFCMessageID + "\r\nReferences: " + d.Message.RFCMessageID + "\r\nAuto-Submitted: auto-replied\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n" + body.String()
}
