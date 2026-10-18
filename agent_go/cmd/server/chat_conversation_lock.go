package server

import (
	"path/filepath"
	"sync"
)

// All transcript writers share a lock across their entire read/modify/write.
// Bounded stripes avoid retaining one mutex forever for every historical chat.
var chatConversationLocks [256]sync.Mutex

func chatConversationMutex(path string) *sync.Mutex {
	path = chatHistoryStoragePath(path)
	var hash uint64 = 1469598103934665603
	for _, b := range []byte(filepath.ToSlash(filepath.Clean(path))) {
		hash ^= uint64(b)
		hash *= 1099511628211
	}
	return &chatConversationLocks[hash%uint64(len(chatConversationLocks))]
}

// Revisions advance only while holding the conversation writer lock.
func advanceChatConversationRevision(record map[string]interface{}) {
	var revision uint64
	switch value := record["revision"].(type) {
	case float64:
		if value > 0 {
			revision = uint64(value)
		}
	case uint64:
		revision = value
	case int:
		if value > 0 {
			revision = uint64(value)
		}
	}
	record["revision"] = revision + 1
}
