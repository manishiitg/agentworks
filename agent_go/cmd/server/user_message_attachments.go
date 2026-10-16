package server

import (
	"path"
	"regexp"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

// The files a person attached to a message travel to the agent as a "Files in context" line at the end of the request. The
// server records them once, as metadata.attachments (path, name, kind image/file) on the stored user message, so the chat
// shows the same thumbnails and name chips live and after a reload without reading the request text (PLAT-856-132).
const filesInContextMarker = "\n\n📁 Files in context:"

var imageAttachment = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|webp|bmp|avif)$`)

func userMessageAttachments(content string) []map[string]string {
	at := strings.Index(content, filesInContextMarker)
	if at < 0 {
		return nil
	}
	line, _, _ := strings.Cut(content[at+len(filesInContextMarker):], "\n")
	seen := map[string]bool{}
	var out []map[string]string
	for _, part := range strings.Split(line, ",") {
		p := strings.TrimSpace(part)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		kind := "file"
		if imageAttachment.MatchString(p) {
			kind = "image"
		}
		out = append(out, map[string]string{"path": p, "name": path.Base(p), "kind": kind})
	}
	return out
}

// prepareUserMessageAttachments is the event store's preparer: it writes metadata.attachments on a user message that
// carries a "Files in context" line, and leaves every other event alone.
func prepareUserMessageAttachments(event *events.Event) {
	if event == nil || event.Type != string(unifiedevents.UserMessage) || event.Data == nil {
		return
	}
	message, ok := event.Data.Data.(*unifiedevents.UserMessageEvent)
	if !ok || message == nil {
		return
	}
	attachments := userMessageAttachments(message.Content)
	if len(attachments) == 0 {
		return
	}
	if message.Metadata == nil {
		message.Metadata = map[string]interface{}{}
	}
	message.Metadata["attachments"] = attachments
}
