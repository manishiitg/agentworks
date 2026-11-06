package server

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

// The files a person attached to a message travel to the agent as a "Files in context" line at the end of the request. The
// server records them once, as metadata.attachments (path, name, kind image/file) on the stored user message, so the chat
// shows the same thumbnails and name chips live and after a reload without reading the request text (PLAT-856-132).
const filesInContextMarker = "\n\n📁 Files in context:"

var imageAttachment = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|webp|bmp|avif)$`)

func userMessageAttachments(content string) []map[string]string {
	seen := map[string]bool{}
	var out []map[string]string
	// Messages joined into one turn each keep their own line.
	for _, section := range strings.Split(content, filesInContextMarker)[1:] {
		line, _, _ := strings.Cut(section, "\n")
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
	}
	return out
}

// crewAttachedFilesNotice tells a turn where this message's files really are. The "Files in context" line keeps the
// Crew's virtual paths, which are the ids the API, the stored message and the workspace tools use, but a native CLI reads
// the disk, and a Crew in a folder the person chose keeps its files outside the workspace, so a virtual path joined to the
// workspace finds nothing. The line is typed text, so only existing files of this turn's own Crew are named; this gives
// the path, never access. The block is the session notice: it reaches the CLI and is stripped from what people see.
func crewAttachedFilesNotice(crewFolder, query string) string {
	attachments := userMessageAttachments(query)
	if len(attachments) == 0 {
		return ""
	}
	roots, bound, err := crewStorageRoots(crewFolder)
	if err != nil || !bound {
		return ""
	}
	var lines []string
	for _, attachment := range attachments {
		virtual := attachment["path"]
		if !strings.HasPrefix(virtual, roots.Virtual+"/") {
			continue
		}
		real, err := fsutil.WorkspacePath(fsutil.WorkspaceDocsRoot(), virtual)
		if err != nil {
			continue
		}
		if _, err := os.Stat(real); err != nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %q is at %q", virtual, real))
	}
	if len(lines) == 0 {
		return ""
	}
	return sessionModeOpen + "\nWhere the files in this message are on this computer. Native file tools and terminal commands use these full paths; workspace tools keep the Crew paths.\n" +
		strings.Join(lines, "\n") + "\n" + sessionModeClose
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
