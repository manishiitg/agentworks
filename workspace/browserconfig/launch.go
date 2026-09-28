// Package browserconfig keeps all managed-browser launch paths consistent.
package browserconfig

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const SharedSession = "shared-browser"
const ProfileEnv = "AGENT_BROWSER_SHARED_PROFILE"

func SharedProfile() string {
	profile := strings.TrimSpace(os.Getenv(ProfileEnv))
	if !filepath.IsAbs(profile) || filepath.Clean(profile) == string(filepath.Separator) {
		return ""
	}
	return filepath.Clean(profile)
}

func SharedEnabled() bool { return SharedProfile() != "" }

// HeadlessArgs must be used for automation, viewer actions and recording alike.
// Managed Chrome keeps its native user agent and uses a persistent profile.
func HeadlessArgs() []string { return HeadlessArgsForSession("") }

// IsUserSession recognizes persistent managed browsers: one per workflow, one
// per project, and a per-session fallback, including an optional deployment
// prefix. The legacy per-user kinds (user, guest, workspace) stay recognized
// so browsers started before per-project isolation are still tracked and
// reaped. The name is retained for compatibility.
var userSession = regexp.MustCompile(`^(?:[A-Za-z0-9_-]+--)?(?:workflow|project|session|user|guest|workspace)-[a-f0-9]{16}--browser$`)
var workflowSession = regexp.MustCompile(`^(?:[A-Za-z0-9_-]+--)?workflow-[a-f0-9]{16}--browser$`)
var projectSession = regexp.MustCompile(`^(?:[A-Za-z0-9_-]+--)?project-[a-f0-9]{16}--browser$`)

func IsUserSession(session string) bool { return userSession.MatchString(session) }

// SocketRoot is where agent-browser daemons kept their sockets before each
// managed browser got its own folder; the global shared browser still does.
const SocketRoot = "/tmp/.agent-browser"

var managedOwner = regexp.MustCompile(`(workflow|project|session|user|guest|workspace)-([a-f0-9]{16})--browser$`)

// SocketDirForSession is the socket folder of one managed browser: one per
// workflow, Crew/Code project or session. Sandboxed commands are granted only
// their own browser's folder, so one owner's agent cannot drive another
// owner's browser through a shared socket folder. Kept short: a Unix socket
// path must fit in 103 bytes and agent-browser names it <session>.sock.
func SocketDirForSession(session string) string {
	m := managedOwner.FindStringSubmatch(session)
	if m == nil || !IsUserSession(session) {
		return SocketRoot
	}
	return filepath.Join(SocketRoot, "o", m[1][:1]+m[2])
}

// ProfilePathForSession returns the persistent Chrome profile directory this
// session launches with, or "" when no shared profile is configured
// (session-isolated/ephemeral mode, no --profile flag at all).
func ProfilePathForSession(session string) string {
	profile := SharedProfile()
	if profile == "" {
		return ""
	}
	if IsUserSession(session) {
		profileRoot := profile + "-users"
		if workflowSession.MatchString(session) {
			profileRoot = profile + "-workflows"
		} else if projectSession.MatchString(session) {
			profileRoot = profile + "-projects"
		}
		profile = filepath.Join(profileRoot, session)
	}
	return profile
}

func HeadlessArgsForSession(session string) []string {
	// Servers have no physical media devices. Keep these options identical in
	// automation, live controls, capture and the persistent browser supervisor.
	const mediaArgs = ",--use-fake-device-for-media-stream,--use-fake-ui-for-media-stream"
	if profile := ProfilePathForSession(session); profile != "" {
		return []string{"--profile", profile, "--idle-timeout", "0", "--args", "--no-sandbox,--disable-gpu,--disable-blink-features=AutomationControlled,--lang=en-US,--restore-last-session" + mediaArgs}
	}
	return []string{"--user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36", "--args", "--no-sandbox,--disable-gpu,--disable-blink-features=AutomationControlled" + mediaArgs}
}
