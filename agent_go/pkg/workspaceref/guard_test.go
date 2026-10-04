package workspaceref

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// PLAT-435 guard: production code outside this package must not handle the
// "_users" path segment itself (split, strip, test or join it). Everything
// goes through Parse and a Ref. The test fails on
//
//  1. a string literal that spells the segment ("_users/...", "_users",
//     "x/_users/%s"; prose containing whitespace, such as tool help text, is
//     not path handling and is ignored), and
//  2. a use of the UsersDir constant,
//
// in any non-test Go file under agent_go that is not listed below. The lists
// are deliberately short and every entry carries its reason. Do not add an
// entry to make a new caller compile: use workspaceref.

// literalAllowlist: files (or directory prefixes ending in "/") allowed to
// spell "_users" in a string literal.
var literalAllowlist = map[string]string{
	"cmd/testing/": "developer e2e commands: flag defaults and fixtures naming the default user's folder; not a server code path",
}

// usersDirAllowlist: files allowed to use UsersDir, the directory name. All are
// physical-storage layers that join it onto a document root on disk, or a
// one-shot migration of legacy data.
var usersDirAllowlist = map[string]string{
	"cmd/server/auth_rotate_cmd.go":                "globs the per-user directories on disk to rotate stored credentials",
	"cmd/server/product_secrets_migration.go":      "one-shot on-disk migration of per-user secrets files",
	"cmd/server/durable_chat_migration_command.go": "one-shot migration reading the owner out of legacy on-disk chat paths",
	"cmd/server/virtual-tools/delegation_tools.go": "const fallback Chats folder; a const cannot call PhysicalPath",
	"internal/videoproduct/managed_skills.go":      "walks the per-user directories on disk",
	"internal/sparkquillproduct/migrate.go":        "one-shot on-disk migration of SparkQuill folders",
	"pkg/chathistory/fs_store.go":                  "physical storage layer of per-user secrets (on-disk path)",
	"pkg/workspace/client.go":                      "folder-guard rewrite at the workspace API client boundary (unsanitized user id, see PLAT-435 ticket)",
	"pkg/workspace/diff_patch_workspace_file.go":   "first-segment check on a client path that is already workspace-root-qualified",
}

var usersSegment = regexp.MustCompile(`(^|/)_users($|/|%)`)

type finding struct {
	file string
	line int
	what string
}

func scanSource(name string, src any) ([]finding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, 0) // comments skipped
	if err != nil {
		return nil, err
	}
	var out []finding
	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.BasicLit:
			if v.Kind != token.STRING {
				return true
			}
			if s, err := strconv.Unquote(v.Value); err == nil && usersSegment.MatchString(s) && !strings.ContainsAny(s, " \t\n") {
				out = append(out, finding{name, fset.Position(v.Pos()).Line, "literal " + v.Value})
			}
		case *ast.SelectorExpr:
			if id, ok := v.X.(*ast.Ident); ok && id.Name == "workspaceref" && v.Sel.Name == "UsersDir" {
				out = append(out, finding{name, fset.Position(v.Pos()).Line, "workspaceref.UsersDir"})
			}
		}
		return true
	})
	return out, nil
}

func TestGuardScannerDetects(t *testing.T) {
	src := `package x
import "strings"
func f(p string) bool { return strings.HasPrefix(p, "_users/") }
func g(u string) string { return "_users/" + u }
func h(u string) string { return "a/_users/%s" }
func i() string { return "_users" }
func j() string { return workspaceref.UsersDir }
// "_users/" in a comment is fine
func k() string { return "my_users_table" }
`
	got, err := scanSource("x.go", src)
	if err != nil || len(got) != 5 {
		t.Fatalf("findings = %v (%v), want 5", got, err)
	}
}

func TestNoUsersPathHandlingOutsideWorkspaceref(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	var violations []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "vendor", ".git", "frontend":
				return filepath.SkipDir
			}
			if rel == "pkg/workspaceref" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		found, err := scanSource(rel, src)
		if err != nil {
			return nil // not this test's job to fail on unparsable generated files
		}
		for _, f := range found {
			allowed := false
			if f.what == "workspaceref.UsersDir" {
				_, allowed = usersDirAllowlist[rel]
			} else {
				for prefix := range literalAllowlist {
					if rel == prefix || (strings.HasSuffix(prefix, "/") && strings.HasPrefix(rel, prefix)) {
						allowed = true
					}
				}
			}
			if !allowed {
				violations = append(violations, f.file+":"+strconv.Itoa(f.line)+" "+f.what)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("PLAT-435: %d place(s) handle the _users path segment outside pkg/workspaceref. Use workspaceref.Parse / Ref "+
			"(Logical, Physical, OwnedBy, SameFor, Project) instead:\n  %s", len(violations), strings.Join(violations, "\n  "))
	}
	// Every allowlist entry must still need its exemption.
	for file := range usersDirAllowlist {
		src, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Errorf("allowlisted file %s: %v", file, err)
			continue
		}
		if found, _ := scanSource(file, src); len(found) == 0 {
			t.Errorf("%s no longer uses UsersDir: remove it from usersDirAllowlist", file)
		}
	}
}
