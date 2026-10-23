package agentworksclient

import (
	"encoding/json"
	"errors"
	"fmt"
)

// The helper, which updates with the CLI, owns the public messaging contract.
// The MCP child forwards JSON and supplies native identity separately. Never
// decode routing or account fields from model-supplied arguments.
func messagingRequest(bound LocalBridgeRequest) (LocalBridgeRequest, error) {
	action, _ := bound.Arguments["action"].(string)
	switch action {
	case "peers", "send", "read", "reply", "ack", "count", "delete", "clear", "ping_person", "get_file":
	case "set_purpose":
		action = "purpose"
	default:
		return LocalBridgeRequest{}, errors.New("Choose a messaging action: peers, send, read, reply, ack, set_purpose, ping_person, get_file, count, delete or clear. Connect, disconnect and publish use their own chat actions.")
	}
	for key, value := range bound.Arguments {
		if value == nil {
			return LocalBridgeRequest{}, fmt.Errorf("Omit %s when it is not needed; do not pass null", key)
		}
	}
	var args struct {
		Target        string `json:"target"`
		Message       string `json:"message"`
		SubmissionID  string `json:"submission_id"`
		InboxID       string `json:"inbox_id"`
		MessageID     string `json:"message_id"`
		Delivery      string `json:"delivery"`
		Purpose       string `json:"purpose"`
		Network       string `json:"network"`
		After         int    `json:"after"`
		OlderThan     string `json:"older_than"`
		FromAgent     string `json:"from_agent"`
		DeliveredOnly bool   `json:"delivered_only"`
		UnreadOnly    bool   `json:"unread_only"`
		FileName      string `json:"file_name"`
		FileBase64    string `json:"file_base64"`
		FileID        string `json:"file_id"`
	}
	raw, err := json.Marshal(bound.Arguments)
	if err == nil {
		err = json.Unmarshal(raw, &args)
	}
	if err != nil {
		return LocalBridgeRequest{}, fmt.Errorf("Check the message arguments: %w", err)
	}
	return LocalBridgeRequest{
		Action: action, Provider: bound.Provider, SessionID: bound.SessionID, HostKey: bound.HostKey, AttachmentID: bound.AttachmentID,
		Target: args.Target, Message: args.Message, SubmissionID: args.SubmissionID,
		InboxID: args.InboxID, MessageID: args.MessageID, Delivery: args.Delivery,
		Purpose: args.Purpose, Network: args.Network, Cursor: args.After,
		OlderThan: args.OlderThan, FromAgent: args.FromAgent, DeliveredOnly: args.DeliveredOnly, UnreadOnly: args.UnreadOnly,
		FileName: args.FileName, FileBase64: args.FileBase64, FileID: args.FileID,
	}, nil
}
