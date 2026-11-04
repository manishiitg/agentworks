package server

import (
	"context"
	"log"
	"path"
)

// crewSharedChatsDir holds copies of other users' conversations with a Crew
// (Crew Run mode). The reader's own copy stays in their central chat store;
// this copy lets the Crew itself read every conversation held with it. It is
// outside builder/conversation so the owner's Recent list is unaffected, and
// it sits under builder/, which other Crews are denied.
const crewSharedChatsDir = "builder/crew-chats/users"

// crewReaderConversationMirrorPath returns where a reader's conversation with
// someone else's Crew is mirrored inside that Crew (a chat folder header), or
// false when crewFolder is not a physical Crew path.
func crewReaderConversationMirrorPath(readerID, crewFolder, sessionID string) (string, bool) {
	crewRoot := canonicalCrewWorkspaceRoot(crewFolder)
	if _, ok := crewProjectOwnerID(crewRoot); !ok || !isCrewProjectPath(crewRoot) {
		return "", false
	}
	reader := sanitizeUserIDForPath(readerID)
	session := sanitizeChatHistorySessionID(sessionID)
	if reader == "" || session == "" {
		return "", false
	}
	return chatHistoryHeaderPath(path.Join(crewRoot, crewSharedChatsDir, reader), session), true
}

// mirrorCrewReaderTurn appends the turn just saved in the reader's own chat to
// the Crew's copy. Best effort: the reader's own chat remains the source.
func mirrorCrewReaderTurn(readerID, crewFolder, sessionID string, saved chatTurnAppend) {
	target, ok := crewReaderConversationMirrorPath(readerID, crewFolder, sessionID)
	if !ok {
		return
	}
	if _, err := appendChatTurnInWorkspace(context.Background(), target, saved); err != nil {
		log.Printf("[CHAT_HISTORY] Crew chat mirror: append %s: %v", target, err)
	}
}
