package agentworksclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// nativeHandbackRetry is how long an offered answer may go unseen in the chat's
// journal before it is offered again; it doubles per attempt up to 30 minutes.
const nativeHandbackRetry = 2 * time.Minute

type nativeHandbackPending struct {
	Attachment AgentAttachment `json:"attachment"`
	Project    string          `json:"project"`
	Agent      string          `json:"agent"`
	IDs        []string        `json:"ids"`
	Text       string          `json:"text"`
	Offset     int64           `json:"offset"`
	TailHash   string          `json:"tail_hash"`
	Attempted  bool            `json:"attempted"`
	Confirmed  bool            `json:"confirmed"`
	// AttemptedAt and Attempts bound the re-offer when the journal never shows the offered text.
	AttemptedAt time.Time `json:"attempted_at,omitempty"`
	Attempts    int       `json:"attempts,omitempty"`
}

// nativeHandbackDue: an unconfirmed offer is offered again once its wait has passed
// (an offer saved before AttemptedAt existed is due at once).
func nativeHandbackDue(p *nativeHandbackPending, now time.Time) bool {
	if !p.Attempted {
		return true
	}
	wait := nativeHandbackRetry << min(max(p.Attempts-1, 0), 4)
	return now.Sub(p.AttemptedAt) >= min(wait, 30*time.Minute)
}

// Only metadata at the last complete boundary, no full history scan or caller path.
func nativeJournalEnd(a AgentAttachment) (int64, string, error) {
	f, err := openExactNativeJournal(a)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, "", err
	}
	size := min(info.Size(), int64(nativeDeltaLimit))
	data := make([]byte, size)
	if _, err = f.ReadAt(data, info.Size()-size); err != nil && size != 0 {
		return 0, "", err
	}
	end := bytes.LastIndexByte(data, '\n') + 1
	if end == 0 && info.Size() > size {
		return 0, "", fmt.Errorf("native record exceeds receipt bound")
	}
	offset := info.Size() - size + int64(end)
	tail := make([]byte, min(offset, int64(4096)))
	if _, err = f.ReadAt(tail, offset-int64(len(tail))); err != nil && len(tail) != 0 {
		return 0, "", err
	}
	sum := sha256.Sum256(tail)
	return offset, hex.EncodeToString(sum[:]), nil
}

// A real USER record is required; assistant echoes, tools and hook stdout never count.
func nativeUserReceipt(provider string, line []byte, want string) bool {
	var row struct {
		Type    string `json:"type"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		Payload struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &row) != nil {
		return false
	}
	var content json.RawMessage
	if provider == "claude" && row.Type == "user" && row.Message.Role == "user" {
		content = row.Message.Content
	} else if provider == "codex" && row.Type == "response_item" && row.Payload.Type == "message" && row.Payload.Role == "user" {
		content = row.Payload.Content
	} else {
		return false
	}
	// The offered text ends in a newline; the app may store the record trimmed.
	want = strings.TrimSpace(want)
	var text string
	if json.Unmarshal(content, &text) == nil {
		return strings.TrimSpace(text) == want
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return false
	}
	var out strings.Builder
	for _, p := range parts {
		if p.Type != "text" && p.Type != "input_text" {
			return false
		}
		out.WriteString(p.Text)
	}
	return strings.TrimSpace(out.String()) == want
}

func (b *localBridge) saveNativeHandback(s *localSession, a AgentAttachment, p *nativeHandbackPending) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	current := false
	for _, v := range b.sessions {
		if v == s {
			current = true
			break
		}
	}
	if !current || b.closed || s.Offline || s.Attachment.ID != a.ID || s.Attachment.SessionID != a.SessionID || s.Attachment.Provider != a.Provider {
		return fmt.Errorf("native handback binding changed")
	}
	before := s.NativeHandback
	if p == nil {
		s.NativeHandback = nil
	} else {
		copy := *p
		copy.IDs = slices.Clone(p.IDs)
		s.NativeHandback = &copy
	}
	if err := b.save(); err != nil {
		s.NativeHandback = before
		return err
	}
	return nil
}

// Runs at an existing turn boundary, after current source admission. No polling,
// fabricated mailbox IDs, native file writes or acknowledgement of mere delivery.
func (b *localBridge) processNativeHandback(ctx context.Context, s *localSession, a AgentAttachment, c *Client, cur NativeCopyCursor) error {
	b.mu.Lock()
	current := !b.closed && !s.Offline && s.Attachment.ID == a.ID && s.Attachment.SessionID == a.SessionID && s.Attachment.Provider == a.Provider
	b.mu.Unlock()
	if !current {
		return fmt.Errorf("native handback source changed")
	}
	body := map[string]any{"provider": a.Provider, "session": a.SessionID, "attachment": a.ID}
	base := "/api/crew-teams/" + url.PathEscape(cur.Project) + "/agents/" + url.PathEscape(cur.Agent) + "/offline-copy/"
	raw, err := c.request(ctx, http.MethodPost, base+"handback", body, true)
	if err != nil {
		return err
	}
	var returned struct {
		Records []struct {
			ID string `json:"id"`
		} `json:"records"`
		Text string `json:"text"`
	}
	if err = json.Unmarshal(raw, &returned); err != nil {
		return err
	}
	if returned.Records == nil {
		return fmt.Errorf("invalid handback response")
	}
	if len(returned.Records) > 200 {
		return fmt.Errorf("handback batch exceeds bound")
	}
	ids := []string{}
	for _, record := range returned.Records {
		if record.ID == "" || slices.Contains(ids, record.ID) {
			return fmt.Errorf("invalid returned IDs")
		}
		ids = append(ids, record.ID)
	}
	b.mu.Lock()
	var p *nativeHandbackPending
	if s.NativeHandback != nil {
		copy := *s.NativeHandback
		copy.IDs = slices.Clone(copy.IDs)
		p = &copy
	}
	offer, remote := b.options.NativeUserOffer, s.NativeRemote
	b.mu.Unlock()
	if p != nil && (p.Attachment.ID != a.ID || p.Attachment.SessionID != a.SessionID || p.Attachment.Provider != a.Provider || p.Project != cur.Project || p.Agent != cur.Agent) {
		return fmt.Errorf("handback belongs to another source")
	}
	if len(ids) == 0 {
		return b.saveNativeHandback(s, a, nil)
	}
	if p == nil {
		if returned.Text == "" {
			return fmt.Errorf("returned user content missing")
		}
		if len(returned.Text) > 96<<10 {
			return fmt.Errorf("handback exceeds native input bound")
		}
		var quoted strings.Builder
		quoted.WriteString("> AgentWorks returned answer records: " + strings.Join(ids, ",") + "\n")
		for _, line := range strings.Split(returned.Text, "\n") {
			if quoted.Len()+len(line)+3 > 96<<10 {
				return fmt.Errorf("handback exceeds native input bound")
			}
			quoted.WriteString("> ")
			quoted.WriteString(line)
			quoted.WriteByte('\n')
		}
		text := quoted.String()
		offset, tail, e := nativeJournalEnd(a)
		if e != nil {
			return e
		}
		p = &nativeHandbackPending{Attachment: a, Project: cur.Project, Agent: cur.Agent, IDs: ids, Text: text, Offset: offset, TailHash: tail}
		if err = b.saveNativeHandback(s, a, p); err != nil {
			return err
		}
	}
	for _, id := range p.IDs {
		if !slices.Contains(ids, id) {
			return fmt.Errorf("returned source records changed")
		}
	}
	if p.Attempted && !p.Confirmed {
		data, tail, _, e := ReadNativeJournalDelta(a, p.Offset, p.TailHash)
		if e != nil {
			return e
		}
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			if nativeUserReceipt(a.Provider, line, p.Text) {
				p.Confirmed = true
				break
			}
		}
		p.Offset += int64(len(data))
		p.TailHash = tail
		if err = b.saveNativeHandback(s, a, p); err != nil {
			return err
		}
	}
	if p.Confirmed {
		body["ids"] = p.IDs
		if _, err = c.request(ctx, http.MethodPost, base+"handback-ack", body, true); err != nil {
			return err
		}
		log.Printf("[AGENT_OFFLINE_COPY] agent=%s attachment=%s event=handback_confirmed count=%d", a.AgentID, a.ID, len(p.IDs))
		return b.saveNativeHandback(s, a, nil)
	}
	// Runs at turn boundaries: an offer the chat never recorded is offered again
	// after a bounded wait, never on every boundary.
	if offer == nil || remote == "" || !nativeHandbackDue(p, time.Now()) {
		return nil
	}
	if p.Attempted {
		log.Printf("[AGENT_OFFLINE_COPY] agent=%s attachment=%s event=handback_reoffered attempt=%d", a.AgentID, a.ID, p.Attempts+1)
	}
	p.Attempted, p.AttemptedAt, p.Attempts = true, time.Now(), p.Attempts+1
	if err = b.saveNativeHandback(s, a, p); err != nil {
		return err
	}
	// Literal user-content callback only. A successful write/queue exit still
	// leaves the server record unacknowledged until a later journal observation.
	err = offer(ctx, remote, a, p.Text)
	if errors.Is(err, ErrNativeOfferRejected) {
		p.Attempted = false
		return b.saveNativeHandback(s, a, p)
	}
	return err
}
