package server

import (
	"net/url"

	"runtime"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/chatlog"
)

// Only the filename changes on Windows. Registries and archive handoffs keep
// the logical session ID, and Unix retains its existing filenames (PLAT-874).
func chatHistoryConversationFileName(sessionID string) string {
	id := sanitizeChatHistorySessionID(sessionID)
	if runtime.GOOS == "windows" {
		id = windowsChatHistoryFilePart(id)
	}
	// The fixed prefix/suffix also prevent reserved device basenames and
	// trailing dot/space filenames, even for IDs such as CON or NUL.
	return "session-" + id + "-conversation.json"
}

func windowsChatHistoryFilePart(id string) string {
	return strings.NewReplacer(".", "%2E", "+", "%20").Replace(url.QueryEscape(id))
}

func chatHistorySessionIDFromFileName(fileName string) string {
	if !strings.HasPrefix(fileName, "session-") || !strings.HasSuffix(fileName, "-conversation.json") {
		return ""
	}
	id := strings.TrimSuffix(strings.TrimPrefix(fileName, "session-"), "-conversation.json")
	if runtime.GOOS == "windows" {
		decoded, err := url.QueryUnescape(id)
		if err != nil || windowsChatHistoryFilePart(decoded) != id || sanitizeChatHistorySessionID(decoded) != decoded {
			return ""
		}
		id = decoded
	}
	return sanitizeChatHistorySessionID(id)
}

// chatHistorySessionIDFromConversationFile returns the session of a stored
// chat: a chat folder's header.json (session-<id>/header.json) or, for
// Goals builder chats, a session-<id>-conversation.json file.
func chatHistorySessionIDFromConversationFile(path string) string {
	if isChatHeaderPath(path) || strings.HasSuffix(filepathToSlash(path), "/"+chatlog.HeaderFile) {
		return chatHistorySessionIDFromHeaderPath(path)
	}
	slashed := filepathToSlash(path)
	if i := strings.LastIndex(slashed, "/"); i >= 0 {
		slashed = slashed[i+1:]
	}
	return chatHistorySessionIDFromFileName(slashed)
}
