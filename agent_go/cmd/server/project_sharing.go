package server

import (
	"os"
	"strings"
)

// projectSharingEnabled says whether a user may see and work in another user's project (a Crew shared
// through the project directory: listed for every account, opened by non-owners as readers). It is on by
// default; AGENTWORKS_PROJECT_SHARING=off makes projects private to their owner, as Code workspaces always
// are. It is a variable so tests can switch it.
// Why: Crew turns run on the app account (2026-10-04), so a reader does not need to work inside the owner's
// slot folder, and Crews listed by list_crews must also open (docs/DECISIONS.md, 2026-10-08).
var projectSharingEnabled = func() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("AGENTWORKS_PROJECT_SHARING")), "off")
}
