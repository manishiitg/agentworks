package agentworksclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
	"unicode"
)

// A hint contains no message body and confers no admission receipt. The receiver
// obtains bodies through its exact-session MCP receive tool or lifecycle hook.
type MuseWatchHint struct {
	Kind         string `json:"kind"`
	Source       string `json:"source"`
	SessionID    string `json:"session_id,omitempty"`
	AttachmentID string `json:"attachment_id,omitempty"`
	MessageID    string `json:"message_id,omitempty"`
	InboxID      string `json:"inbox_id,omitempty"`
	Sender       string `json:"sender,omitempty"`
	Error        string `json:"error,omitempty"`
	Pending      int    `json:"pending,omitempty"`
	// Note tells the chat what a notice is: it never carries the message.
	Note string `json:"note,omitempty"`
}

type museWatchState struct {
	cancel context.CancelFunc
	ready  bool
}

func museWatchActive(s *localSession) bool { return s.museWatch != nil && s.museWatch.ready }

func stopMuseWatch(s *localSession) {
	if s.museWatch != nil {
		s.museWatch.cancel()
		s.museWatch = nil
	}
}

// WatchMuseBridge holds one private Unix HTTP stream. Only the daemon owns the
// authenticated remote SSE connection; this child neither logs in nor polls.
func WatchMuseBridge(ctx context.Context, root string, in LocalBridgeRequest, offer func(MuseWatchHint) error) error {
	if in.Provider != "muse" || in.SessionID == "" || in.AttachmentID == "" || in.HostKey == "" {
		return errors.New("Muse watch requires its exact session, host and approved lease")
	}
	for _, path := range []string{root, filepath.Join(root, "bridge.sock")} {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if CheckPrivateAccess(path, info) != nil || path == root && !info.IsDir() || path != root && info.Mode()&os.ModeSocket == 0 {
			return errors.New("unsafe private bridge path")
		}
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(root, "bridge.sock"))
	}}
	defer transport.CloseIdleConnections()
	in.Action = "watch"
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://local/bridge", bytes.NewReader(body))
	if err != nil {
		return err
	}
	response, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 4096)
	for scanner.Scan() {
		var hint MuseWatchHint
		if err = json.Unmarshal(scanner.Bytes(), &hint); err != nil {
			return err
		}
		if hint.Error != "" {
			return errors.New(hint.Error)
		}
		if hint.Source != "agentworks-muse-watch" || hint.SessionID != in.SessionID || hint.AttachmentID != in.AttachmentID {
			return errors.New("Muse watch stream changed session or lease")
		}
		if hint.Kind == "offer" || hint.Kind == "pending" {
			if err = offer(hint); err != nil {
				return err
			}
		} else if hint.Kind != "ready" {
			return errors.New("invalid Muse watch event")
		}
	}
	return scanner.Err()
}

func (b *localBridge) serveMuseWatch(serverCtx context.Context, w http.ResponseWriter, r *http.Request, in LocalBridgeRequest) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	encoder := json.NewEncoder(w)
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	failed := func(reason string) {
		log.Printf("[BRIDGE] Muse watch for lease %s ended: %s", boundedMuseHint(in.AttachmentID), reason)
		_ = encoder.Encode(MuseWatchHint{Kind: "error", Source: "agentworks-muse-watch", Error: reason})
		flusher.Flush()
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	state := &museWatchState{cancel: cancel}
	key := localBridgeKey(in.Provider, in.SessionID)
	b.mu.Lock()
	s := b.sessions[key]
	if in.Provider != "muse" || s == nil || s.Offline || s.client == nil || in.HostKey == "" || s.HostKey != in.HostKey || in.AttachmentID == "" || s.Attachment.ID != in.AttachmentID || b.closed {
		b.mu.Unlock()
		failed("Muse watch is not bound to an active approved exact session")
		return
	}
	if current := b.museCurrent[in.HostKey]; current != "" && current != in.SessionID {
		b.mu.Unlock()
		failed("Muse switched to a different native session")
		return
	}
	if s.museWatch != nil {
		b.mu.Unlock()
		failed("this exact Muse lease already has a Monitor watch")
		return
	}
	if b.options.HostAlive != nil && !b.options.HostAlive("muse", s.HostKey) {
		b.mu.Unlock()
		failed("Muse host process ended")
		return
	}

	// Reserve the lease before unlocking, but it is not an active route until
	// authorization succeeds. Other chats never wait on this network request.
	s.museWatch = state
	client, name := s.client, s.Attachment.AgentName
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		if s.museWatch == state {
			s.museWatch = nil
		}
		b.mu.Unlock()
	}()
	valid := func() bool { // caller holds b.mu
		return !b.closed && b.sessions[key] == s && !s.Offline && s.HostKey == in.HostKey && s.Attachment.ID == in.AttachmentID && s.museWatch == state && ctx.Err() == nil && (b.museCurrent[in.HostKey] == "" || b.museCurrent[in.HostKey] == in.SessionID)
	}
	for {
		_, err := client.request(ctx, http.MethodPost, "/api/external/v1/agents/attachment", map[string]any{"action": "validate", "agent_name": name, "attachment_id": in.AttachmentID, "message_ids": []string{}}, true)
		if err == nil {
			break
		}
		if !museWatchRetry(ctx, serverCtx, err) {
			failed("Muse attachment is no longer authorized")
			return
		}
	}
	b.mu.Lock()
	if !valid() {
		b.mu.Unlock()
		log.Printf("[BRIDGE] Muse watch for lease %s ended: the lease, host or session changed", boundedMuseHint(in.AttachmentID))
		return
	}
	state.ready = true
	b.mu.Unlock()
	ready := MuseWatchHint{Kind: "ready", Source: "agentworks-muse-watch", SessionID: in.SessionID, AttachmentID: in.AttachmentID}
	if encoder.Encode(ready) != nil {
		return
	}
	flusher.Flush()
	hintedThrough := 0
	for {
		b.mu.Lock()
		if !valid() {
			b.mu.Unlock()
			return
		}
		latest, position := s.latestSequence, s.ReadPosition
		changed := b.changed
		b.mu.Unlock()
		if latest > hintedThrough && latest > position {
			hint := ready
			hint.Kind, hint.Pending = "pending", 1
			hint.Note = "Messages are waiting. Call the agentworks tool (action=receive) in this chat to read them."
			if encoder.Encode(hint) != nil {
				return
			}
			flusher.Flush()
			hintedThrough = latest
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return
		case <-serverCtx.Done():
			return
		}
	}
}

func boundedMuseHint(value string) string {
	runes := []rune(value)
	if len(runes) > 128 {
		runes = runes[:128]
	}
	for i, r := range runes {
		if unicode.IsControl(r) {
			runes[i] = ' '
		}
	}
	return string(runes)
}

// museWatchRetryDelay is how long a watch waits before checking its lease again.
var museWatchRetryDelay = 5 * time.Second

// museWatchRetry reports whether a failed lease check is only the server being slow or unreachable
// (so the watch waits and tries again) rather than the server refusing the lease. A watch that
// ended on a slow server left the chat deaf until its next prompt re-armed it. It waits first.
func museWatchRetry(ctx, serverCtx context.Context, err error) bool {
	var apiErr *APIError
	if ctx.Err() != nil || serverCtx.Err() != nil || errors.As(err, &apiErr) && apiErr.Status >= 400 && apiErr.Status < 500 && apiErr.Status != 408 && apiErr.Status != 429 {
		return false
	}
	log.Printf("[BRIDGE] Muse watch lease check failed, retrying: %v", err)
	timer := time.NewTimer(museWatchRetryDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
	case <-serverCtx.Done():
	}
	return false
}
