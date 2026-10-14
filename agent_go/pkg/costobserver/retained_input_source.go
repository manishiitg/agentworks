package costobserver

import (
	"strings"
	"sync"
)

// A turn typed into a running coding-agent terminal is paid by the chat's owner, but it may have been sent by another
// person's agent. The server records who sent the latest live input of a chat here, and the usage rows read from the
// CLI's own transcript (usage_source=retained) carry it. The latest input wins: a person typing clears it.
var retainedInputSources = struct {
	sync.Mutex
	m map[string][2]string
}{m: map[string][2]string{}}

const maxRetainedInputSources = 4096

// SetRetainedInputSource records what triggered the latest live input of a chat (empty for a person typing).
func SetRetainedInputSource(sessionID, triggeredBy, label string) {
	sessionID, triggeredBy, label = strings.TrimSpace(sessionID), strings.TrimSpace(triggeredBy), strings.TrimSpace(label)
	if sessionID == "" {
		return
	}
	retainedInputSources.Lock()
	defer retainedInputSources.Unlock()
	if triggeredBy == "" {
		delete(retainedInputSources.m, sessionID)
		return
	}
	if len(retainedInputSources.m) >= maxRetainedInputSources {
		retainedInputSources.m = map[string][2]string{} // a cache of the latest input; losing it only drops a label
	}
	retainedInputSources.m[sessionID] = [2]string{triggeredBy, label}
}

func retainedInputSource(sessionID string) (triggeredBy, label string) {
	retainedInputSources.Lock()
	defer retainedInputSources.Unlock()
	v := retainedInputSources.m[strings.TrimSpace(sessionID)]
	return v[0], v[1]
}
