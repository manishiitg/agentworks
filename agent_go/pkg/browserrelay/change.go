package browserrelay

import (
	"strings"
	"sync/atomic"
)

var changeHook atomic.Pointer[func(user, scope string)]

// SetChangeHook registers fn, called whenever a browser connection changes for
// a user and scope (the extension connects or disconnects, a project is
// selected or removed, the tab count changes). fn may run with relay locks
// held, so it must return at once and must not call back into the relay. The
// server uses it to wake that user's live-feed streams (GET /api/live).
func SetChangeHook(fn func(user, scope string)) {
	if fn == nil {
		changeHook.Store(nil)
		return
	}
	changeHook.Store(&fn)
}

// notifyChange reports a change of the binding with this key (user, 0x00, scope).
func notifyChange(bindingKey string) {
	fn := changeHook.Load()
	if fn == nil {
		return
	}
	user, scope, _ := strings.Cut(bindingKey, "\x00")
	(*fn)(user, scope)
}
