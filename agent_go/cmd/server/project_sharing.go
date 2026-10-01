package server

import (
	"os"
	"strings"
)

// projectSharingEnabled says whether a user may see and work in another user's project (a Crew shared
// through the project directory: listed for every account, opened by non-owners as readers). It is off:
// projects, like Code workspaces, are private to their owner. This removes the cross-user project
// directory and reader access in one place; the code behind it is kept so it can be turned back on with
// AGENTWORKS_PROJECT_SHARING=on. It is a variable so tests of the reader flows can switch it on.
// Why: with per-user Linux accounts (slots) a non-owner's commands run as their own account and cannot
// work inside the owner's folder; a private project needs nothing shared (docs/DECISIONS.md, 2026-10-01).
var projectSharingEnabled = func() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("AGENTWORKS_PROJECT_SHARING")), "on")
}
