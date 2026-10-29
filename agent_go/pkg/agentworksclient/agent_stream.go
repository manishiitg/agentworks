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
	ClientBuild   string    `json:"client_build,omitempty"`
	Repository    string    `json:"repository,omitempty"`
	Branch        string    `json:"branch,omitempty"`
	Activity      string    `json:"activity,omitempty"`
	ActivityAt    time.Time `json:"activity_at,omitempty"`
	DeliveryRoute string    `json:"delivery_route,omitempty"`
	ID            string    `json:"attachment_id"`
	AgentID       string    `json:"agent_id"`
	AgentName     string    `json:"agent_name"`
	Label         string    `json:"label"`
	Purpose       string    `json:"purpose"`
	Provider      string    `json:"provider"`
	SessionID     string    `json:"native_session_id"`
}

type AgentLiveMessage struct {
	AgentID      string `json:"agent_id,omitempty"`
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
	// Kind is "ping" for a server-authored person notification consumed by the helper.
	// SentAt is when the sender sent the message.
	Kind   string `json:"kind,omitempty"`
	SentAt string `json:"sent_at,omitempty"`
	// Network is set (by the local bridge) on a message that came from another
	// network the chat joined with --also; empty means the chat's own network.
	Network string `json:"network,omitempty"`
}

type AgentAttachmentOptions struct {
	AttachmentID string `json:"attachment_id,omitempty"` // reconnect only this still-current attachment
	AgentName    string `json:"agent_name"`
	Label        string `json:"label"`
	Purpose      string `json:"purpose"`
	Provider     string `json:"provider"`
	SessionID    string `json:"native_session_id"`
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

// UpdateAgentLabel changes only presentation, using the same exact lease.
func (c *Client) UpdateAgentLabel(ctx context.Context, a AgentAttachment, label string) error {
	body := map[string]any{"action": "label", "agent_name": a.AgentName, "attachment_id": a.ID, "label": label, "client_build": a.ClientBuild}
	body["delivery_route"] = a.DeliveryRoute // Empty clears an adapter that is no longer observed.
	body["repository"], body["branch"] = a.Repository, a.Branch
	body["activity"], body["activity_at"] = a.Activity, a.ActivityAt
	_, err := c.request(ctx, http.MethodPost, "/api/external/v1/agents/attachment", body, true)
	return err
}

// streamIdleLimit is how long a live stream may stay silent before it is dropped and reopened; tests shorten it.
var streamIdleLimit = 45 * time.Second

// StreamAgentMessages carries inbox wake hints and explicit person pings on one
// authenticated connection. Peer bodies come only from the bounded inbox read.
func (c *Client) StreamAgentMessages(ctx context.Context, attachment AgentAttachment, receive func(AgentLiveMessage) error, connected ...func()) error {
	if attachment.AgentName == "" || attachment.ID == "" {
		return errors.New("exact attachment required")
	}
	return c.streamAgentMessages(ctx, attachment, receive, connected...)
}

// StreamAgentWakeHints releases metadata only, never incoming message bodies.
func (c *Client) StreamAgentWakeHints(ctx context.Context, receive func(AgentLiveMessage) error, connected ...func()) error {
	return c.streamAgentMessages(ctx, AgentAttachment{}, receive, connected...)
}

func (c *Client) streamAgentMessages(ctx context.Context, attachment AgentAttachment, receive func(AgentLiveMessage) error, connected ...func()) error {
	account := attachment.ID == ""
	token, err := c.authToken(ctx)
	if err != nil {
		return err
	}
	q := url.Values{"agent_name": {attachment.AgentName}, "attachment_id": {attachment.ID}}
	if account {
		q = url.Values{}
	}
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
		if frame.Type == "login_changed" {
			return &APIError{Status: 401, Code: "agent_login_changed", Message: "The saved sign-in needs to be reloaded."}
		}
		if frame.Type == "closed" {
			return &APIError{Status: 403, Code: "agent_attachment_closed", Message: "The attachment or its connection is no longer authorized."}
		}
		if frame.Type == "inbox_changed" {
			if account {
				if frame.AgentID == "" || frame.Sequence <= 0 || frame.Message != "" || frame.Sender != "" {
					return errors.New("invalid account wake metadata")
				}
				if err = receive(AgentLiveMessage{AgentID: frame.AgentID, AttachmentID: frame.AttachmentID, Sequence: frame.Sequence}); err != nil {
					return err
				}
				continue
			}
			if frame.AttachmentID != attachment.ID || frame.Sequence <= 0 {
				return errors.New("inbox signal does not match this attachment")
			}
			if err = receive(AgentLiveMessage{AttachmentID: frame.AttachmentID, Sequence: frame.Sequence}); err != nil {
				return err
			}
			continue
		}
		if account {
			continue
		}
		if frame.Type != "message" || frame.Kind != "ping" {
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
