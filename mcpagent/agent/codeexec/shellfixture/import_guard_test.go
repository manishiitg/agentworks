package shellfixture_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Nothing outside a test may import this package.
//
// The executor here used to live in package codeexec, where it read as platform
// infrastructure: exported, production-looking, beside the live tool registry.
// On 2026-08-04 its 100KB cap was audited as the platform's shell cap while the
// real path — the workspace service's /api/execute — was uncapped, and a coding
// CLI rejected twelve results of up to 130,046 characters. Moving the code fixes
// that reading; this test is what keeps it fixed.
func TestNoProductionCodeImportsTheShellFixture(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}

	const pkgPath = "github.com/manishiitg/mcpagent/agent/codeexec/shellfixture"
	var offenders []string

	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); name == "vendor" || name == ".git" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return nil // unparsable files are not this test's concern
		}
		for _, imported := range file.Imports {
			if strings.Trim(imported.Path.Value, `"`) == pkgPath {
				rel, _ := filepath.Rel(root, path)
				offenders = append(offenders, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}

	if len(offenders) > 0 {
		t.Fatalf("non-test files import the shell fixture: %v\n"+
			"It has no folder guard and no sandbox, and its output cap describes only itself. "+
			"A hosted agent's shell belongs in the workspace service.", offenders)
	}
}
