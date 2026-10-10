package security

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
)

// A command run as a user's slot account never receives an app-private path (PLAT-478): the managed browsers'
// profiles (logged-in sessions, app-owned 0700), their socket folders, and the app's state area. The slot account
// cannot open them by Unix permissions anyway, and the launcher, which runs as the slot, used to fail the whole
// command when it could not even stat one ("SANDBOX_UNAVAILABLE: inspect Landlock path: stat
// .../browser-profile-projects/project-...--browser: permission denied", server A 2026-10-04). They must not become
// readable to slots either, so the rule is: not granted, never opened up. A slot drives its project's browser
// through the platform (a standalone agent-browser command runs as the service account, see
// handlers.isStandaloneBrowserCommand, and agent_go's agent_browser tool).

// appPrivateRoots lists the folders no slot command is granted anything inside. The app's state root
// (AGENTWORKS_STATE_ROOT) is one unless it contains one of keep (the workspace or the slot's own home): on such a
// layout it is not a private tree, and dropping it would take the command's own folders away. The browser roots are
// always private.
func appPrivateRoots(keep ...string) []string {
	var roots []string
	addProfile := func(profile string) {
		profile = strings.TrimSpace(profile)
		if profile == "" || !filepath.IsAbs(profile) || filepath.Clean(profile) == string(filepath.Separator) {
			return
		}
		profile = filepath.Clean(profile)
		roots = append(roots, profile, profile+"-users", profile+"-workflows", profile+"-projects")
	}
	addProfile(browserconfig.SharedProfile())
	addProfile(os.Getenv(browserconfig.ProfileRootEnv))
	if config, err := os.UserConfigDir(); err == nil {
		// ProfilePathForSession's default when neither variable is set.
		addProfile(filepath.Join(config, "agentworks", "browser-profile"))
	}
	socketRoots := []string{browserconfig.SocketRoot, browserconfig.ManagedSocketRoot()}
	roots = append(roots, socketRoots...)
	for _, key := range []string{"WORKSPACE_DOCS_PATH", "DOCS_DIR"} {
		// The sandbox twin of the socket folders (<docs>/tmp/.agent-browser): other owners' browser daemons.
		if docs := strings.TrimSpace(os.Getenv(key)); docs != "" && filepath.IsAbs(docs) {
			for _, socketRoot := range socketRoots {
				roots = append(roots, filepath.Join(filepath.Clean(docs), "tmp", strings.TrimPrefix(socketRoot, "/tmp/")))
			}
		}
	}
	if state := strings.TrimSpace(os.Getenv("AGENTWORKS_STATE_ROOT")); state != "" && filepath.IsAbs(state) && filepath.Clean(state) != string(filepath.Separator) {
		state = filepath.Clean(state)
		holdsKept := false
		for _, k := range keep {
			if k = strings.TrimSpace(k); k != "" && (pathWithin(k, state) || pathWithin(canonicalPath(k), canonicalPath(state))) {
				holdsKept = true
			}
		}
		if !holdsKept {
			roots = append(roots, state)
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, root := range roots {
		for _, spelling := range []string{root, canonicalPath(root)} {
			if spelling != "" && !seen[spelling] {
				seen[spelling] = true
				out = append(out, spelling)
			}
		}
	}
	return out
}

// withoutAppPrivatePaths drops every path inside an app-private root from a slot command's grants and logs each
// one (granting less is the safe direction; the command still runs under the rest of its policy). Paths of a
// command that does not run as a slot are returned unchanged. keep: see appPrivateRoots.
func withoutAppPrivatePaths(slot string, paths []string, keep ...string) []string {
	if slot == "" || len(paths) == 0 {
		return paths
	}
	roots := appPrivateRoots(keep...)
	kept := make([]string, 0, len(paths))
	for _, path := range paths {
		private := false
		for _, root := range roots {
			if pathWithin(path, root) || pathWithin(canonicalPath(path), root) {
				private = true
				break
			}
		}
		if private {
			log.Printf("[SLOT_GRANT] not granted to %s: %s is app-private (browser profile, browser socket or app state)", slot, path)
			continue
		}
		kept = append(kept, path)
	}
	return kept
}

// grantStatSkippable says whether the launcher may leave a requested grant out instead of refusing the command:
// only when the account it runs as may not even look at the path (EACCES/EPERM). Such a grant could not give the
// command anything the account's own permissions allow, so omitting it grants less, never more. Every other error
// keeps failing the command closed.
func grantStatSkippable(err error) bool {
	return err != nil && errors.Is(err, fs.ErrPermission)
}
