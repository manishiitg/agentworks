package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

type claudeSessionRecord struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Socket    string `json:"messagingSocketPath"`
}

// The person explicitly selects a registration from Claude's session-messaging
// feature. Read only that exact registration, never select by name or recency.
func nativeClaudeSession(remote, session string) (claudeSessionRecord, string, error) {
	var record claudeSessionRecord
	u, err := url.Parse(remote)
	if err != nil || u.Scheme != "claude+session" || u.Host != "" || !filepath.IsAbs(u.Path) || u.RawQuery != "" || u.Fragment != "" {
		return record, "", errors.New("Claude session messaging needs an exact absolute registration file")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return record, "", err
	}
	expected := filepath.Join(home, ".claude", "sessions")
	if filepath.Dir(u.Path) != expected {
		return record, "", errors.New("registration must belong to the local Claude sessions directory")
	}
	info, err := os.Lstat(u.Path)
	if err != nil {
		return record, "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > 64<<10 {
		return record, "", errors.New("unsafe Claude session registration")
	}
	b, err := os.ReadFile(u.Path)
	if err != nil {
		return record, "", err
	}
	if json.Unmarshal(b, &record) != nil || record.SessionID != session || record.PID < 2 || filepath.Base(u.Path) != strconv.Itoa(record.PID)+".json" || !filepath.IsAbs(record.Socket) {
		return record, "", errors.New("registration does not match the exact Claude session")
	}
	socket, err := os.Lstat(record.Socket)
	if err != nil {
		return record, "", err
	}
	if socket.Mode()&os.ModeSocket == 0 || socket.Mode().Perm()&0077 != 0 {
		return record, "", errors.New("Claude messaging socket must be private")
	}
	h := sha256.Sum256([]byte(record.Socket))
	keyPath := filepath.Join(expected, strconv.Itoa(record.PID)+"."+hex.EncodeToString(h[:])+".key")
	info, err = os.Lstat(keyPath)
	if err != nil {
		return record, "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return record, "", errors.New("Claude messaging key must be private")
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return record, "", err
	}
	if strings.TrimSpace(string(key)) == "" {
		return record, "", errors.New("empty Claude messaging key")
	}
	return record, strings.TrimSpace(string(key)), nil
}

func claudeSessionOffer(ctx context.Context, remote string, out agentworksclient.LocalBridgeReply) (offerErr error) {
	mayHaveDelivered := false
	defer func() {
		if offerErr != nil && !mayHaveDelivered {
			offerErr = errors.Join(agentworksclient.ErrNativeOfferRejected, offerErr)
		}
	}()
	a := out.Attachment
	record, key, err := nativeClaudeSession(remote, a.SessionID)
	if err != nil {
		return err
	}
	text := pluginMessageText(out, "claude_session")
	if len(text) > 96<<10 {
		return errors.New("native message exceeds 96 KiB; explicitly read the inbox to recover, no automatic retry")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", record.Socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	encoder := json.NewEncoder(conn)
	if err = encoder.Encode(map[string]any{"type": "auth", "token": key}); err != nil {
		return err
	}
	mayHaveDelivered = true
	if err = encoder.Encode(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}}); err != nil {
		return err
	}
	// This version of Claude does not return an admission receipt. A successful
	// socket write remains an offer; the receiving agent explicitly acknowledges.
	return agentworksclient.ErrNativeOfferUnconfirmed
}

// MCP startup needs only its own process registration, not whole-app discovery.
func claudeSessionIDForHost(host string) string {
	home, err := os.UserHomeDir()
	if err != nil || host == "" {
		return ""
	}
	files, _ := filepath.Glob(filepath.Join(home, ".claude", "sessions", "*.json"))
	found := ""
	for _, file := range files {
		raw, err := os.ReadFile(file)
		var rec claudeSessionRecord
		if err != nil || len(raw) > 64<<10 || json.Unmarshal(raw, &rec) != nil {
			continue
		}
		if _, _, err = nativeClaudeSession("claude+session://"+file, rec.SessionID); err != nil {
			continue
		}
		if key, err := providerPIDHostKey("claude", rec.PID); err == nil && key == host {
			if found != "" && found != rec.SessionID {
				return ""
			}
			found = rec.SessionID
		}
	}
	return found
}
