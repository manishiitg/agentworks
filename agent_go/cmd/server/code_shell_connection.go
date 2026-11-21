package server

import (
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Terminal telemetry never includes a URL, command, output or peer-supplied
// close text (which can contain credentials). Reasons below are fixed labels.
type codeShellConnection struct {
	mu                                        sync.Mutex
	opened, active                            time.Time
	id, shell, project, user, client, version string
	code                                      int
	reason                                    string
}

func newCodeShellConnection(r *http.Request, shell, project, user string) *codeShellConnection {
	now := time.Now()
	client := r.URL.Query().Get("client_kind")
	switch client {
	case "mac_app", "desktop_app", "browser":
	default:
		client = "unknown"
	}
	version := r.URL.Query().Get("client_version")
	if len(version) == 0 || len(version) > 64 || strings.IndexFunc(version, func(c rune) bool {
		return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._+-", c))
	}) >= 0 {
		version = "unknown"
	}
	c := &codeShellConnection{opened: now, active: now, id: now.UTC().Format("20060102T150405.000000000"), shell: shell, project: project, user: user, client: client, version: version}
	log.Printf("[PROJECT_TERMINAL] event=open connection=%s project=%s shell=%s user=%s client=%s app_version=%s", c.id, project, shell, user, client, version)
	return c
}

func (c *codeShellConnection) activity() {
	c.mu.Lock()
	c.active = time.Now()
	c.mu.Unlock()
}

func (c *codeShellConnection) ended(err error, reason string, code int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reason != "" {
		return
	}
	var closed *websocket.CloseError
	var timeout net.Error
	if errors.As(err, &closed) {
		code = closed.Code
		// A close frame's text is untrusted; retain its code, not its text.
		if reason == "client_read" {
			reason = "peer_close"
		}
	} else if errors.As(err, &timeout) && timeout.Timeout() {
		reason = "timeout"
	}
	c.code, c.reason = code, reason
}

func (c *codeShellConnection) closed() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reason == "" {
		c.reason, c.code = "transport_closed", websocket.CloseAbnormalClosure
	}
	log.Printf("[PROJECT_TERMINAL] event=close connection=%s project=%s shell=%s user=%s client=%s app_version=%s close_code=%d reason=%s idle_seconds=%.3f duration_seconds=%.3f", c.id, c.project, c.shell, c.user, c.client, c.version, c.code, c.reason, time.Since(c.active).Seconds(), time.Since(c.opened).Seconds())
}
