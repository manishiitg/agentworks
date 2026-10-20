package services

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
)

// GmailInboundClient is a narrow server-owned adapter. Watch registration and
// attachment reads use only the company receiving connection. Every invocation
// re-resolves it and its current read grant; personal Gmail is never watched.
type GmailInboundClient struct {
	ConnectionID string
	Topic        string
}

func (c GmailInboundClient) run(ctx context.Context, readOnly bool, args ...string) ([]byte, error) {
	svc := GetGmailService()
	if svc == nil {
		return nil, fmt.Errorf("Gmail is unavailable")
	}
	saved, ok := svc.GetConnection(c.ConnectionID)
	if !ok || saved.IsPrivate() || !saved.AgentInboxReceiver {
		return nil, fmt.Errorf("Agent inbox receiving requires a company Google connection")
	}
	scope := GmailUseScope{InboxReceiver: true}
	conn, e := svc.ConnectionForScope(c.ConnectionID, scope)
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

	access, e := svc.GoogleCLIAccessForConnectionIn(ctx, c.ConnectionID, scope)
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

func decodeInboundAttachment(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}
