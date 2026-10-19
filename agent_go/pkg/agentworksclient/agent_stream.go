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
	FromPerson   string `json:"from_person"`
	Untrusted    bool   `json:"untrusted"`
	SenderID     string `json:"sender_id"`
	Message      string `json:"message"`
	Sequence     int    `json:"sequence"`
	Delivery     string `json:"delivery"`
	ArrivalText  string `json:"arrival_text,omitempty"`
	DeliveryMode string `json:"delivery_mode,omitempty"`
	// Kind is "status" for a line the server wrote itself (a sender is told a message is still waiting): it is shown
	// as a plain status, needs no reply, and is acknowledged by the bridge. SentAt is when the sender sent the message.
	Kind   string `json:"kind,omitempty"`
	SentAt string `json:"sent_at,omitempty"`
	// Network is set (by the local bridge) on a message that came from another
	// network the chat joined with --also; empty means the chat's own network.
	Network string `json:"network,omitempty"`
}

type AgentAttachmentOptions struct {
	RejectLiveMove bool   `json:"reject_live_move,omitempty"`
	AgentName      string `json:"agent_name"`
	Purpose        string `json:"purpose"`
	Provider       string `json:"provider"`
	SessionID      string `json:"native_session_id"`
	// ClientBuild is the CLI build that makes the attachment, so the server can tell which agents run an older one.
	ClientBuild string `json:"client_build,omitempty"`
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

// streamIdleLimit is how long a live stream may stay silent before it is dropped and reopened; tests shorten it.
var streamIdleLimit = 45 * time.Second

// StreamAgentMessages keeps one authenticated connection open. The server
// pushes on durable writes; this never polls the messages tool. Reconnect and
// adapter de-duplication belong to the caller, which owns its native session.
func (c *Client) StreamAgentMessages(ctx context.Context, attachment AgentAttachment, receive func(AgentLiveMessage) error, connected ...func()) error {
	token, err := c.authToken(ctx)
	if err != nil {
		return err
	}
	q := url.Values{"agent_name": {attachment.AgentName}, "attachment_id": {attachment.ID}}
	// The server sends a frame at least every 10 s (heartbeats included). A connection that goes quiet for streamIdleLimit is
	// half open (sleep, a network change): drop it so the caller reconnects instead of waiting forever.
	ctx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	idle := time.AfterFunc(streamIdleLimit, cancelStream)
	defer idle.Stop()
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
		idle.Reset(streamIdleLimit)
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
		if frame.Type == "connected" {
			if frame.AttachmentID != attachment.ID {
				return errors.New("agent stream does not match this attachment")
			}
			for _, notify := range connected {
				notify()
			}
			continue
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

// ShareAgent publishes (share) or takes back (unshare) one of the person's own
// agents on the network, with the person's own CLI login.
// An agent connection is refused.
func (c *Client) ShareAgent(ctx context.Context, action, agentName string) (json.RawMessage, error) {
	return c.request(ctx, http.MethodPost, "/api/external/v1/agents/sharing", map[string]any{"action": action, "agent_name": agentName}, true)
}
