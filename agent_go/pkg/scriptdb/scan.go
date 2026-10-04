// Package scriptdb finds Python scripts that open a workflow's SQLite database
// themselves. Scripts reach the database through the built-in agentworks_db
// helper (the managed query and mutate tools); a script that opens $DB_PATH or
// db.sqlite predates it. A bare sqlite3 import is allowed (a user's own file). The contract migration to the
// managed helper uses this scan: the Builder tool reports what is left to
// convert, and the version stamp is refused until none is.
package scriptdb

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Finding is what one script does that the managed helper replaces.
type Finding struct {
	File string   `json:"file"`
	Raw  []string `json:"raw_db,omitempty"` // direct database access, each "line N: <code>"
	DDL  []string `json:"ddl,omitempty"`    // schema statements, each "line N: <code>"
}

// Clean reports that the script needs nothing converted.
func (f Finding) Clean() bool { return len(f.Raw) == 0 && len(f.DDL) == 0 }

var (
	// The workflow's own database is reached through $DB_PATH or the db.sqlite
	// file name. A bare sqlite3 import is not flagged: a script or a Relay
	// Python tool may read a user's own SQLite file (PLAT-423).
	rawDBPattern = regexp.MustCompile(`\bDB_PATH\b|\bdb\.sqlite\b`)
	ddlPattern   = regexp.MustCompile(`(?i)\b(CREATE\s+(TEMP\w*\s+|UNIQUE\s+|VIRTUAL\s+)?(TABLE|INDEX|VIEW|TRIGGER)|ALTER\s+TABLE|DROP\s+(TABLE|INDEX|VIEW|TRIGGER))\b`)
)

// maxEvidence bounds the lines reported per script and kind.
const maxEvidence = 5

// Scannable reports whether a file is a script the migration is about. Test files
// may open the database to check what a script wrote, and the managed helper
// itself is not a script of the workflow.
func Scannable(path string) bool {
	base := filepath.Base(path)
	switch {
	case !strings.HasSuffix(base, ".py"):
		return false
	case strings.HasPrefix(base, "test_"), strings.HasSuffix(base, "_test.py"), base == "conftest.py":
		return false
	case base == "agentworks_db.py", strings.Contains(filepath.ToSlash(path), "/__pycache__/"):
		return false
	}
	return true
}

// ScanSource scans one script's text. Comment-only lines are ignored.
func ScanSource(file, source string) Finding {
	finding := Finding{File: file}
	for number, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		note := func() string {
			if len(trimmed) > 120 {
				trimmed = trimmed[:120] + "..."
			}
			return fmt.Sprintf("line %d: %s", number+1, trimmed)
		}
		if rawDBPattern.MatchString(trimmed) && len(finding.Raw) < maxEvidence {
			finding.Raw = append(finding.Raw, note())
		}
		if ddlPattern.MatchString(trimmed) && len(finding.DDL) < maxEvidence {
			finding.DDL = append(finding.DDL, note())
		}
	}
	return finding
}

// ScanDir scans every script under codeRoot (a workflow's code/ folder) and
// returns the ones that still need converting, sorted by path. A missing folder
// is a workflow with no scripts.
func ScanDir(codeRoot string) (findings []Finding, scanned int, err error) {
	err = filepath.WalkDir(codeRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || !Scannable(path) {
			return nil
		}
		// Report-data scripts (code/reports/) read a deliberate read-only
		// snapshot through $DB_PATH; they are not steps and stay as they are.
		if relative, relErr := filepath.Rel(codeRoot, path); relErr == nil && strings.HasPrefix(filepath.ToSlash(relative), "reports/") {
			return nil
		}
		data, readErr := os.ReadFile(path) // #nosec G304 -- inside the workflow's own folder
		if readErr != nil {
			return readErr
		}
		scanned++
		relative, relErr := filepath.Rel(codeRoot, path)
		if relErr != nil {
			relative = path
		}
		if finding := ScanSource(filepath.ToSlash(relative), string(data)); !finding.Clean() {
			findings = append(findings, finding)
		}
		return nil
	})
	sort.Slice(findings, func(i, j int) bool { return findings[i].File < findings[j].File })
	return findings, scanned, err
}

// Summary is a short, human and agent readable list of what is left.
func Summary(findings []Finding) string {
	var b strings.Builder
	for _, f := range findings {
		fmt.Fprintf(&b, "- code/%s", f.File)
		if len(f.Raw) > 0 {
			fmt.Fprintf(&b, " opens the database directly (%s)", f.Raw[0])
		}
		if len(f.DDL) > 0 {
			fmt.Fprintf(&b, "; schema statement (%s): a migration, not a script", f.DDL[0])
		}
		b.WriteString("\n")
	}
	return b.String()
}
