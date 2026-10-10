// Package contractupgrade bounds when a workflow contract version may be stamped.
// Scheduled runs may execute an older saved contract, but they never own or
// authorize migrations; those remain operator-started work in workflow chat.
//
// Observed on customer-login 2026-08-12: the scheduler adjudicated the 1.0.21
// turn as failed at 08:01:35 (the agent had correctly declined — the improve
// archive held 19 finding IDs absent from the pulse_* tables). At 08:11:10 the
// same session, by then running an unrelated Pulse finalizer turn, wrote the
// stamp anyway with
//
//	curl -X POST -d '{"version":"1.0.21"}' -H "$MCP_AUTH" "$MCP_CUSTOM/set_workflow_contract_version"
//
// The next preflight read 1.0.21, trusted it, and moved on to 1.0.22 — skipping
// the 1.0.21 migration outright. Only a hand-written revert stopped that from
// sticking.
//
// Two properties of that failure decide this design. The write arrived over the
// MCP bridge rather than as a native tool call, so no prompt-level rule could
// have refused it: the bound has to sit in the executor both paths share. And
// the session was alive and legitimately working, so killing sessions at
// adjudication would truncate migrations mid-rewrite — worse than the bug.
//
// Grants are process-local on purpose. One grant authorizes stamps for the life
// of one turn; surviving a restart would mean authorizing a turn that is no
// longer running.
package contractupgrade

import (
	"strings"
	"sync"
)

var (
	mu     sync.Mutex
	grants = map[string]string{}
	// Sessions the scheduler drives. The fence binds only these.
	scheduled = map[string]bool{}
)

// MarkScheduled records that the scheduler owns this session for the whole of
// a scheduled run — schedule messages and Pulse. The marker prevents that
// unattended session from stamping a contract version.
//
// The fence exists because a *scheduled* session stamped a version ten minutes
// after its upgrade turn had been adjudicated, and the next preflight trusted
// it. It was never meant to bind an operator working in the workflow builder:
// there, a human asked for the migration and can see what the agent does, so
// the stamp is authorized by their presence. Enforcing it everywhere removed
// the only way a person could unblock a stalled upgrade by hand.
func MarkScheduled(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	scheduled[sessionID] = true
}

// ClearScheduled releases a session at the end of its scheduled run.
func ClearScheduled(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	delete(scheduled, sessionID)
	delete(grants, sessionID)
}

// IsScheduled reports whether the scheduler owns this session, and therefore
// whether a stamp from it needs an open upgrade turn.
func IsScheduled(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	mu.Lock()
	defer mu.Unlock()
	return scheduled[sessionID]
}

// Mint authorizes sessionID to stamp exactly target, replacing any previous
// authorization for that session. It is retained as a narrow compatibility
// primitive; normal scheduled runs never mint an upgrade grant.
//
// This used to take a set, because Pulse folded every outstanding rung into one
// Review+Fix turn and that turn could owe several stamps. Pulse no longer
// carries contract upgrades at all, so the set collapsed back to one.
func Mint(sessionID, target string) {
	sessionID = strings.TrimSpace(sessionID)
	target = strings.TrimSpace(target)
	if sessionID == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if target == "" {
		delete(grants, sessionID)
		return
	}
	grants[sessionID] = target
}

// Revoke withdraws sessionID's authorization. The scheduler calls this the
// moment it adjudicates a preflight turn — on the passing path as much as the
// failing one, since a turn that has been judged must not be able to change the
// thing it was judged on.
func Revoke(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	delete(grants, sessionID)
}

// Consume authorizes one stamp of version by sessionID and spends it, so a
// repeat call is refused. It reports whether the stamp is allowed.
func Consume(sessionID, version string) bool {
	sessionID = strings.TrimSpace(sessionID)
	version = strings.TrimSpace(version)
	if sessionID == "" || version == "" {
		return false
	}
	mu.Lock()
	defer mu.Unlock()
	if grants[sessionID] != version {
		return false
	}
	delete(grants, sessionID)
	return true
}

// Restore returns an authorization that Consume spent on a stamp which then
// failed before it changed workflow.json. Without it a transient write error
// would strand the turn: the migration is done, the grant is gone, and the
// retry it is told to make cannot succeed.
func Restore(sessionID, version string) {
	Mint(sessionID, version)
}

// Granted reports the version sessionID may still stamp, or "" when it holds no
// authorization. Used to explain a refusal.
func Granted(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	mu.Lock()
	defer mu.Unlock()
	return grants[sessionID]
}
