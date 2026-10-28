package services

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
)

// GmailInboundClient is only used by server receiving, private Watcher sources and stored-message attachment reads.
// It never grants a Crew general access to its owner's private mailbox.
type GmailInboundClient struct {
	ConnectionID string
	OwnerID      string
}

func (c GmailInboundClient) run(ctx context.Context, readOnly bool, args ...string) ([]byte, error) {
	svc := GetGmailService()
	if svc == nil {
		return nil, fmt.Errorf("Gmail is unavailable")
	}
	saved, err := svc.AgentInboxConnection(c.OwnerID)
	if err != nil || saved.ID != c.ConnectionID {
		return nil, fmt.Errorf("Reconnect your selected inbox account in your project's Vault panel or in chat")
	}
	scope := GmailUseScope{inboxOwner: c.OwnerID, UserID: c.OwnerID, ReadOnly: true}
	conn, e := svc.ConnectionForScope(c.ConnectionID, scope)
	if e != nil {
		return nil, e
	}
	if !conn.AllowReadAccess {
		return nil, fmt.Errorf("enable Gmail read access and reconnect")
	}
	status, _ := svc.AuthStatusForConnectionBlocking(ctx, c.ConnectionID)
	if !status.Authenticated || !GoogleScopesGrant(status.Scopes, GmailReadonlyScope) {
		return nil, fmt.Errorf("Gmail read permission has not been granted")
	}

	if status.Email != "" && !strings.EqualFold(status.Email, conn.Email) {
		return nil, fmt.Errorf("Inbox account identity changed; reconnect in your project's Vault panel or in chat")
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
		// Classify a deleted message without returning CLI output or
		// arguments (which can contain token or message text) to the caller.
		if strings.Contains(stderr.String(), "404") {
			return nil, gmailinbound.ErrMessageGone
		}
		return nil, fmt.Errorf("Gmail connection needs attention; reconnect your inbox account in your project's Vault panel or in chat")
	}
	if out.overflow {
		return nil, fmt.Errorf("Gmail response exceeds 30 MiB")
	}
	return out.Bytes(), nil
}

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

func (c GmailInboundClient) Message(ctx context.Context, id string) (gmailinbound.RawMessage, error) {
	b, e := c.run(ctx, true, "gmail", "raw", id)
	var m gmailinbound.RawMessage
	if errors.Is(e, gmailinbound.ErrMessageGone) {
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

// List fetches only messages Gmail matched to the owner's Crew aliases.
// The overlap window handles arrivals during listing; the inbox store deduplicates IDs.
func (c GmailInboundClient) List(ctx context.Context, since int64, addresses []string, personal bool) ([]string, error) {
	if len(addresses) == 0 && !personal {
		return nil, nil
	}
	var ids []string
	// A private source covers received mail across the mailbox, including archived
	// arrivals. Otherwise query only Crew aliases. Never download the old mailbox.
	queries := []string{}
	if personal {
		queries = append(queries, fmt.Sprintf("after:%d -in:sent -in:drafts", since-300))
	}
	// Bound query size without changing the one-mailbox polling model.
	for start := 0; start < len(addresses); start += 40 {
		end := start + 40
		if end > len(addresses) {
			end = len(addresses)
		}
		terms := []string{}
		for _, address := range addresses[start:end] {
			terms = append(terms, "deliveredto:"+address)
		}
		queries = append(queries, fmt.Sprintf("after:%d {%s}", since-300, strings.Join(terms, " ")))
	}
	for _, query := range queries {
		page := ""
		for n := 0; n < 100; n++ {
			params := map[string]any{"userId": "me", "maxResults": 100, "q": query}
			if page != "" {
				params["pageToken"] = page
			}
			b, _ := json.Marshal(params)
			data, err := c.run(ctx, true, "api", "call", "gmail", "v1", "gmail.users.messages.list", "--params", string(b))
			if err != nil {
				return nil, err
			}
			var r struct {
				Messages []struct {
					ID string `json:"id"`
				} `json:"messages"`
				Next string `json:"nextPageToken"`
			}
			if err = json.Unmarshal(data, &r); err != nil {
				return nil, err
			}
			for _, m := range r.Messages {
				ids = append(ids, m.ID)
			}
			page = r.Next
			if page == "" {
				break
			}
			if n == 99 {
				return nil, fmt.Errorf("Crew email backlog exceeds 100 pages; check position retained")
			}
		}
	}
	return ids, nil
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
