package mcpagent

import (
	"github.com/manishiitg/multi-llm-provider-go/pkg/nativereplies"
	"sync"
)

var nativeRuntimeObserver struct {
	sync.RWMutex
	notify func(*AgentSessionHandle)
}

// SetNativeRuntimeObserver lets the host observe owned runtime creation and
// restoration directly, independently of streaming events or browser views.
func SetNativeRuntimeObserver(notify func(*AgentSessionHandle)) {
	nativeRuntimeObserver.Lock()
	nativeRuntimeObserver.notify = notify
	nativeRuntimeObserver.Unlock()
}

func notifyNativeRuntime(agent *Agent) {
	if agent == nil {
		return
	}
	nativeRuntimeObserver.RLock()
	notify := nativeRuntimeObserver.notify
	nativeRuntimeObserver.RUnlock()
	if notify != nil {
		notify(agent.currentAgentSessionHandle())
	}
}

func init() {
	nativereplies.SetForegroundLookup(func(provider, nativeID string) string {
		var turnID string
		turnSessions.Range(func(_, value interface{}) bool {
			session, ok := value.(*Session)
			if !ok || session == nil {
				return true
			}
			handle := session.Snapshot()
			if handle == nil || handle.Provider.Provider != provider || handle.Provider.NativeSessionID != nativeID {
				return true
			}
			lifecycle := session.currentTurnLifecycle()
			if lifecycle != nil && !lifecycle.isTerminal() {
				turnID = lifecycle.id
				return false
			}
			return true
		})
		return turnID
	})
}
