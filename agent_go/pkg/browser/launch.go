package browser

import "github.com/manishiitg/coding-agent-loop/workspace/browserconfig"

const SharedSessionName = browserconfig.SharedSession

func SharedBrowserEnabled() bool   { return browserconfig.SharedEnabled() }
func HeadlessLaunchArgs() []string { return browserconfig.HeadlessArgs() }

func HeadlessLaunchArgsForSession(session string) []string {
	return browserconfig.HeadlessArgsForSession(session)
}
func IsUserBrowserSession(session string) bool { return browserconfig.IsUserSession(session) }

// ProfilePathForSession returns the persistent Chrome profile directory this
// session launches with, or "" in session-isolated/ephemeral mode.
func ProfilePathForSession(session string) string {
	return browserconfig.ProfilePathForSession(session)
}
