package server

import (
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// chatStreamLog writes one chat event stream's life (open, resume, keepalive miss, close) with the person and a
// coarse device class, so "my phone lost the chat" can be traced with one grep on the user (PLAT-856-509).
// Only IDs, counts and reason codes are logged; never message text, and no address of the device.
type chatStreamLog struct {
	user, device, project, conversation string
	opened                              time.Time
	sent                                int
	reason                              string
}

func newChatStreamLog(r *http.Request, project, conversation string, lastEvent int) *chatStreamLog {
	l := &chatStreamLog{user: logUsernameForUserID(GetUserIDFromContext(r.Context())), device: clientDeviceClass(r.UserAgent()), project: project, conversation: conversation, opened: time.Now(), reason: "client_gone"}
	log.Printf("[CHAT_STREAM] open username=%s device=%s project=%s conversation=%s last_event_id=%d", l.user, l.device, project, conversation, lastEvent)
	return l
}

func (l *chatStreamLog) resume(from, to, replayed int, snapshot bool, source string) {
	log.Printf("[CHAT_STREAM] resume username=%s device=%s project=%s conversation=%s from=%d to=%d replayed=%d snapshot=%s source=%s", l.user, l.device, l.project, l.conversation, from, to, replayed, map[bool]string{true: "yes", false: "no"}[snapshot], source)
}

func (l *chatStreamLog) keepaliveMiss() {
	l.reason = "write_error"
	log.Printf("[CHAT_STREAM] keepalive_miss username=%s device=%s project=%s conversation=%s after=%s", l.user, l.device, l.project, l.conversation, time.Since(l.opened).Round(time.Second))
}

// close records why the stream ended: the client went away, the server is shutting down, or a write failed.
func (l *chatStreamLog) close() {
	if l.reason == "client_gone" && serverShuttingDown.Load() {
		l.reason = "server_shutdown"
	}
	log.Printf("[CHAT_STREAM] close username=%s device=%s project=%s conversation=%s after=%s reason=%s sent=%d", l.user, l.device, l.project, l.conversation, time.Since(l.opened).Round(time.Second), l.reason, l.sent)
}

// serverShuttingDown is set when the server starts stopping, so streams it ends say so.
var serverShuttingDown atomic.Bool

// clientDeviceClass is a coarse "phone|tablet|desktop/browser" from the User-Agent only.
func clientDeviceClass(ua string) string {
	lower := strings.ToLower(ua)
	kind := "desktop"
	switch {
	case strings.Contains(lower, "ipad") || strings.Contains(lower, "tablet") || strings.Contains(lower, "android") && !strings.Contains(lower, "mobile"):
		kind = "tablet"
	case strings.Contains(lower, "iphone") || strings.Contains(lower, "android") || strings.Contains(lower, "mobile"):
		kind = "phone"
	}
	browser := "other"
	switch {
	case strings.Contains(lower, "electron") || strings.Contains(lower, "agentworks"):
		browser = "app"
	case strings.Contains(lower, "edg/"):
		browser = "edge"
	case strings.Contains(lower, "firefox") || strings.Contains(lower, "fxios"):
		browser = "firefox"
	case strings.Contains(lower, "chrome") || strings.Contains(lower, "crios"):
		browser = "chrome"
	case strings.Contains(lower, "safari"):
		browser = "safari"
	}
	return kind + "/" + browser
}
