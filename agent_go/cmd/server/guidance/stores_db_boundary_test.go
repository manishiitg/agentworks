package guidance

import (
	"strings"
	"testing"
)

func TestStoresGuidanceRequiresManagedBuilderDatabaseTools(t *testing.T) {
	rendered, err := renderFromRegistry("stores", tmplData{}, referenceKinds)
	if err != nil {
		t.Fatalf("render stores guidance: %v", err)
	}

	for _, forbidden := range []string{
		"builder) MAY use `sqlite3",
		"use `sqlite3 db/db.sqlite` directly",
	} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("stores guidance still authorizes raw Builder SQLite access: %q", forbidden)
		}
	}
	for _, required := range []string{
		"database action=mutate",
		"db.sqlite-wal",
		"db.sqlite-shm",
		"stop and report that exact permission failure",
		"do not use a raw-database fallback",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("stores guidance missing managed DB boundary %q", required)
		}
	}
}
