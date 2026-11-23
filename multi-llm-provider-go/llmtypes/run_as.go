package llmtypes

import (
	"path/filepath"
	"strings"
	"sync"
)

// RunAs is the application's explicit decision of which account a coding-CLI launch runs as (PLAT-442).
//
// Before it existed the provider guessed from the launch folder: a folder under <docs root>/_users/<id>/ ran as
// the slot that user holds. That made the identity depend on where a project happened to be stored, and a project
// moved to a shared folder would silently have lost its isolation. The application now decides per turn and says so:
//
//   - Declared with a User: the launch runs as that person's slot (a Code, or a Crew, whose owner this is; a
//     Crew Run-mode reader's turn names the Crew owner, not the reader).
//   - Declared with no User: the launch runs as the app account (a Goal).
//   - Not declared: nothing was decided; the provider falls back to the old folder rule and logs it.
type RunAs struct {
	Declared bool `json:"declared,omitempty"`
	// User is the person whose account the launch runs as; empty with Declared means the app account.
	User string `json:"user,omitempty"`
	// Slot is that person's slot account as the application resolved it. The provider checks it against the
	// host's slot table and logs a disagreement; the table wins.
	Slot string `json:"slot,omitempty"`
	// Root is the folder the decision covers (the turn's working folder): every launch file, home or working
	// folder at or under it runs as this identity.
	Root string `json:"root,omitempty"`
}

// declaredRunAs is the registry of explicit decisions, keyed by cleaned folder. Entries are small and bounded.
var declaredRunAs = struct {
	sync.Mutex
	byDir map[string]RunAs
	order []string
}{byDir: map[string]RunAs{}}

const declaredRunAsLimit = 4096

// DeclareRunAs records the decision for dir and everything under it. A later declaration for the same folder
// replaces the earlier one (the owner's turn and a reader's turn in the same Crew declare the same identity).
func DeclareRunAs(dir string, r RunAs) {
	dir = strings.TrimSpace(dir)
	if dir == "" || !r.Declared {
		return
	}
	dir = filepath.Clean(dir)
	declaredRunAs.Lock()
	defer declaredRunAs.Unlock()
	if _, exists := declaredRunAs.byDir[dir]; !exists {
		declaredRunAs.order = append(declaredRunAs.order, dir)
		if len(declaredRunAs.order) > declaredRunAsLimit {
			delete(declaredRunAs.byDir, declaredRunAs.order[0])
			declaredRunAs.order = declaredRunAs.order[1:]
		}
	}
	declaredRunAs.byDir[dir] = r
}

// DeclaredRunAs returns the decision covering hint: the declaration for the longest folder that is hint or a
// parent of it.
func DeclaredRunAs(hint string) (RunAs, bool) {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return RunAs{}, false
	}
	hint = filepath.Clean(hint)
	declaredRunAs.Lock()
	defer declaredRunAs.Unlock()
	for dir := hint; ; dir = filepath.Dir(dir) {
		if r, ok := declaredRunAs.byDir[dir]; ok {
			return r, true
		}
		if parent := filepath.Dir(dir); parent == dir || parent == "." {
			return RunAs{}, false
		}
	}
}

// ResetDeclaredRunAsForTest clears the registry.
func ResetDeclaredRunAsForTest() {
	declaredRunAs.Lock()
	defer declaredRunAs.Unlock()
	declaredRunAs.byDir, declaredRunAs.order = map[string]RunAs{}, nil
}

// DeclareRunAs registers the policy's explicit run-as decision (see RunAs) for the folders it covers: Root and
// the private home.
func (p *CLISecurityPolicy) DeclareRunAs() {
	if p == nil || !p.RunAs.Declared {
		return
	}
	DeclareRunAs(p.RunAs.Root, p.RunAs)
	DeclareRunAs(p.PrivateHome, p.RunAs)
}
