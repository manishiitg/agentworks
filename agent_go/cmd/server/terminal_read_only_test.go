package server

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// The native terminal is for owners and editors: a caller looking at their OWN session
// with read-only access (a Crew reader, a read-only login, a read-only channel) is
// refused, and nobody else is (an owner, an editor, an admin viewing another user's
// session, a guest call on an owner's session).
func TestTerminalDeniedForReadOnlyAccess(t *testing.T) {
	const sid = "read-only-terminal-session"
	t.Cleanup(func() { common.ClearSessionShellConfig(sid) })

	if terminalDeniedForReadOnlyAccess(sid, "reader", "reader") {
		t.Fatal("a session with no read-only mark was refused")
	}
	common.SetSessionReadOnlyAccess(sid, true)
	if !terminalDeniedForReadOnlyAccess(sid, "reader", "reader") {
		t.Fatal("a read-only caller's own session was not refused")
	}
	if terminalDeniedForReadOnlyAccess(sid, "admin", "reader") {
		t.Fatal("an admin viewing another user's read-only session was refused")
	}
	if terminalDeniedForReadOnlyAccess(sid, "", "reader") {
		t.Fatal("an unidentified caller was treated as the session's user")
	}
	// The mark is refreshed every turn: access can change, and a session that is no
	// longer read-only gets its terminal back.
	common.SetSessionReadOnlyAccess(sid, false)
	if terminalDeniedForReadOnlyAccess(sid, "reader", "reader") {
		t.Fatal("a session that is no longer read-only is still refused")
	}
}
