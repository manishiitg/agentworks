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
const ProfileRootEnv = "AGENT_BROWSER_PROFILE_ROOT"

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

// SandboxSocketDir is where a managed browser's socket folder lands on the host when its daemon was started from
// inside a coding CLI's sandbox: that sandbox's private /tmp is the workspace's shared tmp folder, so
// /tmp/.agent-browser/o/<owner> there is <docs>/tmp/.agent-browser/o/<owner> here. Without it the live view and the
// cleanup look only in the host /tmp, find no stream, and the live view reconnects forever ("Browser restarted").
// Empty when the session is not managed or the docs folder is unknown.
func SandboxSocketDir(session string) string {
	own := SocketDirForSession(session)
	if own == SocketRoot {
		return ""
	}
	docs := strings.TrimSpace(os.Getenv("WORKSPACE_DOCS_PATH"))
	if docs == "" {
		docs = strings.TrimSpace(os.Getenv("DOCS_DIR"))
	}
	if docs == "" || !filepath.IsAbs(docs) {
		return ""
	}
	return filepath.Join(docs, "tmp", strings.TrimPrefix(own, "/tmp/"))
}

// SandboxSocketDirs lists every owner folder under <docs>/tmp/.agent-browser/o (see SandboxSocketDir).
func SandboxSocketDirs() []string {
	docs := strings.TrimSpace(os.Getenv("WORKSPACE_DOCS_PATH"))
	if docs == "" {
		docs = strings.TrimSpace(os.Getenv("DOCS_DIR"))
	}
	if docs == "" || !filepath.IsAbs(docs) {
		return nil
	}
	owners, _ := filepath.Glob(filepath.Join(docs, "tmp", strings.TrimPrefix(SocketRoot, "/tmp/"), "o", "*"))
	return owners
}

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
		// Explicit legacy configuration retains its ephemeral/invalid behavior.
		// New scoped browsers remember sign-ins without enabling global sharing.
		if _, explicit := os.LookupEnv(ProfileEnv); explicit || (!workflowSession.MatchString(session) && !projectSession.MatchString(session)) {
			return ""
		}
		base := strings.TrimSpace(os.Getenv(ProfileRootEnv))
		if base == "" {
			config, err := os.UserConfigDir()
			if err != nil {
				return ""
			}
			base = filepath.Join(config, "agentworks", "browser-profile")
		}
		if !filepath.IsAbs(base) || filepath.Clean(base) == string(filepath.Separator) {
			return ""
		}
		profile = filepath.Clean(base)
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

// RemoveEmptySessionSocketDirs removes a managed browser's socket folder (<tmp>/.agent-browser/o/<owner>, and the sandbox's shared-tmp
// twin) once its session is closed. A folder that still holds files (a live daemon's socket) is left alone: os.Remove only removes
// an empty directory.
func RemoveEmptySessionSocketDirs(session string) {
	if !IsUserSession(session) {
		return
	}
	for _, dir := range []string{SocketDirForSession(session), SandboxSocketDir(session)} {
		if dir != "" && dir != SocketRoot {
			_ = os.Remove(dir)
		}
	}
}
