package server

import "testing"

// Only a side chat (tab) can be closed; the main chat never is.
func TestIsSideChatConversationKey(t *testing.T) {
	for key, want := range map[string]bool{
		"proj1:chat:abc": true,
		"proj1":          false,
		"proj1:chat:":    false,
		":chat:abc":      false,
		"proj1:chat:a:b": false,
		"a:b:chat:c":     false,
		"":               false,
	} {
		if got := isSideChatConversationKey(key); got != want {
			t.Errorf("isSideChatConversationKey(%q) = %v, want %v", key, got, want)
		}
	}
}
