package server

import (
	"context"
	"strings"
)

// The chat submission journal already requires an Idempotency-Key per user
// message; that key is the message's stable client identity. It names the
// durable user_message row so the browser reconciles its provisional bubble by
// id and a retried delivery lands on the same journal row.
const clientMessageEventIDPrefix = "user:"

// clientMessageIDHeader carries the message's first Idempotency-Key on a
// retry that had to rotate the key (409 turn_running).
const clientMessageIDHeader = "X-Client-Message-ID"

func clientMessageIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	submission, ok := ctx.Value(chatSubmissionContextKey{}).(chatSubmissionContext)
	if !ok {
		return ""
	}
	if submission.ClientMessageID != "" {
		return submission.ClientMessageID
	}
	return sanitizeClientMessageID(submission.ID)
}

func sanitizeClientMessageID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 128 {
		return ""
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.', r == ':':
		default:
			return ""
		}
	}
	return id
}

func clientUserMessageEventID(clientMessageID string) string {
	if clientMessageID == "" {
		return ""
	}
	return clientMessageEventIDPrefix + clientMessageID
}
