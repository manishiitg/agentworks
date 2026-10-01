// Package gmailinbound receives mailbox wakeups, persists work, and dispatches
// email conversations. No watcher process is retained for an idle mailbox.
package gmailinbound

import (
	"encoding/base64"
	"fmt"
	"mime"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
)

type Route struct {
	Filters         *Filters          `json:"filters,omitempty"`
	Name            string            `json:"name,omitempty"`
	WorkflowTrigger bool              `json:"workflow_trigger,omitempty"`
	RouteSelections map[string]string `json:"route_selections,omitempty"`
	GroupNames      []string          `json:"group_names,omitempty"`
	StepID          string            `json:"step_id,omitempty"`
	ID              string            `json:"id"`
	OwnerID         string            `json:"owner_id"`
	ConnectionID    string            `json:"connection_id"`
	Address         string            `json:"address"`
	WorkspacePath   string            `json:"workspace_path"`
	ProfileID       string            `json:"profile_id,omitempty"`
	ProjectID       string            `json:"project_id"`
	Enabled         bool              `json:"enabled"`
	EnabledAt       int64             `json:"enabled_at"`
	Reply           bool              `json:"reply"`
}

type Mailbox struct {
	ConnectionID string
	Email        string
	Cursor       string
	Generation   int64
	Processed    int64
	RenewAt      int64
	Since        int64
	LastError    string
}

type Attachment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int    `json:"size"`
	Data string `json:"data,omitempty"`
}
type Message struct {
	ID            string       `json:"id"`
	ReceivedAt    int64        `json:"received_at"`
	ThreadID      string       `json:"thread_id"`
	IsReply       bool         `json:"is_reply,omitempty"`
	From          string       `json:"from"`
	Recipients    []string     `json:"recipients"`
	Subject       string       `json:"subject"`
	Body          string       `json:"body"`
	RFCMessageID  string       `json:"rfc_message_id"`
	Authenticated bool         `json:"authenticated"`
	Automatic     bool         `json:"automatic"`
	Attachments   []Attachment `json:"attachments,omitempty"`
}
type Delivery struct {
	ID        string
	Route     Route
	Message   Message
	SessionID string
	Response  string
	Status    string
}

// RawMessage is the lossless gog gmail raw (format=full) wire contract.
type RawMessage struct {
	ID           string   `json:"id"`
	ThreadID     string   `json:"threadId"`
	LabelIDs     []string `json:"labelIds"`
	InternalDate string   `json:"internalDate"`
	Payload      Part     `json:"payload"`
}
type Part struct {
	MimeType string `json:"mimeType"`
	Filename string `json:"filename"`
	Headers  []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"headers"`
	Body struct {
		Data         string `json:"data"`
		AttachmentID string `json:"attachmentId"`
		Size         int    `json:"size"`
	} `json:"body"`
	Parts []Part `json:"parts"`
}

func (p Part) header(name string) string {
	for _, h := range p.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

var dmarcResult = regexp.MustCompile(`(?i)\bdmarc=pass\b[^;]*\bheader\.from=([a-z0-9.-]+)`)
var htmlTag = regexp.MustCompile(`<[^>]*>`)

func (r RawMessage) Parse(account string) (Message, error) {
	from, err := mail.ParseAddress(r.Payload.header("From"))
	if err != nil {
		return Message{}, fmt.Errorf("invalid sender")
	}
	m := Message{ID: r.ID, ThreadID: r.ThreadID, From: strings.ToLower(from.Address), Subject: r.Payload.header("Subject"), RFCMessageID: r.Payload.header("Message-ID")}
	m.IsReply = strings.TrimSpace(r.Payload.header("In-Reply-To")) != "" || strings.TrimSpace(r.Payload.header("References")) != ""
	m.ReceivedAt, _ = strconv.ParseInt(r.InternalDate, 10, 64)
	if subject, e := new(mime.WordDecoder).DecodeHeader(m.Subject); e == nil {
		m.Subject = subject
	}
	for _, name := range []string{"To", "Cc", "Delivered-To", "X-Original-To"} {
		list, _ := mail.ParseAddressList(r.Payload.header(name))
		for _, a := range list {
			m.Recipients = append(m.Recipients, strings.ToLower(a.Address))
		}
	}
	m.Automatic = r.Payload.header("Auto-Submitted") != "" && !strings.EqualFold(r.Payload.header("Auto-Submitted"), "no")
	m.Automatic = m.Automatic || r.Payload.header("List-ID") != "" || r.Payload.header("Return-Path") == "<>"
	for _, label := range r.LabelIDs {
		if label == "SPAM" || label == "TRASH" {
			m.Automatic = true
		}
		if label == "SENT" && strings.EqualFold(from.Address, account) {
			m.Authenticated = true
		}
	}
	// Trust only the first Gmail authentication result. A later forged pass
	// must never override Google's own fail result.
	for _, h := range r.Payload.Headers {
		if strings.EqualFold(h.Name, "Authentication-Results") && strings.HasPrefix(strings.TrimSpace(h.Value), "mx.google.com;") {
			match := dmarcResult.FindStringSubmatch(h.Value)
			domain := strings.SplitN(m.From, "@", 2)
			if len(match) == 2 && len(domain) == 2 && strings.EqualFold(match[1], domain[1]) {
				m.Authenticated = true
			}
			break
		}
	}
	var plain, html strings.Builder
	var walk func(Part) error
	walk = func(p Part) error {
		if p.Filename != "" {
			if p.Body.AttachmentID != "" || p.Body.Data != "" {
				m.Attachments = append(m.Attachments, Attachment{ID: p.Body.AttachmentID, Name: p.Filename, Size: p.Body.Size, Data: p.Body.Data})
			}
			return nil
		}
		if p.Body.Data != "" {
			b, e := base64.RawURLEncoding.DecodeString(strings.TrimRight(p.Body.Data, "="))
			if e != nil {
				return e
			}
			if p.MimeType == "text/plain" {
				plain.Write(b)
			} else if p.MimeType == "text/html" {
				html.Write(b)
			}
		}
		for _, child := range p.Parts {
			if e := walk(child); e != nil {
				return e
			}
		}
		return nil
	}
	if err := walk(r.Payload); err != nil {
		return Message{}, err
	}
	m.Body = plain.String()
	if m.Body == "" {
		m.Body = htmlTag.ReplaceAllString(html.String(), " ")
	}
	if len(m.Body) > 256*1024 {
		return Message{}, fmt.Errorf("email body exceeds 256 KiB")
	}
	if len(m.Attachments) > 20 {
		return Message{}, fmt.Errorf("too many attachments")
	}
	return m, nil
}
