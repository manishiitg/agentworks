package scriptdb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanSourceFindsDirectDatabaseAccessAndSchemaStatements(t *testing.T) {
	source := `import os
import sqlite3
# conn = sqlite3.connect(os.environ["DB_PATH"])  <- a comment is not use
conn = sqlite3.connect(os.environ["DB_PATH"])
conn.execute("CREATE TABLE IF NOT EXISTS leads (id INTEGER PRIMARY KEY)")
conn.execute("alter table leads add column note text")
rows = conn.execute("SELECT * FROM leads").fetchall()
`
	finding := ScanSource("step/main.py", source)
	if len(finding.Raw) == 0 || !strings.Contains(finding.Raw[0], "line 2") {
		t.Errorf("direct access not found at line 2: %v", finding.Raw)
	}
	if len(finding.DDL) != 2 || !strings.Contains(finding.DDL[1], "alter table") {
		t.Errorf("DDL = %v, want the CREATE and the ALTER (case-insensitive)", finding.DDL)
	}
	if finding.Clean() {
		t.Error("a script that opens the database is not clean")
	}
}

func TestScanSourceLeavesAHelperScriptAlone(t *testing.T) {
	source := `from agentworks_db import query, execute_many
rows = query("SELECT id FROM leads WHERE status = ?", ["new"])
execute_many("INSERT INTO leads(id) VALUES (?)", [[1], [2]])
`
	if finding := ScanSource("step/main.py", source); !finding.Clean() {
		t.Errorf("a helper script must scan clean: %+v", finding)
	}
}

func TestScannableSkipsTestsTheHelperAndCaches(t *testing.T) {
	for path, want := range map[string]bool{
		"step-a/main.py": true, "shared/lib.py": true, "step-a/helpers/db_utils.py": true,
		"step-a/test_main.py": false, "step-a/main_test.py": false, "step-a/conftest.py": false,
		"shared/agentworks_db.py": false, "step-a/__pycache__/main.cpython-312.pyc": false,
		"step-a/__pycache__/x.py": false, "step-a/README.md": false, "step-a/query.sql": false,
	} {
		if got := Scannable(path); got != want {
			t.Errorf("Scannable(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestScanDirReportsOnlyScriptsThatNeedConverting(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("step-b/main.py", "import sqlite3\nsqlite3.connect('x')\n")
	write("step-a/main.py", "from agentworks_db import query\nquery('SELECT 1')\n")
	write("step-c/main.py", "import os\nopen(os.environ['DB_PATH'])\n")
	write("step-c/test_main.py", "import sqlite3\n")
	write("reports/summary.py", "import os\nopen(os.environ['DB_PATH'])\n")
	findings, scanned, err := ScanDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if scanned != 3 {
		t.Errorf("scanned %d scripts, want 3 (the test file is not one)", scanned)
	}
	if len(findings) != 2 || findings[0].File != "step-b/main.py" || findings[1].File != "step-c/main.py" {
		t.Fatalf("findings = %+v, want step-b then step-c, sorted", findings)
	}
	if missing, n, err := ScanDir(filepath.Join(root, "nope")); err != nil || n != 0 || len(missing) != 0 {
		t.Errorf("a missing code folder is a workflow without scripts: %v %d %v", missing, n, err)
	}
	if !strings.Contains(Summary(findings), "code/step-b/main.py") {
		t.Error("the summary must name the files")
	}
}
