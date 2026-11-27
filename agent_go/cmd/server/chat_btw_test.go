package server

import (
	"strings"
	"testing"
)

// /btw: the side question's copy is hidden from the tab strip and agent lists, is not a user-view (Run mode) chat,
// is named after its question and is told to answer without changing anything.
func TestBtwSideQuestionCopyIsHiddenAndReadOnly(t *testing.T) {
	key := "proj" + codeChatSideMarker + btwChatPrefix + "1234"
	if !isBtwConversationKey(key) || !hiddenFromTabStrip(key) {
		t.Fatalf("a side-question copy must be hidden from the tab strip: %q", key)
	}
	if isUserViewConversationKey(key) {
		t.Fatal("a side-question copy is the owner's own chat, not a user-view chat")
	}
	if hiddenFromTabStrip("proj"+codeChatSideMarker+"fork-1234") || isBtwConversationKey("proj"+codeChatSideMarker+"fork-1234") {
		t.Fatal("an ordinary fork stays visible")
	}
	name := btwChatName("  is the   Batch B retry change safe if the gateway restarts mid-call and the bridge is reconnecting?  ")
	if !strings.HasPrefix(name, "btw: is the Batch B retry") || !strings.HasSuffix(name, "…") || len([]rune(name)) > btwNameMaxRunes+6 {
		t.Fatalf("name = %q", name)
	}
	note := btwFirstTurnNote("MCP Review")
	for _, want := range []string{"Side question", "does not see this question", "do not edit files"} {
		if !strings.Contains(note, want) {
			t.Fatalf("note misses %q: %s", want, note)
		}
	}
}
