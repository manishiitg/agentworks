package browserrelay

import "testing"

// The server wakes the owner's live-feed streams from this hook: it must name the user and scope of the binding key.
func TestChangeHookNamesUserAndScope(t *testing.T) {
	var gotUser, gotScope string
	SetChangeHook(func(user, scope string) { gotUser, gotScope = user, scope })
	t.Cleanup(func() { SetChangeHook(nil) })
	notifyChange(key("user-1", "Chats/Code/projects/a"))
	if gotUser != "user-1" || gotScope != "Chats/Code/projects/a" {
		t.Fatalf("hook got user=%q scope=%q", gotUser, gotScope)
	}
}
