package agentworksclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// LocalBridgeRequest is the adapter boundary. Attachment/configuration is a
// person-only CLI or prompt-submit action, never exposed as an MCP tool.
type LocalBridgeRequest struct {
	Action         string   `json:"action"`
	AttachmentID   string   `json:"attachment_id,omitempty"`
	Provider       string   `json:"provider"`
	SessionID      string   `json:"session_id"`
	HostKey        string   `json:"host_key,omitempty"`
	NativeRemote   string   `json:"native_remote,omitempty"`
	ConfigPath     string   `json:"config_path,omitempty"`
	AgentName      string   `json:"agent_name,omitempty"`
	Purpose        string   `json:"purpose,omitempty"`
	AllowedSenders []string `json:"allowed_senders,omitempty"`
	MessageID      string   `json:"message_id,omitempty"`
	Delivery       string   `json:"delivery,omitempty"`
	Transport      string   `json:"transport,omitempty"`
	Message        string   `json:"message,omitempty"`
	SubmissionID   string   `json:"submission_id,omitempty"`
	Target         string   `json:"target,omitempty"`
	InboxID        string   `json:"inbox_id,omitempty"`
	Cursor         int      `json:"after,omitempty"`
}

type LocalBridgeReply struct {
	Attached   bool               `json:"attached"`
	Attachment AgentAttachment    `json:"attachment,omitempty"`
	Messages   []AgentLiveMessage `json:"messages,omitempty"`
	Error      string             `json:"error,omitempty"`
	Result     json.RawMessage    `json:"result,omitempty"`
	Sessions   []AgentAttachment  `json:"sessions,omitempty"`
	LastError  string             `json:"last_error,omitempty"`
	Offline    bool               `json:"offline,omitempty"`
	Pending    int                `json:"pending"`
}

type localOffer struct {
	AgentLiveMessage
	Offered    bool      `json:"offered"`
	Suppressed bool      `json:"suppressed,omitempty"`
	Transport  string    `json:"transport,omitempty"`
	OfferedAt  time.Time `json:"offered_at,omitempty"`
}

type localSession struct {
	SeenThrough      int                   `json:"seen_through,omitempty"`
	Offline          bool                  `json:"offline,omitempty"`
	ParentConfigPath string                `json:"parent_config_path,omitempty"`
	AllowedSenders   []string              `json:"allowed_senders,omitempty"`
	Attachment       AgentAttachment       `json:"attachment"`
	HostKey          string                `json:"host_key,omitempty"`
	NativeRemote     string                `json:"native_remote,omitempty"`
	LastError        string                `json:"last_error,omitempty"`
	ConfigPath       string                `json:"config_path"`
	Offers           map[string]localOffer `json:"offers"`
	client           *Client
	cancel           context.CancelFunc
	nativeCalls      int
}

type localBridge struct {
	mu       sync.Mutex
	root     string
	sessions map[string]*localSession
	changed  chan struct{}
	options  LocalBridgeOptions
	workers  sync.WaitGroup
	closed   bool
}

var ErrNativeOfferUnconfirmed = errors.New("native offer has no admission receipt")

type LocalBridgeOptions struct {
	NativeOffer func(context.Context, string, AgentAttachment, []AgentLiveMessage) error
	Notify      func(AgentAttachment)
}

func DefaultLocalBridgeRoot() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agentworks", "bridge"), nil
}

func localBridgeKey(provider, session string) string { return provider + ":" + session }

// CallLocalBridge uses only the private local socket; it never signs in or
// starts a listener. In particular an unattached hook cannot contact the server.
func CallLocalBridge(ctx context.Context, root string, request LocalBridgeRequest) (LocalBridgeReply, error) {
	var out LocalBridgeReply
	info, err := os.Lstat(root)
	if err != nil {
		return out, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return out, errors.New("unsafe local bridge directory")
	}
	socket := filepath.Join(root, "bridge.sock")
	info, err = os.Lstat(socket)
	if err != nil {
		return out, err
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm()&0077 != 0 {
		return out, errors.New("unsafe local bridge socket")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	b, err := json.Marshal(request)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://local/bridge", bytes.NewReader(b))
	if err != nil {
		return out, err
	}
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return out, err
	}
	if out.Error != "" {
		return out, errors.New(out.Error)
	}
	return out, nil
}

func localConfiguredClient(path string) (*Client, error) {
	cfg, err := LoadConfig(path)
	if err != nil {
		return nil, err
	}
	c, err := New(cfg.Server, cfg.Token)
	if err != nil {
		return nil, err
	}
	if cfg.RefreshToken != "" {
		c.WithTokenProvider(ConfigTokenProvider(cfg.Server, path))
	}
	return c, nil
}

func (b *localBridge) save() error {
	return atomicLiveJSON(filepath.Join(b.root, "sessions.json"), b.sessions)
}
func (b *localBridge) wake() { close(b.changed); b.changed = make(chan struct{}) }

// ServeLocalBridge holds streaming connections for explicitly attached sessions.
// It makes no mailbox polling requests. Current server API has one stream per
// attachment; a multiplexed server stream remains a separate scaling change.
func ServeLocalBridge(ctx context.Context, root string, options ...LocalBridgeOptions) error {
	if err := privateLiveDir(root); err != nil {
		return err
	}
	unlock, err := liveFileLock(filepath.Join(root, "bridge.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	socket := filepath.Join(root, "bridge.sock")
	if info, e := os.Lstat(socket); e == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("refusing redirected bridge socket")
		}
		if e = os.Remove(socket); e != nil {
			return e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	l, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer l.Close()
	if err = os.Chmod(socket, 0600); err != nil {
		return err
	}
	b := &localBridge{root: root, sessions: map[string]*localSession{}, changed: make(chan struct{})}
	if len(options) > 0 {
		b.options = options[0]
	}
	if err = readLiveJSON(filepath.Join(root, "sessions.json"), &b.sessions); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if b.sessions == nil {
		b.sessions = map[string]*localSession{}
	}
	for _, s := range b.sessions {
		if s == nil {
			return errors.New("invalid attached session state")
		}
		for _, v := range s.Offers {
			if v.Sequence > s.SeenThrough {
				s.SeenThrough = v.Sequence
			}
		}
		if s.Offers == nil {
			s.Offers = map[string]localOffer{}
		}
		s.client, err = localConfiguredClient(s.ConfigPath)
		if err != nil {
			s.LastError = "Attached connection unavailable; sign in and explicitly reattach."
			continue
		}
	}
	for _, s := range b.sessions {
		if s.client != nil && !s.Offline {
			b.start(ctx, s)
		}
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/bridge" {
			http.NotFound(w, r)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		var in LocalBridgeRequest
		out := LocalBridgeReply{}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			out.Error = "invalid local bridge request"
		} else {
			out = b.handle(ctx, r.Context(), in)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 3 * time.Second}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	defer func() {
		b.mu.Lock()
		b.closed = true
		for _, s := range b.sessions {
			if s.cancel != nil {
				s.cancel()
			}
		}
		b.wake()
		b.mu.Unlock()
		b.workers.Wait()
	}()
	err = srv.Serve(l)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (b *localBridge) start(ctx context.Context, s *localSession) {
	streamCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	if s.NativeRemote != "" && b.options.NativeOffer != nil {
		b.workers.Add(1)
		hostKey := s.HostKey
		go func() { defer b.workers.Done(); b.nativeLoop(streamCtx, s, hostKey) }()
	}
	b.workers.Add(1)
	go func() {
		defer b.workers.Done()
		backoff := time.Second
		for streamCtx.Err() == nil {
			err := s.client.StreamAgentMessages(streamCtx, s.Attachment, func(m AgentLiveMessage) error {
				b.mu.Lock()
				defer b.mu.Unlock()
				if b.closed || streamCtx.Err() != nil {
					return context.Canceled
				}
				if s.Offers[m.MessageID].MessageID != "" || m.Sequence <= s.SeenThrough {
					return nil
				}
				compactLocalOffers(s, false)
				if len(s.Offers) >= 2000 {
					return errors.New("local pending journal is full; use agent_message_read to recover and acknowledge pending messages, then /agentworks cleanup")
				}
				previousThrough := s.SeenThrough
				s.SeenThrough = max(s.SeenThrough, m.Sequence)
				s.Offers[m.MessageID] = localOffer{AgentLiveMessage: m}
				if err := b.save(); err != nil {
					delete(s.Offers, m.MessageID)
					s.SeenThrough = previousThrough
					return err
				}
				b.wake()
				if b.options.Notify != nil && s.NativeRemote == "" {
					go b.options.Notify(s.Attachment)
				}
				return nil
			})
			b.mu.Lock()
			b.wake()
			b.mu.Unlock()
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.Code != "agent_attachment_closed" && (apiErr.Status == 401 || apiErr.Status == 403 || apiErr.Status == 404) {
				return
			}
			timer := time.NewTimer(backoff)
			select {
			case <-streamCtx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			if backoff < 30*time.Second {
				backoff *= 2
				if backoff > 30*time.Second {
					backoff = 30 * time.Second
				}
			}
		}
	}()
}

func (b *localBridge) handle(serverCtx, callCtx context.Context, in LocalBridgeRequest) LocalBridgeReply {
	if len(in.SessionID) > 128 || len(in.HostKey) > 128 {
		return LocalBridgeReply{Error: "invalid native session"}
	}
	if in.Action == "ping" {
		return LocalBridgeReply{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return LocalBridgeReply{Error: "local bridge stopping"}
	}
	key := localBridgeKey(in.Provider, in.SessionID)
	s := b.sessions[key]
	if in.SessionID == "" && in.HostKey != "" {
		for k, v := range b.sessions {
			if v.Attachment.Provider == in.Provider && v.HostKey == in.HostKey {
				if s != nil {
					return LocalBridgeReply{Error: "exact session ID required for a shared provider process"}
				}
				key, s = k, v
			}
		}
	}
	if in.Action == "list" {
		out := LocalBridgeReply{}
		for _, session := range b.sessions {
			out.Sessions = append(out.Sessions, session.Attachment)
		}
		sort.Slice(out.Sessions, func(i, j int) bool { return out.Sessions[i].AgentName < out.Sessions[j].AgentName })
		return out
	}
	if in.Action == "resume" {
		if s == nil || !s.Offline {
			return LocalBridgeReply{Error: "This exact session has no offline attachment to resume."}
		}
		in.Action = "attach"
		in.ConfigPath = s.ParentConfigPath
		in.AgentName = s.Attachment.AgentName
		in.Purpose = s.Attachment.Purpose
		in.AllowedSenders = s.AllowedSenders
		in.NativeRemote = s.NativeRemote
	}
	if in.Action == "attach" {
		if in.NativeRemote != "" && (!((in.Provider == "codex" && !strings.HasPrefix(in.NativeRemote, "claude+session://")) || (in.Provider == "claude" && strings.HasPrefix(in.NativeRemote, "claude+session://"))) || b.options.NativeOffer == nil) {
			return LocalBridgeReply{Error: "native queue is unavailable for this provider"}
		}
		if in.SessionID == "" || in.HostKey == "" || in.Purpose == "" || in.AllowedSenders == nil {
			return LocalBridgeReply{Error: "explicit session, provider process key, purpose and sender allowlist required"}
		}
		client, err := localConfiguredClient(in.ConfigPath)
		if err != nil {
			return LocalBridgeReply{Error: err.Error()}
		}
		if _, err = client.Call(callCtx, "messages", map[string]any{"action": "register", "agent_name": in.AgentName, "purpose": in.Purpose}); err != nil {
			return LocalBridgeReply{Error: err.Error()}
		}
		connectionDir := filepath.Join(b.root, "connections")
		if err = privateLiveDir(connectionDir); err != nil {
			return LocalBridgeReply{Error: err.Error()}
		}
		connectionPath := ""
		var previous *localSession
		for _, candidate := range b.sessions {
			if candidate.Attachment.AgentName == in.AgentName && candidate.ParentConfigPath == in.ConfigPath {
				previous, connectionPath = candidate, candidate.ConfigPath
				break
			}
		}
		newConnection := false
		create := func() error {
			cfg, e := client.CreateAgentConnection(callCtx, in.AgentName)
			if e != nil {
				return e
			}
			connectionPath = filepath.Join(connectionDir, liveHash(cfg.Server, in.AgentName, in.SessionID, time.Now().String())+".json")
			if e = SaveConfig(connectionPath, cfg); e != nil {
				_ = client.RevokeOAuth(callCtx, cfg.RefreshToken)
				return e
			}
			newConnection = true
			return nil
		}
		if connectionPath == "" {
			if err = create(); err != nil {
				return LocalBridgeReply{Error: err.Error()}
			}
		}
		client, err = localConfiguredClient(connectionPath)
		if err != nil {
			return LocalBridgeReply{Error: err.Error()}
		}

		options := AgentAttachmentOptions{AgentName: in.AgentName, Purpose: in.Purpose, Provider: in.Provider, SessionID: in.SessionID, AllowedSenders: in.AllowedSenders}
		a, err := client.AttachAgent(callCtx, options)
		var apiErr *APIError
		if err != nil && !newConnection && errors.As(err, &apiErr) && (apiErr.Status == 401 || apiErr.Status == 403 || apiErr.Status == 400 && apiErr.Code == "invalid_grant") {
			// Re-login/expiry requires a new child from the currently approved
			// parent, once. Never retry an ambiguous attachment write.
			client, err = localConfiguredClient(in.ConfigPath)
			if err == nil {
				err = create()
			}
			if err == nil {
				client, err = localConfiguredClient(connectionPath)
			}
			if err == nil {
				a, err = client.AttachAgent(callCtx, options)
			}
		}
		if err != nil {
			if newConnection {
				if cfg, e := LoadConfig(connectionPath); e == nil {
					_ = client.RevokeOAuth(callCtx, cfg.RefreshToken)
				}
				_ = os.Remove(connectionPath)
			}
			return LocalBridgeReply{Error: err.Error()}
		}
		next := &localSession{Attachment: a, HostKey: in.HostKey, NativeRemote: in.NativeRemote, ConfigPath: connectionPath, ParentConfigPath: in.ConfigPath, AllowedSenders: in.AllowedSenders, Offers: map[string]localOffer{}, client: client}
		if previous != nil && previous.Attachment.AgentID == a.AgentID {
			next.Offers = previous.Offers
			next.SeenThrough = previous.SeenThrough
		}
		old := map[string]*localSession{}
		for k, v := range b.sessions {
			if k == key || v.Attachment.AgentID == a.AgentID {
				old[k] = v
				delete(b.sessions, k)
			}
		}
		b.sessions[key] = next
		if err = b.save(); err != nil {
			delete(b.sessions, key)
			for k, v := range old {
				b.sessions[k] = v
			}
			return LocalBridgeReply{Error: err.Error()}
		}
		for _, v := range old {
			if v.ConfigPath != next.ConfigPath {
				_ = os.Remove(v.ConfigPath)
			}
			if v.cancel != nil {
				v.cancel()
			}
		}
		b.start(serverCtx, next)
		b.wake()
		return LocalBridgeReply{Attached: true, Attachment: a}
	}
	if s == nil {
		return LocalBridgeReply{}
	}
	if !s.Offline && (in.HostKey == "" || s.HostKey == "" || in.HostKey != s.HostKey) {
		return LocalBridgeReply{}
	}
	if in.AttachmentID != "" && in.AttachmentID != s.Attachment.ID {
		return LocalBridgeReply{}
	}
	if s.client == nil {
		return LocalBridgeReply{Error: "attached connection is unavailable; sign in and explicitly reattach"}
	}
	out := LocalBridgeReply{Attached: !s.Offline, Offline: s.Offline, Attachment: s.Attachment, LastError: s.LastError}
	if s.Offline && in.Action != "status" && in.Action != "detach" && in.Action != "cleanup" && in.Action != "offline" {
		return out
	}
	for _, v := range s.Offers {
		if !v.Offered && !v.Suppressed {
			out.Pending++
		}
	}
	if in.Action == "reply" && in.MessageID == "" && in.InboxID != "" {
		in.Action = "reply_inbox"
	}
	switch in.Action {
	case "peers", "send", "read", "reply_inbox":
		// The plugin exposes only messaging for its attached identity. A full
		// AgentWorks tool connection must be installed separately by the person.
		args := map[string]any{"agent_name": s.Attachment.AgentName}
		switch in.Action {
		case "peers":
			args["action"] = "list_agents"
		case "send":
			args["action"], args["target"], args["message"], args["submission_id"] = "send", in.Target, in.Message, in.SubmissionID
		case "reply_inbox":
			history, e := s.client.Call(callCtx, "messages", map[string]any{"action": "read", "agent_name": s.Attachment.AgentName, "inbox_id": in.InboxID, "after": 0, "limit": 20})
			if e != nil {
				return LocalBridgeReply{Error: e.Error()}
			}
			filtered, e := filterAttachedMailbox(callCtx, s, history)
			if e != nil {
				return LocalBridgeReply{Error: e.Error()}
			}
			var checked struct {
				Messages []json.RawMessage `json:"messages"`
			}
			if json.Unmarshal(filtered, &checked) != nil || len(checked.Messages) == 0 {
				return LocalBridgeReply{Error: "inbox has no currently approved sender for this attachment"}
			}
			// Explicit mailbox recovery survives local metadata compaction.
			// The server still verifies this caller's inbox participation.
			args["action"], args["inbox_id"], args["message"], args["submission_id"] = "send", in.InboxID, in.Message, in.SubmissionID
		case "read":
			args["action"], args["after"], args["limit"] = "read", in.Cursor, 20
			if in.InboxID != "" {
				args["inbox_id"] = in.InboxID
			}
		}
		// Recheck the receiver lease even for directory/read operations.
		if _, err := s.client.request(callCtx, http.MethodPost, "/api/external/v1/agents/attachment", map[string]any{"action": "validate", "agent_name": s.Attachment.AgentName, "attachment_id": s.Attachment.ID, "message_ids": []string{}}, true); err != nil {
			return LocalBridgeReply{Error: "attachment no longer authorized"}
		}
		raw, err := s.client.Call(callCtx, "messages", args)
		if err != nil {
			return LocalBridgeReply{Error: err.Error()}
		}
		if in.Action == "read" {
			raw, err = filterAttachedMailbox(callCtx, s, raw)
			if err != nil {
				return LocalBridgeReply{Error: err.Error()}
			}
		}
		out.Result = raw
		return out
	case "status":
		return out
	case "detach", "offline":
		if s.Offline && in.Action == "offline" {
			return out
		}
		var err error
		if !s.Offline {
			_, err = s.client.request(callCtx, http.MethodPost, "/api/external/v1/agents/attachment", map[string]any{"action": "detach", "agent_name": s.Attachment.AgentName, "attachment_id": s.Attachment.ID}, true)
		}
		if err != nil {
			var apiErr *APIError
			if !errors.As(err, &apiErr) || (apiErr.Status != 401 && apiErr.Status != 403 && apiErr.Status != 404 && apiErr.Status != 409) {
				return LocalBridgeReply{Error: err.Error()}
			}
		}
		if in.Action == "offline" {
			s.Offline = true
			s.HostKey = ""
		} else {
			delete(b.sessions, key)
		}
		if err = b.save(); err != nil {
			b.sessions[key] = s
			return LocalBridgeReply{Error: err.Error()}
		}
		if s.cancel != nil {
			s.cancel()
		}
		if in.Action == "detach" && s.ParentConfigPath != "" {
			if cfg, e := LoadConfig(s.ConfigPath); e == nil {
				_ = s.client.RevokeOAuth(callCtx, cfg.RefreshToken)
			}
			_ = os.Remove(s.ConfigPath)
		}
		b.wake()
		return LocalBridgeReply{}
	case "cleanup":
		// Retain pending bodies and the sequence watermark. Retire offered
		// metadata without allowing a fresh automatic turn after a crash.
		compactLocalOffers(s, true)
		if err := b.save(); err != nil {
			return LocalBridgeReply{Error: err.Error()}
		}
		return out
	case "drain", "next":
		if in.Action == "drain" && (s.NativeRemote != "" || s.nativeCalls > 0) {
			return out
		}
		providerForTransport := map[string]string{"claude_session": "claude", "claude_channel": "claude", "claude_hook": "claude", "codex_queue": "codex", "codex_desktop": "codex", "codex_hook": "codex", "cursor_hook": "cursor", "muse_session": "muse", "muse_hook": "muse", "agy_hook": "agy", "pi_hook": "pi", "pi_extension": "pi"}
		if providerForTransport[in.Transport] != s.Attachment.Provider {
			return LocalBridgeReply{Error: "unsupported delivery adapter for this provider"}
		}

		if in.Action == "next" {
			if in.Transport == "claude_channel" {
				s.nativeCalls++
				defer func() { s.nativeCalls-- }()
			}
			// A bounded local wait, never an inbox poll. Adapter reconnects to
			// this Unix socket; all server reads remain persistent SSE.
			wait := time.NewTimer(25 * time.Second)
			defer wait.Stop()
			for !localPending(s) && b.sessions[key] == s && !b.closed {
				changed := b.changed
				b.mu.Unlock()
				select {
				case <-callCtx.Done():
					b.mu.Lock()
					return LocalBridgeReply{Error: "local listener cancelled"}
				case <-wait.C:
					b.mu.Lock()
					return out
				case <-changed:
					b.mu.Lock()
				}
			}
			if b.sessions[key] != s || b.closed {
				return LocalBridgeReply{}
			}
		}
		for _, m := range s.Offers {
			if !m.Offered && !m.Suppressed {
				out.Messages = append(out.Messages, m.AgentLiveMessage)
			}
		}
		sort.Slice(out.Messages, func(i, j int) bool { return out.Messages[i].Sequence < out.Messages[j].Sequence })
		if len(out.Messages) > 20 {
			out.Messages = out.Messages[:20]
		}
		if len(out.Messages) == 0 {
			return out
		}
		ids := []string{}
		for _, m := range out.Messages {
			ids = append(ids, m.MessageID)
		}
		raw, err := s.client.request(callCtx, http.MethodPost, "/api/external/v1/agents/attachment", map[string]any{"action": "validate", "agent_name": s.Attachment.AgentName, "attachment_id": s.Attachment.ID, "message_ids": ids}, true)
		if err != nil {
			return LocalBridgeReply{Error: "attachment or messages no longer authorized"}
		}
		var checked struct {
			IDs []string `json:"message_ids"`
		}
		if json.Unmarshal(raw, &checked) != nil {
			return LocalBridgeReply{Error: "invalid pending validation"}
		}
		allowed := map[string]bool{}
		for _, id := range checked.IDs {
			allowed[id] = true
		}
		previous := map[string]localOffer{}
		filtered := []AgentLiveMessage{}
		for _, m := range out.Messages {
			previous[m.MessageID] = s.Offers[m.MessageID]
			if allowed[m.MessageID] {
				filtered = append(filtered, m)
			} else {
				v := s.Offers[m.MessageID]
				v.Suppressed = true
				v.Message = ""
				s.Offers[m.MessageID] = v
			}
		}
		out.Messages = filtered
		for _, m := range out.Messages {
			v := s.Offers[m.MessageID]
			v.Offered = true
			v.OfferedAt = time.Now()
			v.Message = ""
			v.Transport = in.Transport
			s.Offers[m.MessageID] = v
		}
		if err := b.save(); err != nil {
			for id, old := range previous {
				s.Offers[id] = old
			}

			return LocalBridgeReply{Error: err.Error()}
		}
		return out
	case "ack", "reply":
		m, ok := s.Offers[in.MessageID]
		if !ok || !m.Offered {
			return LocalBridgeReply{Error: "message was not offered to this exact session"}
		}
		args := map[string]any{"agent_name": s.Attachment.AgentName}
		if in.Action == "ack" {
			if in.Delivery != "admitted" && in.Delivery != "processed" {
				return LocalBridgeReply{Error: "ack requires admitted or processed"}
			}
			args["action"], args["attachment_id"], args["message_id"], args["delivery"], args["transport"] = "ack", s.Attachment.ID, in.MessageID, in.Delivery, m.Transport
		} else {
			if _, err := s.client.request(callCtx, http.MethodPost, "/api/external/v1/agents/attachment", map[string]any{"action": "validate", "agent_name": s.Attachment.AgentName, "attachment_id": s.Attachment.ID, "message_ids": []string{}}, true); err != nil {
				return LocalBridgeReply{Error: "attachment no longer authorized"}
			}
			args["action"], args["inbox_id"], args["message"], args["submission_id"] = "send", m.InboxID, in.Message, in.SubmissionID
		}
		raw, err := s.client.Call(callCtx, "messages", args)
		if err != nil {
			return LocalBridgeReply{Error: err.Error()}
		}
		if in.Action == "ack" {
			m.Message = ""
			m.Delivery = in.Delivery
			s.Offers[in.MessageID] = m
			if err = b.save(); err != nil {
				return LocalBridgeReply{Error: err.Error()}
			}
		}
		out.Result = raw
		return out
	default:
		return LocalBridgeReply{Error: "unknown local bridge action"}
	}
}

func localPending(s *localSession) bool {
	for _, m := range s.Offers {
		if !m.Offered && !m.Suppressed {
			return true
		}
	}
	return false
}

func (b *localBridge) nativeLoop(ctx context.Context, s *localSession, hostKey string) {
	for ctx.Err() == nil {
		out := b.handle(ctx, ctx, LocalBridgeRequest{Action: "next", Provider: s.Attachment.Provider, SessionID: s.Attachment.SessionID, AttachmentID: s.Attachment.ID, HostKey: hostKey, Transport: nativeCodexTransport(s.NativeRemote)})
		if !out.Attached || out.Error != "" {
			return
		}
		if len(out.Messages) == 0 {
			continue
		}
		if err := b.options.NativeOffer(ctx, s.NativeRemote, s.Attachment, out.Messages); err != nil {
			if errors.Is(err, ErrNativeOfferUnconfirmed) {
				continue
			}
			b.mu.Lock()
			s.LastError = "Native queue did not confirm acceptance; use explicit mailbox read."
			_ = b.save()
			b.mu.Unlock()
			if b.options.Notify != nil {
				b.options.Notify(s.Attachment)
			}
			continue // Persisted offer prevents a duplicate turn on an ambiguous failure.
		}
		for _, m := range out.Messages {
			_ = b.handle(ctx, ctx, LocalBridgeRequest{Action: "ack", Provider: s.Attachment.Provider, SessionID: s.Attachment.SessionID, AttachmentID: s.Attachment.ID, HostKey: hostKey, MessageID: m.MessageID, Delivery: "admitted"})
		}
	}
}

// The server emits monotonically increasing account message sequences. Persist
// the received watermark with pending bodies before offering anything. Retiring
// offered metadata never permits an ambiguous delivery to become a new turn.
func compactLocalOffers(s *localSession, all bool) {

	for id, v := range s.Offers {
		if (v.Offered || v.Suppressed) && (all || len(s.Offers) >= 1000 && v.Delivery == "processed" || !v.OfferedAt.IsZero() && time.Since(v.OfferedAt) > 7*24*time.Hour) {
			delete(s.Offers, id)
		}
	}
}

func nativeCodexTransport(remote string) string {
	if strings.HasPrefix(remote, "claude+session://") {
		return "claude_session"
	}
	if strings.HasPrefix(remote, "desktop+unix://") {
		return "codex_desktop"
	}
	return "codex_queue"
}

// Manual recovery obeys the same current sender and peer authorization as push,
// including historical messages, without recording any delivery receipt.
func filterAttachedMailbox(ctx context.Context, s *localSession, raw json.RawMessage) (json.RawMessage, error) {
	var result map[string]json.RawMessage
	if json.Unmarshal(raw, &result) != nil {
		return nil, errors.New("invalid mailbox response")
	}
	var messages []json.RawMessage
	if json.Unmarshal(result["messages"], &messages) != nil || len(messages) > 20 {
		return nil, errors.New("invalid bounded mailbox response")
	}
	ids := []string{}
	for _, message := range messages {
		var value struct {
			ID string `json:"message_id"`
		}
		if json.Unmarshal(message, &value) != nil || value.ID == "" {
			return nil, errors.New("invalid mailbox message")
		}
		ids = append(ids, value.ID)
	}
	validated, err := s.client.request(ctx, http.MethodPost, "/api/external/v1/agents/attachment", map[string]any{"action": "validate", "agent_name": s.Attachment.AgentName, "attachment_id": s.Attachment.ID, "message_ids": ids, "history": true}, true)
	if err != nil {
		return nil, errors.New("mailbox attachment or sender no longer authorized")
	}
	var checked struct {
		IDs []string `json:"message_ids"`
	}
	if json.Unmarshal(validated, &checked) != nil {
		return nil, errors.New("invalid mailbox authorization response")
	}
	allowed := map[string]bool{}
	for _, id := range checked.IDs {
		allowed[id] = true
	}
	filtered := []json.RawMessage{}
	for i, message := range messages {
		if allowed[ids[i]] {
			filtered = append(filtered, message)
		}
	}
	result["messages"], err = json.Marshal(filtered)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
