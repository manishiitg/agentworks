package server

import (
	"context"
	"log"
	"path"
	"strings"
	"time"
)

// crewSharedChatsDir holds copies of other users' conversations with a Crew
// (Crew Run mode). The reader's own copy stays in their central chat store;
// this copy lets the Crew itself read every conversation held with it. It is
// outside builder/conversation so the owner's Recent list is unaffected, and
// it sits under builder/, which other Crews are denied.
const crewSharedChatsDir = "builder/crew-chats/users"

// crewReaderConversationMirrorPath returns where a reader's conversation with
// someone else's Crew is mirrored inside that Crew, or false when crewFolder
// is not a physical Crew path.
func crewReaderConversationMirrorPath(readerID, crewFolder, sessionID string, t time.Time) (string, bool) {
	crewRoot := canonicalCrewWorkspaceRoot(crewFolder)
	if _, ok := crewProjectOwnerID(crewRoot); !ok || !isCrewProjectPath(crewRoot) {
		return "", false
	}
	reader := sanitizeUserIDForPath(readerID)
	session := sanitizeChatHistorySessionID(sessionID)
	if reader == "" || session == "" {
		return "", false
	}
	// One file per session (overwritten each turn): the source is the
	// reader's full transcript, so dated copies would only duplicate it.
	_ = t
	return path.Join(crewRoot, crewSharedChatsDir, reader, chatHistoryConversationFileName(session)), true
}

// mirrorCrewReaderConversation copies a reader's just-saved conversation
// into the Crew it was held with. Best effort: the reader's own transcript
// remains the source of truth.
func mirrorCrewReaderConversation(readerID, crewFolder, sessionID, sourcePath string, t time.Time) {
	target, ok := crewReaderConversationMirrorPath(readerID, crewFolder, sessionID, t)
	sourcePath = strings.Trim(strings.TrimSpace(sourcePath), "/")
	if !ok || sourcePath == "" || sourcePath == target {
		return
	}
	content, exists, err := readFileFromWorkspace(context.Background(), sourcePath)
	if err != nil || !exists {
		if err != nil {
			log.Printf("[CHAT_HISTORY] Crew chat mirror: read %s: %v", sourcePath, err)
		}
		return
	}
	if err := writeRawFileToWorkspace(context.Background(), target, content); err != nil {
		log.Printf("[CHAT_HISTORY] Crew chat mirror: write %s: %v", target, err)
	}
}
