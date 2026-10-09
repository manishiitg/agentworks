package security

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Light sandbox for commands the AgentWorks CLI runs on a person's own Mac (Local mode; owner decision 2026-10-09: it should feel
// like using a coding CLI locally). Everything works as in the person's terminal (real home folder, their environment, the whole
// network, system services such as file watchers) with three limits: writes only land in the shared folder, temporary folders and
// the person's home (not their shell startup files, keys or LaunchAgents), secrets are unreadable, and the folder rules
// (--block, read-only paths) hold. The server's own sandbox (StrictAllowlist) is unchanged.

// LightLocalPolicy describes one command's limits.
type LightLocalPolicy struct {
	// Home is the person's home folder (default: the current user's).
	Home string
	// WritePaths are the shared folders (and Downloads when shared); writes are allowed there.
	WritePaths []string
	// BlockedPaths are neither readable nor writable (the folder rules and the CLI's own state); BlockedWritePaths stay readable.
	BlockedPaths, BlockedWritePaths []string
}

// lightSecretPaths are never readable, relative to the home folder: keys, cloud logins, browser profiles and credential stores.
// The GitHub CLI login (.config/gh) and the login keychain (Library/Keychains, what git's osxkeychain helper reads) are NOT here: a
// command that cannot read them cannot `git push`, and Local mode should work as the person's own terminal does (owner,
// 2026-10-09). SSH keys stay hidden, so a push over SSH needs the HTTPS remote or the gh login.
var lightSecretPaths = []string{
	".ssh", ".aws", ".gnupg", ".kube", ".azure", ".config/gcloud", ".docker/config.json", ".netrc",
	"Library/Cookies", "Library/Safari", "Library/Mail", "Library/Messages",
	"Library/Application Support/Google/Chrome", "Library/Application Support/Firefox", "Library/Application Support/BraveSoftware",
	"Library/Application Support/Microsoft Edge", "Library/Application Support/Arc", "Library/Application Support/Vivaldi",
}

// lightProtectedWrites are inside the writable home but must not be changed by a command: they run later, outside any sandbox.
var lightProtectedWrites = []string{
	".zshrc", ".zshenv", ".zprofile", ".zlogin", ".zlogout", ".bashrc", ".bash_profile", ".bash_login", ".profile", ".bash_logout",
	".config/fish", "Library/LaunchAgents", ".local/bin/agentworks",
}

// lightLocalProfile is the Seatbelt profile for one command. Later rules override earlier ones, so the denies come last.
func lightLocalProfile(p LightLocalPolicy) string {
	home := p.Home
	var sb strings.Builder
	q := func(path string) string { return sandboxQuoted(canonicalPath(path)) }
	sb.WriteString("(version 1)\n(allow default)\n\n")
	sb.WriteString("; Writes land only in the shared folders, temporary folders, devices and the home folder.\n(deny file-write*)\n(allow file-write*\n")
	for _, path := range p.WritePaths {
		sb.WriteString(fmt.Sprintf("  (subpath \"%s\")\n", q(path)))
	}
	for _, path := range []string{"/private/tmp", "/private/var/folders", "/private/var/tmp", "/dev"} {
		sb.WriteString(fmt.Sprintf("  (subpath \"%s\")\n", sandboxQuoted(path)))
	}
	if home != "" {
		sb.WriteString(fmt.Sprintf("  (subpath \"%s\")\n", q(home)))
	}
	sb.WriteString(")\n\n")
	if home != "" {
		sb.WriteString("; Files that run later, outside any sandbox, are not for a command to change.\n(deny file-write*\n")
		for _, rel := range lightProtectedWrites {
			sb.WriteString(fmt.Sprintf("  (subpath \"%s\")\n", q(filepath.Join(home, rel))))
		}
		sb.WriteString(")\n\n; Keys, cloud logins, browser profiles and credential stores cannot be read (or changed).\n(deny file-read* file-write*\n")
		for _, rel := range lightSecretPaths {
			sb.WriteString(fmt.Sprintf("  (subpath \"%s\")\n", q(filepath.Join(home, rel))))
		}
		sb.WriteString(")\n\n")
	}
	if len(p.BlockedPaths) > 0 {
		sb.WriteString("; Folder rules and the CLI's own state: neither read nor written.\n(deny file-read* file-write*\n")
		for _, path := range p.BlockedPaths {
			sb.WriteString(fmt.Sprintf("  (subpath \"%s\")\n", q(path)))
		}
		sb.WriteString(")\n")
	}
	if len(p.BlockedWritePaths) > 0 {
		sb.WriteString("; Read-only paths: readable, never written.\n(deny file-write*\n")
		for _, path := range p.BlockedWritePaths {
			sb.WriteString(fmt.Sprintf("  (subpath \"%s\")\n", q(path)))
		}
		sb.WriteString(")\n")
	}
	return sb.String()
}

// lightLocalEnvironment is the person's own environment, without AgentWorks' own credentials and settings.
func lightLocalEnvironment(env []string) []string {
	out := make([]string, 0, len(env))
	for _, value := range env {
		if strings.HasPrefix(value, "AGENTWORKS_") {
			continue
		}
		out = append(out, value)
	}
	return out
}

func lightHome(p LightLocalPolicy) string {
	if p.Home != "" {
		return p.Home
	}
	home, _ := os.UserHomeDir()
	return home
}
