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
	SenderID     string `json:"sender_id"`
	Message      string `json:"message"`
	Sequence     int    `json:"sequence"`
	Delivery     string `json:"delivery"`
}

type AgentAttachmentOptions struct {
	AgentName      string   `json:"agent_name"`
	Purpose        string   `json:"purpose"`
	Provider       string   `json:"provider"`
	SessionID      string   `json:"native_session_id"`
	AllowedSenders []string `json:"allowed_senders"`
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
