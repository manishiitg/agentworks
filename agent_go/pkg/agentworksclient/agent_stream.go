package agentworksclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AgentAttachment identifies one explicitly attached native conversation. An
// address can move to a new session, but an old listener cannot follow it.
type AgentAttachment struct {
	ID        string `json:"attachment_id"`
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Purpose   string `json:"purpose"`
	Provider  string `json:"provider"`
	SessionID string `json:"native_session_id"`
}

type AgentLiveMessage struct {
	AttachmentID string `json:"attachment_id"`
	MessageID    string `json:"message_id"`
	InboxID      string `json:"inbox_id"`
	Sender       string `json:"sender"`
	// FromPerson is the owner's server username for a cross-person shared
	// message (861-103); Untrusted marks it untrusted. Both are absent for
	// same-account senders.
	FromPerson string `json:"from_person"`
	Untrusted  bool   `json:"untrusted"`
	SenderID   string `json:"sender_id"`
	Message    string `json:"message"`
	Sequence   int    `json:"sequence"`
	Delivery   string `json:"delivery"`
}

type AgentAttachmentOptions struct {
	RejectLiveMove bool   `json:"reject_live_move,omitempty"`
	AgentName      string `json:"agent_name"`
	Purpose        string `json:"purpose"`
	Provider       string `json:"provider"`
	SessionID      string `json:"native_session_id"`
}

// AttachAgent is deliberately outside the MCP tool catalog. Its arguments
// come from the person's explicit native bridge configuration.
func (c *Client) AttachAgent(ctx context.Context, opts AgentAttachmentOptions) (AgentAttachment, error) {
	raw, err := c.request(ctx, http.MethodPost, "/api/external/v1/agents/attachment", struct {
		Action string `json:"action"`
		AgentAttachmentOptions
	}{"attach", opts}, true)
	if err != nil {
		return AgentAttachment{}, err
	}
	var a AgentAttachment
	if err = json.Unmarshal(raw, &a); err != nil || a.ID == "" {
		return a, errors.New("invalid attachment response")
	}
	return a, nil
}

// StreamAgentMessages keeps one authenticated connection open. The server
// pushes on durable writes; this never polls the messages tool. Reconnect and
// adapter de-duplication belong to the caller, which owns its native session.
func (c *Client) StreamAgentMessages(ctx context.Context, attachment AgentAttachment, receive func(AgentLiveMessage) error) error {
	token, err := c.authToken(ctx)
	if err != nil {
		return err
	}
	q := url.Values{"agent_name": {attachment.AgentName}, "attachment_id": {attachment.ID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/external/v1/agents/events?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	transport := *c.http
	transport.Timeout = 0
	resp, err := transport.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &APIError{Status: resp.StatusCode, Code: "agent_stream_refused", Message: "Live connection refused; check attachment and connection consent."}
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return errors.New("server did not return an agent event stream")
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var frame struct {
			Type string `json:"type"`
			AgentLiveMessage
			Error string `json:"error"`
		}
		if err = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame); err != nil {
			return fmt.Errorf("invalid agent event: %w", err)
		}
		if frame.Type == "closed" {
			return &APIError{Status: 403, Code: "agent_attachment_closed", Message: "The attachment or its connection is no longer authorized."}
		}
		if frame.Type != "message" {
			continue
		}
		if frame.AttachmentID != attachment.ID || frame.MessageID == "" || frame.InboxID == "" {
			return errors.New("agent event does not match this attachment")
		}
		if err = receive(frame.AgentLiveMessage); err != nil {
			return err
		}
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	return errors.New("agent stream disconnected")
}

// CreateAgentConnection derives a messaging-only identity from the person's
// explicit CLI login. The returned secret stays in the private local daemon.
func (c *Client) CreateAgentConnection(ctx context.Context, name string) (Config, error) {
	raw, err := c.request(ctx, http.MethodPost, "/api/external/v1/agents/attachment", map[string]any{"action": "connection", "agent_name": name}, true)
	if err != nil {
		return Config{}, err
	}
	var v struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int    `json:"expires_in"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Access == "" || v.Refresh == "" {
		return Config{}, errors.New("invalid agent connection response")
	}
	return Config{Server: c.baseURL, Token: v.Access, RefreshToken: v.Refresh, ExpiresAt: time.Now().Add(time.Duration(v.Expires) * time.Second)}, nil
}

// ShareAgent runs a group action for one of the person's own agents with the
// person's own CLI login: share (join a group, creating it when a purpose is
// given), unshare (leave one group, or all), groups, group (members) or list.
// An agent connection is refused.
func (c *Client) ShareAgent(ctx context.Context, action, agentName, group, purpose string) (json.RawMessage, error) {
	return c.request(ctx, http.MethodPost, "/api/external/v1/agents/sharing", map[string]any{"action": action, "agent_name": agentName, "group": group, "purpose": purpose}, true)
}
